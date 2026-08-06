package agent

import (
	"encoding/json"

	"aruing/internal/core"
	"aruing/internal/session"
)

const (
	// 本会话 prior 证据 raw 注入合计预算（与基线观察预算同量级）
	// 多 run、多条证据共享；优先保较新 run / 较新证据；#18
	defaultPriorEvidenceBudgetTokens = 8_000
)

// 注入 prior_run_details 的结论子集（来自 Report.Conclusions）
type towerPriorConclusion struct {
	// 判定结果（supported / refuted / insufficient）
	Result string `json:"result,omitempty"`
	// 面向用户的理由
	Reason string `json:"reason,omitempty"`
	// 支撑证据编号
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

// 注入模型的证据视图；raw 可能经共享预算截断
type towerPriorEvidenceView struct {
	// 证据编号
	ID string `json:"id"`
	// 工具名
	ToolName string `json:"toolName,omitempty"`
	// 证据摘要
	Summary string `json:"summary,omitempty"`
	// 可展示命令视图
	CommandView string `json:"commandView,omitempty"`
	// 工具失败时非空
	Error string `json:"error,omitempty"`
	// 原始 JSON（可能截断/占位）
	Raw json.RawMessage `json:"raw,omitempty"`
	// 注入副本对 raw 做了预算截断时为 true
	RawTruncated bool `json:"rawTruncated,omitempty"`
}

// 本会话一次正式诊断的深材料，供解释路径引用（权威源 RunLedger）
type towerPriorRunDetail struct {
	// 诊断 Run 编号
	RunID string `json:"run_id"`
	// 建 Run 时的问题
	Question string `json:"question,omitempty"`
	// 报告标题
	Title string `json:"title,omitempty"`
	// 报告摘要
	Summary string `json:"summary,omitempty"`
	// 结论列表
	Conclusions []towerPriorConclusion `json:"conclusions,omitempty"`
	// 处理建议
	Suggestions []string `json:"suggestions,omitempty"`
	// 证据列表（含 raw 注入副本）
	Evidence []towerPriorEvidenceView `json:"evidence"`
}

// 将账本记录转为注入用深材料；raw 共享预算、优先保新
// records 为空时返回空切片（非 nil）
func buildPriorRunDetails(records []session.DiagnosticRecord, budgetTokens int) []towerPriorRunDetail {
	if budgetTokens <= 0 {
		budgetTokens = defaultPriorEvidenceBudgetTokens
	}
	if len(records) == 0 {
		return []towerPriorRunDetail{}
	}

	out := make([]towerPriorRunDetail, 0, len(records))
	for _, rec := range records {
		detail := towerPriorRunDetail{
			RunID:       rec.RunID,
			Question:    rec.Question,
			Title:       rec.Report.Title,
			Summary:     rec.Report.Summary,
			Conclusions: mapPriorConclusions(rec.Report.Conclusions),
			Suggestions: append([]string(nil), rec.Report.Suggestions...),
			Evidence:    mapPriorEvidence(rec.Evidence),
		}
		out = append(out, detail)
	}
	applyPriorEvidenceRawBudget(out, budgetTokens)
	return out
}

func mapPriorConclusions(in []core.Conclusion) []towerPriorConclusion {
	if len(in) == 0 {
		return nil
	}
	out := make([]towerPriorConclusion, 0, len(in))
	for _, c := range in {
		out = append(out, towerPriorConclusion{
			Result:      string(c.Result),
			Reason:      c.Reason,
			EvidenceIDs: append([]string(nil), c.EvidenceIDs...),
		})
	}
	return out
}

func mapPriorEvidence(in []core.Evidence) []towerPriorEvidenceView {
	if len(in) == 0 {
		return []towerPriorEvidenceView{}
	}
	out := make([]towerPriorEvidenceView, len(in))
	for i, e := range in {
		out[i] = towerPriorEvidenceView{
			ID:          e.ID,
			ToolName:    e.ToolName,
			Summary:     e.Summary,
			CommandView: e.CommandView,
			Error:       e.Error,
		}
		if len(e.Raw) > 0 {
			out[i].Raw = append(json.RawMessage(nil), e.Raw...)
		}
	}
	return out
}

// 全部 prior 证据 raw 共享预算；从最新 run、run 内最新证据向前分配
// 不修改账本权威数据（仅作用于已拷贝的注入视图）
func applyPriorEvidenceRawBudget(details []towerPriorRunDetail, budgetTokens int) {
	if len(details) == 0 {
		return
	}
	remaining := budgetTokens
	for i := len(details) - 1; i >= 0; i-- {
		evs := details[i].Evidence
		for j := len(evs) - 1; j >= 0; j-- {
			if len(evs[j].Raw) == 0 {
				continue
			}
			cost := estimateTokens(string(evs[j].Raw))
			if cost <= remaining {
				remaining -= cost
				continue
			}
			if remaining > 0 {
				evs[j].Raw = truncateObservationRaw(evs[j].Raw, remaining)
				evs[j].RawTruncated = true
				remaining = 0
				continue
			}
			evs[j].Raw = omitObservationRawForBudget()
			evs[j].RawTruncated = true
		}
	}
}
