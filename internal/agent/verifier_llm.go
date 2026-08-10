// 真实验证器把猜想、任务与已登记证据交给大模型，回填系统编号后产出判决
//
// 与假验证器共享同一验证边界：一次调用返回完整判断列表，不在角色内多轮调工具
// 判断只能引用输入中已存在的证据编号；猜想与任务仅作上下文
// 判决的系统编号与创建时间由本模块经工厂发放（对齐解析器与规划器）
package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"aruing/internal/core"
	"aruing/internal/llm"
)

//go:embed prompts/verifier.md
var verifierPrompt string

// 验证业务级重试次数：结构合法但语义违规时重新请求
const maxVerifyAttempts = 3

// 用大模型基于已登记证据判断猜想的验证器
//
// 持有不可变依赖（客户端、工厂、提示词），可被多次运行复用
// 不持有跨运行可变状态；每次验证独立发结构化生成
type LLMVerifier struct {
	// 大模型客户端，每次验证发一次结构化生成
	client llm.Client
	// 领域编号与创建时间工厂，回填判决时使用
	factory *core.Factory
	// 嵌入的系统提示词全文，构造后只读
	prompt string
}

// 创建基于大模型的验证器，提示词从包内嵌入文件加载
// 任一依赖缺失直接返回错误，避免运行期才暴露初始化问题
func NewLLMVerifier(client llm.Client, factory *core.Factory) (*LLMVerifier, error) {
	if client == nil {
		return nil, errors.New("LLM verifier requires an llm client")
	}
	if factory == nil {
		return nil, errors.New("LLM verifier requires a factory")
	}
	return &LLMVerifier{client: client, factory: factory, prompt: verifierPrompt}, nil
}

// 请求模型对每条猜想给出判断，校验后回填系统编号与运行绑定
//
// 模型若返回结构合法但语义违规的输出（未知证据、漏判猜想等），在业务级重试内重新请求；
// 重试上限次仍不合规则返回模型输出不一致错误
func (v *LLMVerifier) Verify(
	ctx context.Context,
	query core.Query,
	hypotheses []core.Hypothesis,
	tasks []core.Task,
	evidence []core.Evidence,
) ([]core.Verdict, error) {
	if ctx == nil {
		return nil, errors.New("verifier requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("verify evidence: %w", err)
	}
	if v == nil {
		return nil, errors.New("verifier is required")
	}
	if len(hypotheses) == 0 {
		return nil, errors.New("verifier requires at least one hypothesis")
	}
	if strings.TrimSpace(query.RunID) == "" {
		return nil, errors.New("verifier requires a query with run ID")
	}

	runID := strings.TrimSpace(hypotheses[0].RunID)
	if runID == "" {
		return nil, errors.New("verifier requires a run ID on hypotheses")
	}
	if strings.TrimSpace(query.RunID) != runID {
		return nil, fmt.Errorf("query run ID %q does not match hypotheses run ID %q", query.RunID, runID)
	}
	for i, h := range hypotheses {
		if strings.TrimSpace(h.ID) == "" {
			return nil, fmt.Errorf("hypothesis[%d] id is required", i)
		}
		if strings.TrimSpace(h.RunID) != runID {
			return nil, fmt.Errorf("hypothesis[%d] run ID %q does not match %q", i, h.RunID, runID)
		}
	}

	userPayload, err := buildVerifierUserPayload(query, hypotheses, tasks, evidence)
	if err != nil {
		return nil, fmt.Errorf("build verify prompt: %w", err)
	}

	req := llm.Request{
		System: v.prompt,
		User:   userPayload,
	}

	var lastOut verifierLLMOutput
	var lastValidateErr error
	for attempt := 0; attempt < maxVerifyAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("verify evidence: %w", err)
		}

		var out verifierLLMOutput
		if gErr := v.client.GenerateJSON(ctx, req, &out); gErr != nil {
			return nil, fmt.Errorf("verify with LLM: %w", gErr)
		}

		if vErr := validateVerifierOutput(out, hypotheses, evidence); vErr != nil {
			lastOut = out
			lastValidateErr = vErr
			continue
		}

		return v.fillVerdicts(runID, out)
	}

	return nil, fmt.Errorf("%w: last error: %v, last output: %+v",
		ErrLLMOutputInconsistent, lastValidateErr, lastOut)
}

