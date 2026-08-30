// 先前诊断注入形状：R 层索引卡与 prior_run_details 载荷共用的事业单元视图类型
//
// 索引卡组装见 memory_cards.go（卡片不带 raw，深细节归回灌与 evidence.read）；
// 本文件只保留类型定义
package agent

import "encoding/json"

// 注入先前运行详情的结论子集（来自报告结论）
type towerPriorConclusion struct {
	// 判定结果（成立、否定或证据不足）
	Result string `json:"result,omitempty"`
	// 面向用户的理由
	Reason string `json:"reason,omitempty"`
	// 支撑证据编号
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

// 注入模型的证据视图；索引卡形态下不带原始输出（Raw 为空）
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
	// 原始输出；索引卡不带（保留字段兼容深材料形态的潜在回填）
	Raw json.RawMessage `json:"raw,omitempty"`
	// 注入副本对原始输出做了预算截断时为真
	RawTruncated bool `json:"rawTruncated,omitempty"`
}

// 本会话一次正式诊断的注入视图（索引卡形态；权威源为诊断账本）
type towerPriorRunDetail struct {
	// 诊断运行编号
	RunID string `json:"run_id"`
	// 建运行时的问题
	Question string `json:"question,omitempty"`
	// 报告标题
	Title string `json:"title,omitempty"`
	// 报告摘要
	Summary string `json:"summary,omitempty"`
	// 结论列表
	Conclusions []towerPriorConclusion `json:"conclusions,omitempty"`
	// 处理建议
	Suggestions []string `json:"suggestions,omitempty"`
	// 证据列表（索引卡形态：id/工具/摘要/命令视图）
	Evidence []towerPriorEvidenceView `json:"evidence"`
}
