// 编排模块负责按固定顺序连接问题理解、目标定位、规划、执行、判断和报告
//
// 编排器只依赖各角色对调用方暴露的最小能力，不读取角色内部状态
// 定位阶段为编排可见循环：驱动提议 → 统一执行工具 → 回喂状态 → 提交目标
// 规划阶段仍按计划顺序串行执行任务；依赖图调度和持久化留到后续阶段
// 任务与证据编号由编排边界统一生成，工具与角色不能决定领域实体身份
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"aruing/internal/core"
)

// 描述编排器理解原始问题所需的最小能力
type parser interface {
	// 把运行数据转换为未验证的问题结构
	Parse(context.Context, core.Run) (core.Query, error)
}

// 规划阶段的累积状态，类比 ResolveState
//
// 进程内传输结构，不作为持久化实体（与 ResolveState 同）
// 首轮调用时 Evidence/Verdicts 为 nil，行为等价于 beta2 的盲猜单次规划
// 后续调查循环需要 Round/MaxRounds 时在此结构加字段，不改 Plan 签名
type PlanState struct {
	// 当前运行的问题结构，含已回填系统编号的节点
	Query core.Query
	// 定位阶段已确认的目标
	Targets []core.Target
	// 历次取证累积的证据；首轮为 nil 或定位阶段复用的 seed
	Evidence []core.Evidence
	// 上一次验证结果；首轮为 nil
	Verdicts []core.Verdict
	// 集群侦察发现的可用资源类型（含 CRD）；让 Planner 知道集群装了什么可查，而非盲猜标准资源
	ClusterResources []ClusterResource
}

// 集群侦察发现的资源类型条目（解析自 kubectl api-resources）
// 精简形态，仅含 Planner 判断「可查什么」所需的字段
type ClusterResource struct {
	// 资源复数名，如 ingressroutes、services、pods
	Name string `json:"name"`
	// 资源 Kind，如 IngressRoute、Service、Pod
	Kind string `json:"kind"`
	// 是否命名空间级（false 表示集群级）
	Namespaced bool `json:"namespaced"`
	// 所属 API 组（空表示核心组），如 traefik.io、networking.k8s.io
	APIGroup string `json:"apiGroup,omitempty"`
}

// 描述编排器生成猜想和任务所需的最小能力
type planner interface {
	// 根据问题结构、目标和已有证据返回本轮计划
	Plan(context.Context, PlanState) (Plan, error)
}

// 描述编排器执行单个工具任务所需的最小能力
type taskExecutor interface {
	// 执行任务并返回尚未分配领域编号的证据
	Execute(context.Context, core.Task) (*core.Evidence, error)
}

// 描述编排器根据证据形成判断所需的最小能力
// Query 作为用户原始问题的上下文喂给判断角色，使其能比对证据与用户实际提问
type verifier interface {
	// 根据用户问题、猜想、任务和实际证据返回判断列表
	Verify(context.Context, core.Query, []core.Hypothesis, []core.Task, []core.Evidence) ([]core.Verdict, error)
}

// 描述编排器生成最终用户输出所需的最小能力
type reporter interface {
	// 根据当前运行、判断和证据生成报告
	Report(context.Context, core.Run, []core.Verdict, []core.Evidence) (core.Report, error)
}

// 描述编排器创建动态领域实体所需的最小元数据能力
type entityFactory interface {
	// 根据开放前缀生成全局唯一编号
	NewID(string) (string, error)
	// 返回统一时区的当前时间
	Now() time.Time
}

// 保存完整假闭环所需的角色和执行依赖
// 实例只控制调用顺序，不承担任何角色内部的业务判断
type Orchestrator struct {
	// 负责把原始问题转换为结构化线索
	parser parser
	// 负责定位阶段每轮提议（工具 / 提交目标 / 失败）
	resolver ResolveDriver
	// 负责生成候选猜想和工具任务
	planner planner
	// 负责调用工具并返回证据内容
	executor taskExecutor
	// 负责依据证据生成判断
	verifier verifier
	// 负责把判断整理为最终报告
	reporter reporter
	// 负责为任务、证据和目标分配领域编号和创建时间
	factory entityFactory
	// 定位阶段工具调用预算，零值表示使用 defaultResolveMaxRounds
	resolveMaxRounds int
	// 调查阶段规划轮数预算，零值表示使用 defaultInvestigateMaxRounds
	// 默认 1 表示只跑一轮，与 beta2 单轮行为等价；多轮-3 调高并改 prompt 后才真正迭代
	investigateMaxRounds int
	// 是否启用集群侦察；wiring 在 k8s 工具注册后开启，避免无集群环境（fake/CI）产生噪音
	// 关闭时 reconCluster 直接返回 nil，不进入证据链、不打印失败
	reconEnabled bool
	// 运行过程进度输出；默认丢弃，CLI 接 stderr 让用户实时看到诊断流程
	progress io.Writer
}

