// R 层索引卡：正式诊断的信任分层记忆常驻层（Algorithm 1 第 1–2 行）
//
// 证据与结论（E/V 类单元）从诊断账本压成索引卡：地址（编号）+ 短摘要，
// 锁定常驻、不参与 W/C 替换（式 5）；卡片不带 raw——深细节归回灌与 evidence.read。
// 类型来自领域模型内生（RunLedger 结构化实体）；会话叙述不判型（全走 N 线压缩）
package agent

import (
	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
)

// 卡片单字段词元钳制上限（式 2 的 c_max，百 token 级）
// 截断走 truncateContentPreview，C1 保证编号不随截断丢失
const cardMaxTokens = 200

// κ₂ 二级压缩接口：卡片墙超预算时降卡片密度（递归同构压缩）
// 本版预留不实现（默认实现原样返回）；卡片墙增长到 100+ 诊断才触发，实验用不上
type cardWallCompactor func(cards []towerPriorRunDetail, budgetTokens int) []towerPriorRunDetail

// 默认 κ₂ 占位：原样返回；实现随后续版本落（接口面今天定死）
var noopCardWallCompactor cardWallCompactor = func(cards []towerPriorRunDetail, _ int) []towerPriorRunDetail {
	return cards
}

// 组装 R 层索引卡：每条正式诊断产出 run 卡与证据卡
// 复用 prior_run_details 注入形状但 raw 一律不带；文本字段钳到 c_max（C1 保地址）
// 卡片无条数上限（按需取满；超墙走 κ₂ 接口，本版不触发）
func buildMemoryCards(records []session.DiagnosticRecord) []towerPriorRunDetail {
	out := make([]towerPriorRunDetail, 0, len(records))
	for _, rec := range records {
		detail := towerPriorRunDetail{
			RunID:       rec.RunID,
			Question:    clampCardText(rec.Question),
			Title:       clampCardText(rec.Report.Title),
			Summary:     clampCardText(rec.Report.Summary),
			Conclusions: mapConclusionCards(rec.Report.Conclusions),
			Suggestions: clampCardLines(rec.Report.Suggestions),
			Evidence:    mapEvidenceCards(rec.Evidence),
		}
		out = append(out, detail)
	}
	return out
}

// 结论行卡面：理由钳制；结果与证据编号（地址）原样保留不钳
func mapConclusionCards(in []core.Conclusion) []towerPriorConclusion {
	if len(in) == 0 {
		return nil
	}
	out := make([]towerPriorConclusion, 0, len(in))
	for _, c := range in {
		out = append(out, towerPriorConclusion{
			Result:      string(c.Result),
			Reason:      clampCardText(c.Reason),
			EvidenceIDs: append([]string(nil), c.EvidenceIDs...),
		})
	}
	return out
}

// 证据卡面：编号 / 工具名 / 摘要 / 命令视图（均钳制），raw 不带
func mapEvidenceCards(in []core.Evidence) []towerPriorEvidenceView {
	if len(in) == 0 {
		return []towerPriorEvidenceView{}
	}
	out := make([]towerPriorEvidenceView, 0, len(in))
	for _, e := range in {
		out = append(out, towerPriorEvidenceView{
			ID:          e.ID,
			ToolName:    e.ToolName,
			Summary:     clampCardText(e.Summary),
			CommandView: clampCardText(e.CommandView),
			Error:       clampCardText(e.Error),
		})
	}
	return out
}

// 建议行逐条钳制
func clampCardLines(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, clampCardText(s))
	}
	return out
}

// 卡片文本钳制：预算内原样；超预算截断预览（C1 保地址，截断标记说明全文仍在账本）
func clampCardText(s string) string {
	if estimateTokens(s) <= cardMaxTokens {
		return s
	}
	return truncateContentPreview(s, cardMaxTokens)
}