// 序列化验证输入：用户原始问题、猜想、任务与已登记证据
// 问题结构让判断角色能比对证据与用户实际提问的现象或对象（如域名、症状）
// 只暴露判断所需字段，避免把无关运行元数据塞进提示词
func buildVerifierUserPayload(
	query core.Query,
	hypotheses []core.Hypothesis,
	tasks []core.Task,
	evidence []core.Evidence,
) (string, error) {
	// 问题视图：目标已拼入节点文本，便于对照现象
	type queryView struct {
		// 问题结构编号
		ID string `json:"id,omitempty"`
		// 所属运行编号
		RunID string `json:"runId,omitempty"`
		// 目标与节点文本拼合后的描述
		Goal string `json:"goal,omitempty"`
	}
	// 单条猜想的精简视图
	type hypView struct {
		// 猜想系统编号，判断必须按此回填
		ID string `json:"id"`
		// 猜想陈述
		Statement string `json:"statement"`
		// 提出理由
		Reason string `json:"reason,omitempty"`
		// 预期信号列表
		ExpectedSignals []string `json:"expectedSignals,omitempty"`
	}
	// 任务上下文，帮助理解证据从何而来
	type taskView struct {
		// 任务系统编号
		ID string `json:"id"`
		// 关联实体引用
		Refs []string `json:"refs,omitempty"`
		// 工具名
		ToolName string `json:"toolName"`
		// 取证目的
		Purpose string `json:"purpose,omitempty"`
	}
	// 已登记证据视图，判断只能引用其中的编号
	type evidenceView struct {
		// 证据系统编号
		ID string `json:"id"`
		// 产出任务编号
		TaskID string `json:"taskId"`
		// 工具名
		ToolName string `json:"toolName"`
		// 可展示命令视图
		CommandView string `json:"commandView,omitempty"`
		// 摘要
		Summary string `json:"summary"`
		// 失败错误，成功时可空
		Error string `json:"error,omitempty"`
		// 原始输出片段
		Raw json.RawMessage `json:"raw,omitempty"`
	}
	// 验证角色完整用户载荷
	type payload struct {
		// 用户问题上下文
		Query queryView `json:"query"`
		// 待判断猜想列表
		Hypotheses []hypView `json:"hypotheses"`
		// 本轮相关任务
		Tasks []taskView `json:"tasks"`
		// 已登记证据，证据引用不得越界
		Evidence []evidenceView `json:"evidence"`
	}

	// 节点文本拼进目标，让大模型看到用户问题的完整结构（域名、症状、资源名等）
	parts := []string{query.Goal}
	for _, n := range query.Nodes {
		if strings.TrimSpace(n.Text) != "" {
			parts = append(parts, n.Text)
		}
	}
	p := payload{
		Query: queryView{
			ID:    query.ID,
			RunID: query.RunID,
			Goal:  strings.TrimSpace(strings.Join(parts, " / ")),
		},
		Hypotheses: make([]hypView, 0, len(hypotheses)),
		Tasks:      make([]taskView, 0, len(tasks)),
		Evidence:   make([]evidenceView, 0, len(evidence)),
	}
	for _, h := range hypotheses {
		p.Hypotheses = append(p.Hypotheses, hypView{
			ID:              h.ID,
			Statement:       h.Statement,
			Reason:          h.Reason,
			ExpectedSignals: append([]string(nil), h.ExpectedSignals...),
		})
	}
	for _, t := range tasks {
		p.Tasks = append(p.Tasks, taskView{
			ID:       t.ID,
			Refs:     append([]string(nil), t.Refs...),
			ToolName: t.ToolName,
			Purpose:  t.Purpose,
		})
	}
	for _, e := range evidence {
		p.Evidence = append(p.Evidence, evidenceView{
			ID:          e.ID,
			TaskID:      e.TaskID,
			ToolName:    e.ToolName,
			CommandView: e.CommandView,
			Summary:     e.Summary,
			Error:       e.Error,
			Raw:         append(json.RawMessage(nil), e.Raw...),
		})
	}

	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// 模型输出的中间结构，只含判断内容，不含系统编号或时间
type verifierLLMOutput struct {
	// 对每条输入猜想的判断列表
	Verdicts []verifierVerdictOut `json:"verdicts"`
}

// 模型侧一条判断，判决编号由回填时经工厂发放
type verifierVerdictOut struct {
	// 对应输入猜想的系统编号
	HypothesisID string `json:"hypothesis_id"`
	// 结果：支持、排除或证据不足
	Result string `json:"result"`
	// 判断理由，须可追溯到证据
	Reason string `json:"reason"`
	// 支撑本判断的证据编号，须全部属于输入证据
	EvidenceIDs []string `json:"evidence_ids"`
}

// 校验模型输出满足编排与报告的最小契约
//
// 校验项：
//   - 每条输入猜想恰好一条判断
//   - 判定结果枚举合法
//   - 理由非空
//   - 证据编号非空且全部属于输入证据
func validateVerifierOutput(
	out verifierLLMOutput,
	hypotheses []core.Hypothesis,
	evidence []core.Evidence,
) error {
	if len(out.Verdicts) == 0 {
		return errors.New("at least one verdict is required")
	}

	knownHyp := make(map[string]struct{}, len(hypotheses))
	for _, h := range hypotheses {
		knownHyp[h.ID] = struct{}{}
	}
	knownEvidence := make(map[string]struct{}, len(evidence))
	for _, e := range evidence {
		if strings.TrimSpace(e.ID) != "" {
			knownEvidence[e.ID] = struct{}{}
		}
	}

	seenHyp := make(map[string]struct{}, len(out.Verdicts))
	for i, verdict := range out.Verdicts {
		hypID := strings.TrimSpace(verdict.HypothesisID)
		if hypID == "" {
			return fmt.Errorf("verdict[%d] hypothesis_id is required", i)
		}
		if _, ok := knownHyp[hypID]; !ok {
			return fmt.Errorf("verdict[%d] references unknown hypothesis %q", i, hypID)
		}
		if _, dup := seenHyp[hypID]; dup {
			return fmt.Errorf("verdict[%d] duplicates hypothesis %q", i, hypID)
		}
		seenHyp[hypID] = struct{}{}

		if !isValidVerdictResult(verdict.Result) {
			return fmt.Errorf("verdict[%d] result %q is invalid", i, verdict.Result)
		}
		if strings.TrimSpace(verdict.Reason) == "" {
			return fmt.Errorf("verdict[%d] reason is required", i)
		}
		if len(verdict.EvidenceIDs) == 0 {
			return fmt.Errorf("verdict[%d] requires at least one evidence_id", i)
		}
		for j, eid := range verdict.EvidenceIDs {
			eid = strings.TrimSpace(eid)
			if eid == "" {
				return fmt.Errorf("verdict[%d] evidence_ids[%d] is empty", i, j)
			}
			if _, ok := knownEvidence[eid]; !ok {
				return fmt.Errorf("verdict[%d] references unknown evidence %q", i, eid)
			}
		}
	}

	for _, h := range hypotheses {
		if _, ok := seenHyp[h.ID]; !ok {
			return fmt.Errorf("missing verdict for hypothesis %q", h.ID)
		}
	}

	return nil
}

// 判断判定结果是否为领域允许的枚举值
func isValidVerdictResult(result string) bool {
	switch core.VerdictResult(strings.TrimSpace(result)) {
	case core.VerdictSupported, core.VerdictRefuted, core.VerdictInsufficient:
		return true
	default:
		return false
	}
}

// 将校验通过的模型输出回填为绑定当前运行的判决列表
func (v *LLMVerifier) fillVerdicts(runID string, out verifierLLMOutput) ([]core.Verdict, error) {
	now := v.factory.Now()
	verdicts := make([]core.Verdict, 0, len(out.Verdicts))
	for _, item := range out.Verdicts {
		id, err := v.factory.NewID("v")
		if err != nil {
			return nil, fmt.Errorf("create verdict ID: %w", err)
		}
		// 去重并保留顺序，避免模型重复引用同一证据
		evidenceIDs := uniqueNonEmpty(item.EvidenceIDs)
		verdicts = append(verdicts, core.Verdict{
			ID:           id,
			RunID:        runID,
			HypothesisID: strings.TrimSpace(item.HypothesisID),
			Result:       core.VerdictResult(strings.TrimSpace(item.Result)),
			Reason:       item.Reason,
			EvidenceIDs:  evidenceIDs,
			CreatedAt:    now,
		})
	}
	return verdicts, nil
}

// 去掉空白项并按首次出现去重，保留原有顺序
func uniqueNonEmpty(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