// 绑定完整闭环所需依赖并创建编排器
// 依赖有效性在执行入口统一检查，创建过程不产生外部副作用
func NewOrchestrator(
	parserRole parser,
	resolverRole ResolveDriver,
	plannerRole planner,
	executor taskExecutor,
	verifierRole verifier,
	reporterRole reporter,
	factory entityFactory,
) *Orchestrator {
	return &Orchestrator{
		parser:   parserRole,
		resolver: resolverRole,
		planner:  plannerRole,
		executor: executor,
		verifier: verifierRole,
		reporter: reporterRole,
		factory:  factory,
		progress: io.Discard,
	}
}

// 覆盖定位阶段工具调用预算，maxRounds 小于等于 0 时恢复默认值
// 仅供 wiring 或测试在创建后调整，不影响已在进行的 Execute
func (o *Orchestrator) SetResolveMaxRounds(maxRounds int) {
	if o == nil {
		return
	}
	o.resolveMaxRounds = maxRounds
}

// 覆盖调查阶段规划轮数预算，maxRounds 小于等于 0 时恢复默认值
// 仅供 wiring 或测试调整；默认 1 保持单轮行为，调高后配合多轮 prompt 才产生迭代
func (o *Orchestrator) SetInvestigateMaxRounds(maxRounds int) {
	if o == nil {
		return
	}
	o.investigateMaxRounds = maxRounds
}

// 设置进度输出目标（通常为 stderr），nil 或不调用则静默
// 进度只写该 sink，不污染 stdout 报告；写失败被忽略，不影响诊断
func (o *Orchestrator) SetProgress(w io.Writer) {
	if o == nil {
		return
	}
	o.progress = w
}

// 开关集群侦察；wiring 在 k8s 工具注册后调用 true，无集群环境保持 false
// 关闭时侦察不尝试、不进证据链、不打印失败，避免无 kubectl 的 fake/CI 环境产生噪音
func (o *Orchestrator) SetReconEnabled(enabled bool) {
	if o == nil {
		return
	}
	o.reconEnabled = enabled
}

// 向进度 sink 写一行；sink 为 nil 时跳过
func (o *Orchestrator) progressf(format string, args ...any) {
	if o == nil || o.progress == nil {
		return
	}
	fmt.Fprintf(o.progress, format+"\n", args...)
}

// 工具执行失败的哨兵错误，与合成的 error evidence 一同返回
// 调用方据此区分「可容忍的工具失败」与「致命错误」；编排层决定容忍/暂停/重试
var errToolFailed = errors.New("tool execution failed")

// 从一次运行开始依次推进全部角色，成功时返回最终报告
// 从一次运行开始依次推进全部角色，成功时返回最终报告与调查阶段的全部证据
// 证据透出供 CLI 渲染调查链；线性 Execute→Report 是最小单轮驱动方式，不是对外长期契约（architecture #15）
func (o *Orchestrator) Execute(ctx context.Context, run core.Run) (core.Report, []core.Evidence, error) {
	if err := ctx.Err(); err != nil {
		return core.Report{}, nil, fmt.Errorf("execute run: %w", err)
	}
	if err := o.validate(); err != nil {
		return core.Report{}, nil, err
	}

	o.progressf("解析问题…")
	query, err := o.parser.Parse(ctx, run)
	if err != nil {
		return core.Report{}, nil, fmt.Errorf("parse run: %w", err)
	}
	targets, resolveEvidence, err := o.resolveLoop(ctx, query)
	if err != nil {
		return core.Report{}, nil, fmt.Errorf("resolve targets: %w", err)
	}
	// 集群侦察：发现集群实际可用资源类型（含 CRD），让 Planner 知道集群装了什么
	// 侦察 Evidence 单独存放，不进 investigateLoop 种子（不漏到 Verifier）；由本方法合并进返回链
	reconEvidence, clusterResources := o.reconCluster(ctx, run.ID)
	if clusterResources != nil {
		o.progressf("侦察到 %d 种资源类型", len(clusterResources))
	}
	// 调查阶段为编排可见循环：Plan→Execute→Verify，证据不足时带历史证据再 Plan
	// 默认只跑一轮与 beta2 等价；预算调高后配合 prompt 才真正迭代（architecture #15-#16）
	// 定位阶段已取的作为首轮 seed 复用，不白查已取信息；侦察证据不在此列
	evidence, verdicts, err := o.investigateLoop(ctx, query, targets, resolveEvidence, clusterResources)
	if err != nil {
		return core.Report{}, nil, err
	}
	// 按时间序组装完整证据链：定位 → 侦察 → 调查，全部对用户透明可追溯
	// Reporter 只看定位+调查证据（侦察是 context，不是结论依据）；返回链含侦察供 CLI 渲染
	chain := appendEvidence(evidence, reconEvidence, len(resolveEvidence))
	o.progressf("生成报告…")
	report, err := o.reporter.Report(ctx, run, verdicts, evidence)
	if err != nil {
		return core.Report{}, nil, fmt.Errorf("build report: %w", err)
	}
	return report, chain, nil
}

