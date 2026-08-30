// 探针实验装置（0.1.3 步骤 4）：长会话驱动器与会话级评测记录
//
// 单进程驱动一条脚本化长会话（MemoryStore / RunLedger 进程内，持久化未到前
// 长会话只能单进程，bash 驱动 CLI 够不着会话内状态——本 runner 是唯一形态）：
// 逐轮调会话服务、诊断轮后读编排只读统计、探针轮后读记忆观测量，
// 尾部探针发完后组装会话级记录落盘
// 依赖全部函数注入：CLI 装真栈（Session + Tower + 编排器），单测装假栈；
// 本包不 import agent，观测结构在此侧自持口径
// 轮次失败不静默吞：记入记录并中止后续轮（失败 run 全量报告是统计纪律）

package eval

import (
	"context"
	"fmt"
	"time"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/llm"
	"github.com/Aruing/Aruing/internal/session"
)

// 长会话驱动依赖（函数注入；CLI 装真栈、单测装假栈）
type ProbeDeps struct {
	// 单轮会话入口（session.Service.Turn）
	Turn func(ctx context.Context, sessionID, userText string) (session.TurnResult, error)
	// 最近一轮记忆观测量（TowerResponder.LastMemoryStats 的转换；探针轮后读）
	MemStats func() MemoryStats
	// 最近一次诊断的轮数与出口（Orchestrator.LastRunStats 的转换；诊断轮后读）
	DiagnoseStats func() DiagnoseStats
	// 诊断账本读回（内嵌 RunRecord 组装与 from_ledger 展开的权威源）
	Ledger session.RunLedger
	// token 用量快照（可空：无记账适配器时 token 段为空）
	Tokens func() map[string]llm.UsageTotals
}

// 单轮记忆观测量（记录侧口径；agent 侧统计的工程投影）
type MemoryStats struct {
	// 记忆方法规范名（ours / d1-last-n / d2-flat-summary）
	Method string `json:"method"`
	// 定位命中层：lambda1 / lambda2 / none（D1/D2 臂恒 none）
	LocateLayer string `json:"locate_layer"`
	// λ₂ 是否实际调用
	Lambda2Called bool `json:"lambda2_called"`
	// 回灌注入的对话消息条数
	RehydratedMsgs int `json:"rehydrated_msgs"`
	// 回灌注入的合成证据条数
	RehydratedEvidence int `json:"rehydrated_evidence"`
	// 本轮注入视图时的历史消息条数
	HistTurns int `json:"hist_turns"`
}

// 单次诊断的分组观测量（RunRecord 内嵌字段的来源）
type DiagnoseStats struct {
	// 调查轮数（实测口径）
	Rounds int
	// 取证决策出口（supported / insufficient；b1-serial 为空）
	Exit string
	// insufficient 出口缺口说明
	Gap string
}

// 会话级评测记录：一条长会话 × 尾部探针的机器可判分产物
// 探针轮是基线对话轮、没有 Run，与 RunRecord（单次诊断）语义不同、并列存在
type ProbeSessionRecord struct {
	// 记录格式版本（与 RunRecord 同族向后兼容口径）
	SchemaVersion int `json:"schema_version"`
	// 会话编号
	SessionID string `json:"session_id"`
	// 探针规格名（一般同场景名）
	Scenario string `json:"scenario"`
	// 模型名（固定模型版本的统计纪律）
	Model string `json:"model"`
	// 记忆方法规范名（实验分组变量）
	MemoryMethod string `json:"memory_method"`
	// 投影方法名（透传分组）
	ProjectionMethod string `json:"projection_method"`
	// 取证决策方法名（透传分组）
	AcquireMethod string `json:"acquire_method"`
	// 脚本种子（可复现）
	ScriptSeed int64 `json:"script_seed"`
	// 主体轮数 N（探针轮不计入）
	Rounds int `json:"rounds"`
	// 实际执行的总轮数（含探针轮；轮次失败中止时小于脚本长度）
	TurnsExecuted int `json:"turns_executed"`
	// 全部轮次是否执行完（false = 中途失败，失败轮记入 TurnErrors）
	Completed bool `json:"completed"`
	// 轮次失败清单（含探针轮；失败不剔除、全量报告）
	TurnErrors []TurnError `json:"turn_errors,omitempty"`
	// 逐诊断信息（含内嵌完整 RunRecord，供完成率列与 judge 复判）
	Diagnoses []DiagnoseTurnInfo `json:"diagnoses"`
	// 逐探针结果（答案 + 展开后期望 + 状态；命中判定归 judge）
	Probes []ProbeEntry `json:"probes"`
	// 全会话按角色聚合的 token 用量（累计口径；内嵌 RunRecord 不再单拆）
	Tokens map[string]llm.UsageTotals `json:"tokens"`
	// 端到端耗时（毫秒）
	WallTimeMS int64 `json:"wall_time_ms"`
}

