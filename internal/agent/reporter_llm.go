// 真实报告器把运行、已完成判断与已登记证据交给大模型，回填系统编号后产出 Report
//
// 与 FakeReporter 共享同一 Report 边界：一次调用返回完整报告，不在角色内取证或改写判断结果
// 结论必须覆盖每条 Verdict；result 与 Verdict 一致；evidence_ids 为对应 Verdict 引用的子集
// Report 的系统编号与创建时间由本模块经 Factory 发放（对齐 LLMParser / LLMPlanner / LLMVerifier）
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

//go:embed prompts/reporter.md
var reporterPrompt string

// 报告业务级重试次数：JSON 合法但语义违规时重新请求
const maxReportAttempts = 3

// 用大模型整理诊断报告的报告器
//
// 持有不可变依赖（客户端、工厂、prompt），可被多次运行复用
// 不持有跨运行可变状态；每次 Report 独立发 GenerateJSON
type LLMReporter struct {
	// 大模型客户端，每次 Report 发一次 GenerateJSON
	client llm.Client
	// 领域编号与创建时间工厂，回填 Report 时使用
	factory *core.Factory
	// 嵌入的系统提示词全文，构造后只读
	prompt string
}

// 创建基于大模型的报告器，prompt 从包内嵌入文件加载
// 任一依赖缺失直接返回错误，避免运行期才暴露初始化问题
func NewLLMReporter(client llm.Client, factory *core.Factory) (*LLMReporter, error) {
	if client == nil {
		return nil, errors.New("LLM reporter requires an llm client")
	}
	if factory == nil {
		return nil, errors.New("LLM reporter requires a factory")
	}
	return &LLMReporter{client: client, factory: factory, prompt: reporterPrompt}, nil
}

// 请求模型整理报告，校验后回填系统编号与运行绑定
//
// 模型若返回结构合法但语义违规的输出（漏结论、改 result、非法证据等），在业务级重试内重新请求；
// 重试 maxReportAttempts 次仍不合规则返回 ErrLLMOutputInconsistent
func (r *LLMReporter) Report(
	ctx context.Context,
	run core.Run,
	verdicts []core.Verdict,
	evidence []core.Evidence,
) (core.Report, error) {
	if r == nil {
		return core.Report{}, errors.New("reporter is required")
	}
	if ctx == nil {
		return core.Report{}, errors.New("reporter requires a context")
	}
	if err := ctx.Err(); err != nil {
		return core.Report{}, fmt.Errorf("build report: %w", err)
	}
	if strings.TrimSpace(run.ID) == "" {
		return core.Report{}, errors.New("reporter requires a run ID")
	}
	if len(verdicts) == 0 {
		return core.Report{}, errors.New("reporter requires at least one verdict")
	}

	for i, verdict := range verdicts {
		if strings.TrimSpace(verdict.ID) == "" {
			return core.Report{}, fmt.Errorf("verdict[%d] id is required", i)
		}
		if strings.TrimSpace(verdict.RunID) != "" && verdict.RunID != run.ID {
			return core.Report{}, fmt.Errorf("verdict %q belongs to run %q, not %q", verdict.ID, verdict.RunID, run.ID)
		}
		if strings.TrimSpace(verdict.HypothesisID) == "" {
			return core.Report{}, fmt.Errorf("verdict[%d] hypothesis id is required", i)
		}
		// 与 Verifier / FakeReporter 一致：Verdict 必须带证据；空集无法构造合法结论子集
		if len(verdict.EvidenceIDs) == 0 {
			return core.Report{}, fmt.Errorf("verdict %q requires evidence", verdict.ID)
		}
	}
	for i, item := range evidence {
		if strings.TrimSpace(item.ID) == "" {
			return core.Report{}, fmt.Errorf("evidence[%d] id is required", i)
		}
		if strings.TrimSpace(item.RunID) != "" && item.RunID != run.ID {
			return core.Report{}, fmt.Errorf("evidence %q belongs to run %q, not %q", item.ID, item.RunID, run.ID)
		}
	}

	userPayload, err := buildReporterUserPayload(run, verdicts, evidence)
	if err != nil {
		return core.Report{}, fmt.Errorf("build report prompt: %w", err)
	}

	req := llm.Request{
		System: r.prompt,
		User:   userPayload,
	}

	var lastOut reporterLLMOutput
	var lastValidateErr error
	for attempt := 0; attempt < maxReportAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return core.Report{}, fmt.Errorf("build report: %w", err)
		}

		var out reporterLLMOutput
		if gErr := r.client.GenerateJSON(ctx, req, &out); gErr != nil {
			return core.Report{}, fmt.Errorf("report with LLM: %w", gErr)
		}

		if vErr := validateReporterOutput(out, verdicts, evidence); vErr != nil {
			lastOut = out
			lastValidateErr = vErr
			continue
		}

		return r.fillReport(run.ID, out)
	}

	return core.Report{}, fmt.Errorf("%w: last error: %v, last output: %+v",
		ErrLLMOutputInconsistent, lastValidateErr, lastOut)
}