// 把侦察证据插入到证据链的定位块之后、调查块之前（按发生时间顺序）
// resolveCount 为定位阶段证据数量（即 evidence 的前缀长度）；recon 为 nil 时原样返回
func appendEvidence(evidence []core.Evidence, recon *core.Evidence, resolveCount int) []core.Evidence {
	if recon == nil {
		return evidence
	}
	if resolveCount > len(evidence) {
		resolveCount = len(evidence)
	}
	chain := make([]core.Evidence, 0, len(evidence)+1)
	chain = append(chain, evidence[:resolveCount]...) // 定位
	chain = append(chain, *recon)                     // 侦察
	chain = append(chain, evidence[resolveCount:]...) // 调查
	return chain
}

// 调查阶段默认规划轮数：1 表示只跑一轮，与 beta2 单轮行为等价
// 多轮-3 调高 wiring 默认值并改 prompt 后才真正迭代取证
const defaultInvestigateMaxRounds = 1

// 调查阶段循环：Plan→Execute→Verify，证据不足时带历史证据再 Plan
//
// 镜像 resolveLoop 的编排可见模式：累积状态在编排层，角色只返回本轮计划
// 工具仍经 executeTask/Dispatcher，角色不私自调工具（#16）
// 三个出口：找到 supported / 预算耗尽 / 后续轮规划器返回空任务
// seedEvidence 为定位阶段已登证据，作为首轮上下文复用（调查首轮 Planner 能看到）
// clusterResources 为集群侦察发现，每次 Plan 调用都带入 PlanState
func (o *Orchestrator) investigateLoop(
	ctx context.Context,
	query core.Query,
	targets []core.Target,
	seedEvidence []core.Evidence,
	clusterResources []ClusterResource,
) ([]core.Evidence, []core.Verdict, error) {
	maxRounds := o.investigateMaxRounds
	if maxRounds <= 0 {
		maxRounds = defaultInvestigateMaxRounds
	}

	var hypotheses []core.Hypothesis
	var tasks []core.Task
	// 首轮以定位阶段已取证据起步，不白查；累积语义不变
	evidence := slices.Clone(seedEvidence)
	var verdicts []core.Verdict

	for round := 0; round < maxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, fmt.Errorf("investigate: %w", err)
		}

		o.progressf("调查第 %d 轮…", round+1)
		plan, err := o.planner.Plan(ctx, PlanState{
			Query:            query,
			Targets:          targets,
			Evidence:         evidence,
			Verdicts:         verdicts,
			ClusterResources: clusterResources,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("plan tasks (round %d): %w", round, err)
		}

		// 后续轮规划器若无可补查的任务，视为调查结束，沿用上一轮判断
		if round > 0 && len(plan.Tasks) == 0 {
			o.progressf("  规划器无更多任务，结束调查")
			break
		}
		o.progressf("  规划 %d 个取证任务", len(plan.Tasks))

		// 猜想跨轮累积，Verifier 每轮拿全量重判
		hypotheses = append(hypotheses, plan.Hypotheses...)
		for _, task := range plan.Tasks {
			item, executeErr := o.executeTask(ctx, task)
			if executeErr != nil && (!errors.Is(executeErr, errToolFailed) || item == nil) {
				return nil, nil, fmt.Errorf("execute task %q: %w", task.ID, executeErr)
			}
			// errToolFailed 时 item 为合成的 error evidence，容忍继续（未来此处可改暂停问用户）
			tasks = append(tasks, task)
			evidence = append(evidence, *item)
		}

		verdicts, err = o.verifier.Verify(ctx, query, hypotheses, tasks, evidence)
		if err != nil {
			return nil, nil, fmt.Errorf("verify evidence (round %d): %w", round, err)
		}
		o.progressf("  验证：%s", summarizeVerdicts(verdicts))
		// 至少一个猜想被支持即找到根因，结束调查
		// 全部被排除不算完成——应继续生成新猜想（预算兜底）
		if hasSupportedVerdict(verdicts) {
			break
		}
	}

	return evidence, verdicts, nil
}

