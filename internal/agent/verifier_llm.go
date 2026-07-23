// 真实验证器把猜想、任务与已登记证据交给大模型，回填系统编号后产出 Verdict
//
// 与 FakeVerifier 共享同一 Verify 边界：一次调用返回完整判断列表，不在角色内多轮调工具
// 判断只能引用输入中已存在的 Evidence 编号；Hypothesis / Task 仅作上下文
// Verdict 的系统编号与创建时间由本模块经 Factory 发放（对齐 LLMParser / LLMPlanner）
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

// 验证业务级重试次数：JSON 合法但语义违规时重新请求
const maxVerifyAttempts = 3

// 用大模型基于已登记证据判断猜想的验证器
//
// 持有不可变依赖（客户端、工厂、prompt），可被多次运行复用
// 不持有跨运行可变状态；每次 Verify 独立发 GenerateJSON
type LLMVerifier struct {
	client  llm.Client
	factory *core.Factory
	prompt  string
}

// 创建基于大模型的验证器，prompt 从包内嵌入文件加载
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
// 重试 maxVerifyAttempts 次仍不合规则返回 ErrLLMOutputInconsistent
func (v *LLMVerifier) Verify(
	ctx context.Context,
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

	runID := strings.TrimSpace(hypotheses[0].RunID)
	if runID == "" {
		return nil, errors.New("verifier requires a run ID on hypotheses")
	}
	for i, h := range hypotheses {
		if strings.TrimSpace(h.ID) == "" {
			return nil, fmt.Errorf("hypothesis[%d] id is required", i)
		}
		if strings.TrimSpace(h.RunID) != runID {
			return nil, fmt.Errorf("hypothesis[%d] run ID %q does not match %q", i, h.RunID, runID)
		}
	}

	userPayload, err := buildVerifierUserPayload(hypotheses, tasks, evidence)
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

// 序列化验证输入：猜想、任务与已登记证据
// 只暴露判断所需字段，避免把无关运行元数据塞进 prompt
func buildVerifierUserPayload(
	hypotheses []core.Hypothesis,
	tasks []core.Task,
	evidence []core.Evidence,
) (string, error) {
	type hypView struct {
		ID              string   `json:"id"`
		Statement       string   `json:"statement"`
		Reason          string   `json:"reason,omitempty"`
		ExpectedSignals []string `json:"expectedSignals,omitempty"`
	}
	type taskView struct {
		ID       string   `json:"id"`
		Refs     []string `json:"refs,omitempty"`
		ToolName string   `json:"toolName"`
		Purpose  string   `json:"purpose,omitempty"`
	}
	type evidenceView struct {
		ID          string          `json:"id"`
		TaskID      string          `json:"taskId"`
		ToolName    string          `json:"toolName"`
		CommandView string          `json:"commandView,omitempty"`
		Summary     string          `json:"summary"`
		Error       string          `json:"error,omitempty"`
		Raw         json.RawMessage `json:"raw,omitempty"`
	}
	type payload struct {
		Hypotheses []hypView      `json:"hypotheses"`
		Tasks      []taskView     `json:"tasks"`
		Evidence   []evidenceView `json:"evidence"`
	}

	p := payload{
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
	Verdicts []verifierVerdictOut `json:"verdicts"`
}

type verifierVerdictOut struct {
	HypothesisID string   `json:"hypothesis_id"`
	Result       string   `json:"result"`
	Reason       string   `json:"reason"`
	EvidenceIDs  []string `json:"evidence_ids"`
}

// 校验模型输出满足编排与报告的最小契约
//
// 校验项：
//   - 每条输入猜想恰好一条判断
//   - result 枚举合法
//   - reason 非空
//   - evidence_ids 非空且全部属于输入 evidence
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

// 判断 result 是否为领域允许的枚举值
func isValidVerdictResult(result string) bool {
	switch core.VerdictResult(strings.TrimSpace(result)) {
	case core.VerdictSupported, core.VerdictRefuted, core.VerdictInsufficient:
		return true
	default:
		return false
	}
}

// 将校验通过的模型输出回填为绑定当前运行的 Verdict 列表
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