// 单次轮次失败记录
type TurnError struct {
	// 失败轮在脚本中的下标（零起）
	TurnIndex int `json:"turn_index"`
	// 轮次类型
	Kind string `json:"kind"`
	// 错误信息
	Error string `json:"error"`
}

// 单次穿插诊断的记录：轮位 + 状态 + 内嵌 RunRecord
type DiagnoseTurnInfo struct {
	// 诊断轮在脚本中的下标（零起）
	TurnIndex int `json:"turn_index"`
	// 诊断运行编号
	RunID string `json:"run_id"`
	// completed = 报告落账本；suspended = 澄清挂起（后续轮自然作答复）
	Status string `json:"status"`
	// 内嵌完整 RunRecord（token 段为空：全会话累计在会话级记录）
	Record RunRecord `json:"record"`
}

// 单条探针结果：判分①层的全部机械输入
type ProbeEntry struct {
	// 探针编号
	ProbeID string `json:"probe_id"`
	// 类别：evidence / synthesis / chain
	Class string `json:"class"`
	// 探针问题原文
	Question string `json:"question"`
	// 助手答案原文（判分①的包含判定对象）
	Answer string `json:"answer"`
	// 展开后的期望组（每组至少一串命中才算 hit）
	Expected [][]string `json:"expected"`
	// 期望展开状态：expanded / no_diagnosis / no_facts（非 expanded 不进成功率分母）
	ExpectStatus string `json:"expect_status"`
	// 探针轮在脚本中的下标（零起）
	TurnIndex int `json:"turn_index"`
	// 探针轮记忆观测量（命中层 / 回灌条目数）
	Memory MemoryStats `json:"memory"`
}

// 一次长会话运行的注入参数（CLI 从 config 与参数解析）
type ProbeRunOptions struct {
	// 会话编号（runner 不建会话，由调用方建好再传入）
	SessionID string
	// 模型名（记录留档）
	Model string
	// 记忆方法规范名（实验分组）
	MemoryMethod string
	// 投影方法名（透传）
	ProjectionMethod string
	// 取证决策分组信息（方法 / 名义 K / 种子）
	Acquire AcquireRecordInfo
	// 场景真值资源名（kth_run_pods 的过滤键）
	ResourceName string
}