// 判断是否存在被证据支持的猜想，存在即已找到根因、循环应结束
// 全部被排除（refuted）不算完成——应继续生成新猜想，由预算兜底
func hasSupportedVerdict(verdicts []core.Verdict) bool {
	for _, v := range verdicts {
		if v.Result == core.VerdictSupported {
			return true
		}
	}
	return false
}

// 统计判断结果用于进度展示
func summarizeVerdicts(verdicts []core.Verdict) string {
	var supported, refuted, insufficient int
	for _, v := range verdicts {
		switch v.Result {
		case core.VerdictSupported:
			supported++
		case core.VerdictRefuted:
			refuted++
		case core.VerdictInsufficient:
			insufficient++
		}
	}
	return fmt.Sprintf("%d 支持 / %d 排除 / %d 证据不足", supported, refuted, insufficient)
}

// 集群侦察：经编排的统一执行通道跑一次只读 `kubectl api-resources`，发现集群实际可用资源类型（含 CRD）
//
// 与定位/调查同源信任模型：走 executeTask → Dispatcher，Task ID 由 Factory 发放（不硬编码），
// 产出的 Evidence 进报告链供用户追溯；api-resources 在只读白名单（#12/#16）
// 返回侦察证据与解析后的精简资源清单：
//   - 成功：Evidence.Summary 为发现摘要，Evidence.Raw 含原始 stdout；resources 为解析结果
//   - 工具失败：executeTask 合成 error evidence（errToolFailed），透传进链（透明），resources 为 nil
//   - 无 k8s 工具注册 / Task ID 生成失败：返回 (nil, nil)，根本未尝试，无可展示
//
// 侦察 Evidence 不进 investigateLoop 的种子切片，因此不漏到 Verifier（侦察是 Planner 的 context，
// 不是支持或排除猜想的依据）；由 Execute 单独合并进最终返回链
func (o *Orchestrator) reconCluster(ctx context.Context, runID string) (*core.Evidence, []ClusterResource) {
	if !o.reconEnabled || o.executor == nil || o.factory == nil {
		return nil, nil
	}
	taskID, err := o.factory.NewID("t")
	if err != nil || taskID == "" {
		// 无法发号则放弃侦察，不阻断诊断
		return nil, nil
	}
	// api-resources 为集群级发现，不关联具体 node/target，Refs 为空
	task := core.Task{
		ID:        taskID,
		RunID:     runID,
		ToolName:  "k8s",
		Arguments: json.RawMessage(`{"argv":["api-resources"]}`),
		Purpose:   "侦察集群可用资源类型（含 CRD）",
	}
	item, execErr := o.executeTask(ctx, task)
	// 仅 ctx 取消等致命错误才放弃；errToolFailed 已带合成 error evidence，透传以保持透明
	if execErr != nil && (!errors.Is(execErr, errToolFailed) || item == nil) {
		return nil, nil
	}
	if item == nil {
		return nil, nil
	}
	// 解析原始 stdout 得精简清单；失败（含 error evidence 无可用 Raw）时 resources 为 nil
	resources := parseAPIResources(extractStdout(item.Raw))
	if execErr != nil {
		// 工具失败：保留 executeTask 合成的 error evidence，覆写 Summary 标注侦察目的
		item.Summary = "侦察集群可用资源类型失败"
		return item, nil
	}
	if len(resources) > 0 {
		item.Summary = fmt.Sprintf("侦察集群可用资源类型：发现 %d 种（含 CRD）", len(resources))
	}
	return item, resources
}

// 从 k8s 工具 Evidence.Raw 中取出 stdout 文本；非预期结构返回空串
func extractStdout(raw json.RawMessage) string {
	var parsed struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ""
	}
	return parsed.Stdout
}

