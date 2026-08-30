// 评测记录：一次诊断 run 的机器可判分产物（--eval-json 落盘格式，schema v1）
//
// 字段口径来自实验记录协议（场景 × 方法 × 重复的实验单元产物）：
// 结论、工具调用链、按角色 token 用量、轮次、耗时；挂起与失败也落盘（completed=false），
// 失败 run 全量报告是统计纪律的一部分
// 本包只做提取与判分，不调集群、不调模型

package eval

import (
	"time"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/llm"
)

// SchemaVersion 当前记录格式版本；字段不兼容变更时递增并由读取方按版本分支
const SchemaVersion = 1

// TokenUsage 一个调用方标签的 token 用量（记录内只保留 in/out，调用次数不进记录）
type TokenUsage struct {
	// 提示词侧 token 数
	In int64 `json:"in"`
	// 补全侧 token 数
	Out int64 `json:"out"`
}

// RootCauseEntry 报告中的一条结论；判分①的对象
type RootCauseEntry struct {
	// 判决结果：supported / refuted / insufficient
	Result string `json:"result"`
	// 结论理由文本；判分①的机械包含匹配在此文本上进行
	Reason string `json:"reason"`
	// 结论引用的证据编号
	EvidenceIDs []string `json:"evidence_ids"`
}

// ToolCallEntry 一次工具调用的可追溯记录；从证据链提取
type ToolCallEntry struct {
	// 证据编号
	EvidenceID string `json:"evidence_id"`
	// 工具名（如 k8s）
	Tool string `json:"tool"`
	// 命令视图（人读的调用参数回显）
	Command string `json:"command"`
	// 调用时刻（RFC3339）
	CreatedAt string `json:"created_at"`
	// 工具产出的投影摘要全文（含大表投影子集内容）
	Summary string `json:"summary"`
}

// RunRecord 一次 run 的评测记录；--eval-json 落盘的顶层结构
type RunRecord struct {
	// 记录格式版本
	SchemaVersion int `json:"schema_version"`
	// 运行编号
	RunID string `json:"run_id"`
	// 用户问题原文
	Question string `json:"question"`
	// 模型名（固定模型版本的统计纪律）
	Model string `json:"model"`
	// 投影方法名（tools.projection.method；机械判分的分组变量）
	ProjectionMethod string `json:"projection_method"`
	// 取证决策方法名（agent.acquire.method 解析后的规范名；实验矩阵分组变量）
	AcquireMethod string `json:"acquire_method"`
	// 名义轮数预算 K（agent.acquire.max_rounds；0 = 默认 3）——曲线横轴用实测
	// rounds，名义 K 仅作注入参数与分桶校验
	AcquireMaxRounds int `json:"acquire_max_rounds"`
	// b2-random 实验臂种子（可复现；其余方法为 0）
	AcquireSeed int64 `json:"acquire_seed"`
	// 是否完成（false = 挂起或失败，仍全量落盘）
	Completed bool `json:"completed"`
	// 失败或挂起原因；成功为空
	Error string `json:"error,omitempty"`
	// 报告结论列表（判分①对象）
	RootCauses []RootCauseEntry `json:"verdict_root_cause"`
	// 工具调用链（按时间序）
	ToolCalls []ToolCallEntry `json:"tool_calls"`
	// 结论引用的证据编号并集（判分②对象）
	EvidenceCited []string `json:"evidence_cited"`
	// 按调用方标签聚合的 token 用量
	Tokens map[string]TokenUsage `json:"tokens"`
	// 调查轮数（已开始计；实测口径，预算-准确率曲线横轴用）
	Rounds int `json:"rounds"`
	// 取证决策出口（决策循环路径）：supported / insufficient；b1-serial 为空
	AcquireExit string `json:"acquire_exit,omitempty"`
	// insufficient 出口缺口说明（平台 / 预算尽）；#18 明确失败可观测
	AcquireGap string `json:"acquire_gap,omitempty"`
	// 端到端耗时（毫秒）
	WallTimeMS int64 `json:"wall_time_ms"`
}

// AcquireRecordInfo 取证决策的分组与出口观测量（评测记录侧）；方法名用解析后的
// 规范名（ours/b1-serial/b2-random/b4-cheapest），避免空串与 "ours" 混写致分组歧义
type AcquireRecordInfo struct {
	// 规范化方法名（agent.ParseAcquireMethod 后的 String()）
	Method string
	// 名义轮数预算 K（config 原值；0 = 默认 3）
	MaxRounds int
	// b2-random 种子；其余方法为 0
	Seed int64
	// 决策循环出口（LastRunStats.AcquireExit 透传）
	Exit string
	// insufficient 缺口说明（LastRunStats.AcquireGap 透传）
	Gap string
}

// BuildRunRecord 从一次执行的产物组装评测记录
// report / evidence 来自 Outcome；tokens 来自 llm.UsageTracker 快照；rounds 与
// acq 来自编排只读统计与配置；completed=false 时 report 可为 nil（挂起 / 失败路径），
// 错误信息照实入记录
func BuildRunRecord(
	runID, question, model, projectionMethod string,
	acq AcquireRecordInfo,
	completed bool, runErr string,
	report *core.Report,
	evidence []core.Evidence,
	tokens map[string]llm.UsageTotals,
	rounds int,
	wall time.Duration,
) RunRecord {
	rec := RunRecord{
		SchemaVersion:    SchemaVersion,
		RunID:            runID,
		Question:         question,
		Model:            model,
		ProjectionMethod: projectionMethod,
		AcquireMethod:    acq.Method,
		AcquireMaxRounds: acq.MaxRounds,
		AcquireSeed:      acq.Seed,
		Completed:        completed,
		Error:            runErr,
		Rounds:           rounds,
		AcquireExit:      acq.Exit,
		AcquireGap:       acq.Gap,
		WallTimeMS:       wall.Milliseconds(),
		RootCauses:       []RootCauseEntry{},
		ToolCalls:        []ToolCallEntry{},
		EvidenceCited:    []string{},
	}

	if report != nil {
		for _, c := range report.Conclusions {
			// 空引用归一为空切片：nil 序列化成 null 会破坏 schema「数组」契约
			ids := append([]string{}, c.EvidenceIDs...)
			rec.RootCauses = append(rec.RootCauses, RootCauseEntry{
				Result:      string(c.Result),
				Reason:      c.Reason,
				EvidenceIDs: ids,
			})
			for _, id := range c.EvidenceIDs {
				if !containsString(rec.EvidenceCited, id) {
					rec.EvidenceCited = append(rec.EvidenceCited, id)
				}
			}
		}
	}

	for _, ev := range evidence {
		rec.ToolCalls = append(rec.ToolCalls, ToolCallEntry{
			EvidenceID: ev.ID,
			Tool:       ev.ToolName,
			Command:    ev.CommandView,
			CreatedAt:  ev.CreatedAt.Format(time.RFC3339),
			Summary:    ev.Summary,
		})
	}

	rec.Tokens = make(map[string]TokenUsage, len(tokens))
	for label, t := range tokens {
		rec.Tokens[label] = TokenUsage{In: t.PromptTokens, Out: t.CompletionTokens}
	}
	return rec
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