// RunProbeSession 按脚本驱动一条长会话并组装会话级记录
// 主体轮逐轮执行：诊断轮（TurnResult.RunID 非空）后读账本与编排统计内嵌 RunRecord；
// 探针轮后读记忆观测量并展开期望组
// 任一轮失败：记入 TurnErrors、停止后续轮、返回记录与错误（调用方照常落盘）
func RunProbeSession(
	ctx context.Context,
	deps ProbeDeps,
	opts ProbeRunOptions,
	spec ProbeSpec,
	script ProbeScript,
) (ProbeSessionRecord, error) {
	rec := ProbeSessionRecord{
		SchemaVersion:    SchemaVersion,
		SessionID:        opts.SessionID,
		Scenario:         spec.Name,
		Model:            opts.Model,
		MemoryMethod:     opts.MemoryMethod,
		ProjectionMethod: opts.ProjectionMethod,
		AcquireMethod:    opts.Acquire.Method,
		ScriptSeed:       script.Seed,
		Rounds:           script.Rounds,
		TurnErrors:       []TurnError{},
		Diagnoses:        []DiagnoseTurnInfo{},
		Probes:           []ProbeEntry{},
	}
	start := time.Now()
	fail := func(i int, t ProbeTurn, err error) (ProbeSessionRecord, error) {
		rec.TurnErrors = append(rec.TurnErrors, TurnError{TurnIndex: i, Kind: t.Kind, Error: err.Error()})
		rec.Completed = false
		rec.TurnsExecuted = i
		rec.WallTimeMS = time.Since(start).Milliseconds()
		if deps.Tokens != nil {
			rec.Tokens = deps.Tokens()
		}
		return rec, fmt.Errorf("probe session turn %d (%s): %w", i, t.Kind, err)
	}

	for i, t := range script.Turns {
		res, err := deps.Turn(ctx, opts.SessionID, t.Text)
		if err != nil {
			return fail(i, t, err)
		}
		rec.TurnsExecuted = i + 1

		switch t.Kind {
		case ProbeTurnDiagnose, ProbeTurnQA:
			// 诊断轮也可能是基线轮中途触顶自动升格；统一以 TurnResult.RunID 判定
			if res.RunID == "" {
				continue
			}
			info, derr := buildDiagnoseInfo(deps, opts, t.Text, res, i)
			if derr != nil {
				return fail(i, t, derr)
			}
			rec.Diagnoses = append(rec.Diagnoses, info)

		case ProbeTurnProbe:
			entry := ProbeEntry{
				ProbeID:   t.ProbeID,
				Question:  t.Text,
				Answer:    res.AssistantMessage.Content,
				TurnIndex: i,
			}
			for _, p := range spec.Probes {
				if p.ID == t.ProbeID {
					entry.Class = p.Class
					break
				}
			}
			// 记忆观测量在探针轮后立即读（单线程轮次约定下反映本轮定位）
			if deps.MemStats != nil {
				entry.Memory = deps.MemStats()
			}
			records, lerr := deps.Ledger.ListBySession(ctx, opts.SessionID)
			if lerr != nil {
				return fail(i, t, fmt.Errorf("list ledger: %w", lerr))
			}
			groups, status, xerr := ExpandExpectations(spec.Probes, t.ProbeID, records, opts.ResourceName)
			if xerr != nil {
				return fail(i, t, xerr)
			}
			entry.Expected = groups
			entry.ExpectStatus = status
			rec.Probes = append(rec.Probes, entry)
		}
	}

	rec.Completed = true
	rec.WallTimeMS = time.Since(start).Milliseconds()
	if deps.Tokens != nil {
		rec.Tokens = deps.Tokens()
	}
	return rec, nil
}

// buildDiagnoseInfo 组装单次穿插诊断的内嵌 RunRecord
// 挂起（报告为空）记 suspended：后续轮的用户输入会被挂起恢复优先路径当作澄清答复，
// 脚本语义由记录如实反映；token 不逐诊断拆分（全会话累计在会话级）
func buildDiagnoseInfo(
	deps ProbeDeps,
	opts ProbeRunOptions,
	question string,
	res session.TurnResult,
	turnIndex int,
) (DiagnoseTurnInfo, error) {
	status := "completed"
	runErr := ""
	if res.Report == nil {
		status = "suspended"
		runErr = "suspended waiting for user clarification"
	}
	var stats DiagnoseStats
	if deps.DiagnoseStats != nil {
		stats = deps.DiagnoseStats()
	}
	// 账本是报告与证据的权威源（挂起时无产物，报告侧留空）
	var report = res.Report
	var evidence []core.Evidence
	if rec, err := deps.Ledger.Get(context.Background(), res.RunID); err == nil {
		if report == nil {
			report = &rec.Report
		}
		evidence = rec.Evidence
	}
	runRec := BuildRunRecord(
		res.RunID, question, opts.Model, opts.ProjectionMethod,
		AcquireRecordInfo{
			Method:    opts.Acquire.Method,
			MaxRounds: opts.Acquire.MaxRounds,
			Seed:      opts.Acquire.Seed,
			Exit:      stats.Exit,
			Gap:       stats.Gap,
		},
		status == "completed", runErr, report, evidence,
		nil, stats.Rounds, 0,
	)
	return DiagnoseTurnInfo{
		TurnIndex: turnIndex,
		RunID:     res.RunID,
		Status:    status,
		Record:    runRec,
	}, nil
}