// 解析 `kubectl api-resources` 的 stdout（空格/制表分隔的列：NAME SHORTNAMES NAMESPACED KIND [APIVERSION]）
// 锚定 NAMESPACED 列（恒为 true/false）定位 KIND，规避 SHORTNAMES 为空导致的列错位
// 精简为 4 字段清单全量送 Planner；仅留 maxKeep 安全帽防病态集群，截断不挑类别（避免硬编码偏见）
func parseAPIResources(stdout string) []ClusterResource {
	const maxKeep = 300
	var out []ClusterResource
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		// 至少 NAME + NAMESPACED + KIND 三列；跳过表头
		if len(fields) < 3 || fields[0] == "NAME" {
			continue
		}
		// 找到 NAMESPACED 列（首个 true/false）；其右一列为 KIND，再右若含 / 即 APIVERSION
		nsIdx := -1
		for i, f := range fields {
			if f == "true" || f == "false" {
				nsIdx = i
				break
			}
		}
		if nsIdx < 0 || nsIdx+1 >= len(fields) {
			continue
		}
		r := ClusterResource{
			Name:       fields[0],
			Namespaced: fields[nsIdx] == "true",
			Kind:       fields[nsIdx+1],
		}
		if next := nsIdx + 2; next < len(fields) && strings.Contains(fields[next], "/") {
			r.APIGroup = strings.SplitN(fields[next], "/", 2)[0]
		}
		out = append(out, r)
		if len(out) >= maxKeep {
			break
		}
	}
	return out
}

// 定位阶段循环：驱动提议 → 统一执行工具并发号 → 回喂 → 提交目标
// 角色不得在此循环外私自调工具；预算耗尽或 fail 动作时返回错误
// 返回定位阶段已登记的证据，供调查阶段复用（不白查已取信息）
func (o *Orchestrator) resolveLoop(ctx context.Context, query core.Query) ([]core.Target, []core.Evidence, error) {
	maxRounds := o.resolveMaxRounds
	if maxRounds <= 0 {
		maxRounds = defaultResolveMaxRounds
	}

	state := ResolveState{
		Query:     query,
		Tasks:     nil,
		Evidence:  nil,
		Round:     0,
		MaxRounds: maxRounds,
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}

		action, err := o.resolver.Next(ctx, state)
		if err != nil {
			return nil, nil, fmt.Errorf("driver next: %w", err)
		}

		switch action.Action {
		case ResolveActionCallTool:
			if len(action.ToolCalls) == 0 {
				return nil, nil, errors.New("call_tool requires at least one tool call")
			}
			for _, call := range action.ToolCalls {
				if state.Round >= state.MaxRounds {
					return nil, nil, fmt.Errorf("resolve budget exceeded after %d tool calls", state.MaxRounds)
				}
				if err := o.applyToolCall(ctx, &state, call); err != nil {
					return nil, nil, err
				}
			}
		case ResolveActionSubmitTargets:
			targets, mErr := o.materializeTargets(query, action, state)
			if mErr != nil {
				return nil, nil, mErr
			}
			o.progressf("定位到 %d 个目标", len(targets))
			return targets, slices.Clone(state.Evidence), nil
		case ResolveActionFail:
			msg := action.Error
			if msg == "" {
				msg = action.Reason
			}
			if msg == "" {
				msg = "resolve failed"
			}
			return nil, nil, errors.New(msg)
		default:
			return nil, nil, fmt.Errorf("unknown resolve action %q", action.Action)
		}
	}
}

// 将单次提议的工具调用转为任务、经执行器取证并登记到定位状态
func (o *Orchestrator) applyToolCall(ctx context.Context, state *ResolveState, call ProposedToolCall) error {
	if call.ToolName == "" {
		return errors.New("tool call requires a tool name")
	}

	taskID, err := o.factory.NewID("t")
	if err != nil {
		return fmt.Errorf("create resolve task ID: %w", err)
	}
	if taskID == "" {
		return errors.New("create resolve task ID: ID is required")
	}

	task := core.Task{
		ID:        taskID,
		RunID:     state.Query.RunID,
		Refs:      slices.Clone(call.Refs),
		ToolName:  call.ToolName,
		Arguments: slices.Clone(call.Arguments),
		Purpose:   call.Purpose,
	}

	item, err := o.executeTask(ctx, task)
	if err != nil && (!errors.Is(err, errToolFailed) || item == nil) {
		return fmt.Errorf("execute resolve tool %q: %w", call.ToolName, err)
	}
	// errToolFailed 时 item 为合成的 error evidence，容忍继续

	state.Tasks = append(state.Tasks, task)
	state.Evidence = append(state.Evidence, *item)
	state.Round++
	return nil
}