// 序列化报告输入：原始问题、判断与已登记证据摘要
// 不塞 Evidence.Raw，避免 prompt 过大；判断所需字段以 Verdict 为准
func buildReporterUserPayload(
	run core.Run,
	verdicts []core.Verdict,
	evidence []core.Evidence,
) (string, error) {
	// 已完成判断的精简视图，约束结论 result 与证据子集
	type verdictView struct {
		// 判断系统编号
		ID string `json:"id"`
		// 对应猜想编号
		HypothesisID string `json:"hypothesisId"`
		// 裁决结果枚举字符串
		Result string `json:"result"`
		// 判断理由
		Reason string `json:"reason"`
		// 该判断已引用的证据编号集合
		EvidenceIDs []string `json:"evidenceIds"`
	}
	// 证据摘要视图，不含 Raw 以免撑爆 prompt
	type evidenceView struct {
		// 证据系统编号
		ID string `json:"id"`
		// 产出任务编号
		TaskID string `json:"taskId,omitempty"`
		// 工具名
		ToolName string `json:"toolName"`
		// 可展示命令视图
		CommandView string `json:"commandView,omitempty"`
		// 摘要
		Summary string `json:"summary"`
		// 失败错误，成功时可空
		Error string `json:"error,omitempty"`
	}
	// 报告角色完整 user payload
	type payload struct {
		// 用户原始问题
		Question string `json:"question"`
		// 全部判断，结论必须一一覆盖
		Verdicts []verdictView `json:"verdicts"`
		// 已登记证据摘要
		Evidence []evidenceView `json:"evidence"`
	}

	p := payload{
		Question: run.Question,
		Verdicts: make([]verdictView, 0, len(verdicts)),
		Evidence: make([]evidenceView, 0, len(evidence)),
	}
	for _, v := range verdicts {
		p.Verdicts = append(p.Verdicts, verdictView{
			ID:           v.ID,
			HypothesisID: v.HypothesisID,
			Result:       string(v.Result),
			Reason:       v.Reason,
			// 用非 nil 空切片起步，避免 EvidenceIDs 为 nil 时 JSON 编成 null
			EvidenceIDs: append([]string{}, v.EvidenceIDs...),
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
		})
	}

	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// 模型输出的中间结构，只含报告内容，不含系统编号或时间
type reporterLLMOutput struct {
	// 报告标题
	Title string `json:"title"`
	// 整体摘要
	Summary string `json:"summary"`
	// 按猜想列出的结论条目
	Conclusions []reporterConclusion `json:"conclusions"`
	// 后续建议列表，可为空
	Suggestions []string `json:"suggestions"`
}

// 模型侧一条结论，须对齐对应 Verdict 的 result 与证据子集
type reporterConclusion struct {
	// 对应猜想系统编号
	HypothesisID string `json:"hypothesis_id"`
	// 须与对应 Verdict.Result 一致
	Result string `json:"result"`
	// 面向读者的结论理由
	Reason string `json:"reason"`
	// 引用的证据编号，须为对应 Verdict.EvidenceIDs 的非空子集
	EvidenceIDs []string `json:"evidence_ids"`
}

// 校验模型输出满足报告契约与信任边界
//
// 校验项：
//   - title / summary 非空
//   - 每条输入 Verdict 恰好一条 Conclusion，hypothesis 集合一致
//   - result 与对应 Verdict 一致
//   - reason 非空
//   - evidence_ids 非空，且属于对应 Verdict.EvidenceIDs（并存在于输入 Evidence）
//   - suggestions 各项非空（允许空列表）
func validateReporterOutput(
	out reporterLLMOutput,
	verdicts []core.Verdict,
	evidence []core.Evidence,
) error {
	if strings.TrimSpace(out.Title) == "" {
		return errors.New("title is required")
	}
	if strings.TrimSpace(out.Summary) == "" {
		return errors.New("summary is required")
	}
	if len(out.Conclusions) == 0 {
		return errors.New("at least one conclusion is required")
	}

	verdictByHyp := make(map[string]core.Verdict, len(verdicts))
	for _, v := range verdicts {
		verdictByHyp[v.HypothesisID] = v
	}

	knownEvidence := make(map[string]struct{}, len(evidence))
	for _, e := range evidence {
		if strings.TrimSpace(e.ID) != "" {
			knownEvidence[e.ID] = struct{}{}
		}
	}

	seenHyp := make(map[string]struct{}, len(out.Conclusions))
	for i, c := range out.Conclusions {
		hypID := strings.TrimSpace(c.HypothesisID)
		if hypID == "" {
			return fmt.Errorf("conclusion[%d] hypothesis_id is required", i)
		}
		verdict, ok := verdictByHyp[hypID]
		if !ok {
			return fmt.Errorf("conclusion[%d] references unknown hypothesis %q", i, hypID)
		}
		if _, dup := seenHyp[hypID]; dup {
			return fmt.Errorf("conclusion[%d] duplicates hypothesis %q", i, hypID)
		}
		seenHyp[hypID] = struct{}{}

		result := strings.TrimSpace(c.Result)
		if !isValidVerdictResult(result) {
			return fmt.Errorf("conclusion[%d] result %q is invalid", i, c.Result)
		}
		if core.VerdictResult(result) != verdict.Result {
			return fmt.Errorf("conclusion[%d] result %q does not match verdict %q", i, result, verdict.Result)
		}
		if strings.TrimSpace(c.Reason) == "" {
			return fmt.Errorf("conclusion[%d] reason is required", i)
		}
		// Verdict 无证据时 fail-fast（正常路径由 Report 入口已拦）；不放宽为空结论
		if len(verdict.EvidenceIDs) == 0 {
			return fmt.Errorf("verdict for hypothesis %q has no evidence", hypID)
		}
		if len(c.EvidenceIDs) == 0 {
			return fmt.Errorf("conclusion[%d] requires at least one evidence_id", i)
		}

		allowed := make(map[string]struct{}, len(verdict.EvidenceIDs))
		for _, eid := range verdict.EvidenceIDs {
			eid = strings.TrimSpace(eid)
			if eid != "" {
				allowed[eid] = struct{}{}
			}
		}
		for j, eid := range c.EvidenceIDs {
			eid = strings.TrimSpace(eid)
			if eid == "" {
				return fmt.Errorf("conclusion[%d] evidence_ids[%d] is empty", i, j)
			}
			if _, ok := knownEvidence[eid]; !ok {
				return fmt.Errorf("conclusion[%d] references unknown evidence %q", i, eid)
			}
			if _, ok := allowed[eid]; !ok {
				return fmt.Errorf("conclusion[%d] evidence %q is not in verdict evidence set", i, eid)
			}
		}
	}

	for _, v := range verdicts {
		if _, ok := seenHyp[v.HypothesisID]; !ok {
			return fmt.Errorf("missing conclusion for hypothesis %q", v.HypothesisID)
		}
	}

	for i, s := range out.Suggestions {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("suggestions[%d] is empty", i)
		}
	}

	return nil
}

// 将校验通过的模型输出回填为绑定当前运行的 Report
func (r *LLMReporter) fillReport(runID string, out reporterLLMOutput) (core.Report, error) {
	id, err := r.factory.NewID("rep")
	if err != nil {
		return core.Report{}, fmt.Errorf("create report ID: %w", err)
	}

	conclusions := make([]core.Conclusion, 0, len(out.Conclusions))
	for _, c := range out.Conclusions {
		conclusions = append(conclusions, core.Conclusion{
			HypothesisID: strings.TrimSpace(c.HypothesisID),
			Result:       core.VerdictResult(strings.TrimSpace(c.Result)),
			Reason:       c.Reason,
			EvidenceIDs:  uniqueNonEmpty(c.EvidenceIDs),
		})
	}

	suggestions := make([]string, 0, len(out.Suggestions))
	for _, s := range out.Suggestions {
		suggestions = append(suggestions, strings.TrimSpace(s))
	}

	return core.Report{
		ID:          id,
		RunID:       runID,
		Title:       strings.TrimSpace(out.Title),
		Summary:     strings.TrimSpace(out.Summary),
		Conclusions: conclusions,
		Suggestions: suggestions,
		CreatedAt:   r.factory.Now(),
	}, nil
}