// 执行任务并为证据发放编号与创建时间，定位与调查阶段共用
//
// 进度分两段：执行前打印工具与目的（实时反馈，长任务不静默）；
// 执行后补一行工具返回的命令视图（如 kubectl argv），便于排查模型生成的命令是否合理
func (o *Orchestrator) executeTask(ctx context.Context, task core.Task) (*core.Evidence, error) {
	if task.Purpose != "" {
		o.progressf("  执行 %s：%s", task.ToolName, task.Purpose)
	} else {
		o.progressf("  执行 %s", task.ToolName)
	}
	evidenceID, idErr := o.factory.NewID("e")
	if idErr != nil {
		return nil, fmt.Errorf("create evidence ID: %w", idErr)
	}
	if evidenceID == "" {
		return nil, errors.New("create evidence ID: ID is required")
	}
	item, executeErr := o.executor.Execute(ctx, task)
	if executeErr != nil {
		// 整次 run 被取消才传播；其余工具失败透传给调用方决策
		if ctx.Err() != nil {
			return nil, fmt.Errorf("execute task %q: %w", task.ID, ctx.Err())
		}
		o.progressf("    ↳ 失败：%v", executeErr)
		return &core.Evidence{
			ID:        evidenceID,
			RunID:     task.RunID,
			TaskID:    task.ID,
			ToolName:  task.ToolName,
			Summary:   "工具执行失败",
			Error:     executeErr.Error(),
			CreatedAt: o.factory.Now(),
		}, errToolFailed
	}
	if item == nil {
		return nil, errors.New("evidence is required")
	}
	item.ID = evidenceID
	item.CreatedAt = o.factory.Now()
	if item.CommandView != "" {
		o.progressf("    ↳ %s", item.CommandView)
	}
	return item, nil
}

// 校验并物化提交的目标：NodeID 必须在 Query 内，EvidenceIDs 必须是本阶段已登记编号
// Target ID 与 CreatedAt 由工厂在编排边界生成
func (o *Orchestrator) materializeTargets(query core.Query, action ResolveAction, state ResolveState) ([]core.Target, error) {
	if len(action.Targets) == 0 {
		return nil, errors.New("submit_targets requires at least one target")
	}

	nodeIDs := make(map[string]struct{}, len(query.Nodes))
	for _, node := range query.Nodes {
		if node.ID != "" {
			nodeIDs[node.ID] = struct{}{}
		}
	}
	evidenceIDs := make(map[string]struct{}, len(state.Evidence))
	for _, item := range state.Evidence {
		if item.ID != "" {
			evidenceIDs[item.ID] = struct{}{}
		}
	}

	targets := make([]core.Target, 0, len(action.Targets))
	now := o.factory.Now()
	for index, proposed := range action.Targets {
		if proposed.NodeID == "" {
			return nil, fmt.Errorf("target[%d] node id is required", index)
		}
		if _, ok := nodeIDs[proposed.NodeID]; !ok {
			return nil, fmt.Errorf("target references unknown node %q", proposed.NodeID)
		}
		for _, evidenceID := range proposed.EvidenceIDs {
			if _, ok := evidenceIDs[evidenceID]; !ok {
				return nil, fmt.Errorf("target references unknown evidence %q", evidenceID)
			}
		}
		targetID, err := o.factory.NewID("target")
		if err != nil {
			return nil, fmt.Errorf("create target ID: %w", err)
		}
		if targetID == "" {
			return nil, errors.New("create target ID: ID is required")
		}
		targets = append(targets, core.Target{
			ID:          targetID,
			RunID:       query.RunID,
			NodeID:      proposed.NodeID,
			Type:        proposed.Type,
			Attrs:       maps.Clone(proposed.Attrs),
			EvidenceIDs: slices.Clone(proposed.EvidenceIDs),
			CreatedAt:   now,
		})
	}
	return targets, nil
}

// 检查执行所需依赖是否完整，避免在流程中途出现空对象崩溃
func (o *Orchestrator) validate() error {
	if o == nil {
		return errors.New("orchestrator is required")
	}
	if o.parser == nil || o.resolver == nil || o.planner == nil {
		return errors.New("orchestrator requires parser, resolver, and planner")
	}
	if o.executor == nil || o.verifier == nil || o.reporter == nil {
		return errors.New("orchestrator requires executor, verifier, and reporter")
	}
	if o.factory == nil {
		return errors.New("orchestrator requires an entity factory")
	}
	return nil
}
