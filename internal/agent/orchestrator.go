// 智能体包放诊断过程中的推理角色，以及会话总控基线塔
//
// 这里的智能体先只是进程内的角色边界，不代表独立服务
// 解析器负责理解问题；定位器通过编排可见循环确认目标；规划器生成猜想和取证任务；
// 验证器只基于证据判断；报告器把过程整理成人能读的报告
// 基线塔实现会话应答器：本轮直接回复或升格正式诊断，不在本包写会话消息
//
// 解析、定位、规划、验证、报告与基线塔均可接大模型（提示词经嵌入）
// 定位阶段：角色只提议意图，工具经调度器统一执行，编号由编排发放
// 规划阶段为单次规划调用，不在角色内多轮调工具；猜想与任务编号经工厂在规划器内回填
// 验证阶段为单次验证调用；判决只能引用已登记证据，编号经工厂回填
// 报告阶段为单次报告调用；结论对齐判决，证据引用不得越界，报告编号经工厂回填
// 升格经会话层建运行并调编排执行
//
// 编排器只依赖各角色对调用方暴露的最小能力，不读取角色内部状态；
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
	"sync"
	"time"

	"aruing/internal/core"
)

// 描述编排器理解原始问题所需的最小能力
type parser interface {
	// 把运行数据转换为未验证的问题结构
	Parse(context.Context, core.Run) (core.Query, error)
}

// 规划阶段的累积状态，类比定位状态
//
// 进程内传输结构，不作为持久化实体（与定位状态同）
// 首轮调用时证据与判决为空，行为等价于早期盲猜单次规划
// 后续调查循环需要轮次字段时在此结构加字段，不改计划签名
type PlanState struct {
	// 当前运行的问题结构，含已回填系统编号的节点
	Query core.Query
	// 定位阶段已确认的目标
	Targets []core.Target
	// 历次取证累积的证据；首轮为空或定位阶段复用的种子
	Evidence []core.Evidence
	// 上一次验证结果；首轮为空
	Verdicts []core.Verdict
	// 集群侦察发现的可用资源类型（含自定义资源）；让规划器知道集群装了什么可查，而非盲猜标准资源
	ClusterResources []ClusterResource
}

// 集群侦察发现的资源类型条目（解析自集群资源列表命令）
// 精简形态，仅含规划器判断「可查什么」所需的字段
type ClusterResource struct {
	// 资源复数名，如路由入口复数、服务复数、容器组复数
	Name string `json:"name"`
	// 资源种类名，如路由入口、服务、容器组
	Kind string `json:"kind"`
	// 是否命名空间级（假表示集群级）
	Namespaced bool `json:"namespaced"`
	// 所属接口组（空表示核心组），如反向代理域、网络接口域
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
// 问题结构作为用户原始问题的上下文喂给判断角色，使其能比对证据与用户实际提问
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

// 进程内挂起运行快照：澄清后 Resume 重跑定位起
// 不做磁盘持久化；进程退出即丢
type suspendedRun struct {
	// 运行快照（含问题与会话编号）
	run core.Run
	// 解析后的问题结构，Resume 时复用避免重复解析
	query core.Query
	// 定位状态（任务、证据、澄清累积）
	resolve ResolveState
	// 最近一次澄清请求（面向用户）
	clarify ClarifyRequest
}

// 保存完整假闭环所需的角色和执行依赖
// 实例只控制调用顺序，不承担任何角色内部的业务判断
type Orchestrator struct {
	// 负责把原始问题转换为结构化线索
	parser parser
	// 负责定位阶段每轮提议（工具 / 提交目标 / 澄清 / 失败）
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
	// 定位阶段工具调用预算，零值表示使用默认定位轮次上限
	resolveMaxRounds int
	// 调查阶段规划轮数预算，零值表示使用默认调查轮次上限
	// 默认一轮，与早期单轮行为等价；调高并改多轮提示词后才真正迭代
	investigateMaxRounds int
	// 是否启用集群侦察；装配层在集群工具注册后开启，避免无集群环境（假实现或持续集成）产生噪音
	// 关闭时侦察直接返回空，不进入证据链、不打印失败
	reconEnabled bool
	// 运行过程进度输出；默认丢弃，命令行接标准错误让用户实时看到诊断流程
	progress io.Writer
	// 挂起运行索引（runID → 快照）；澄清 Resume 用
	mu        sync.Mutex
	suspended map[string]*suspendedRun
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

// 覆盖定位阶段工具调用预算，轮数小于等于零时恢复默认值
// 仅供装配层或测试在创建后调整，不影响已在进行的执行
func (o *Orchestrator) SetResolveMaxRounds(maxRounds int) {
	if o == nil {
		return
	}
	o.resolveMaxRounds = maxRounds
}

// 覆盖调查阶段规划轮数预算，轮数小于等于零时恢复默认值
// 仅供装配层或测试调整；默认一轮保持单轮行为，调高后配合多轮提示词才产生迭代
func (o *Orchestrator) SetInvestigateMaxRounds(maxRounds int) {
	if o == nil {
		return
	}
	o.investigateMaxRounds = maxRounds
}

// 设置进度输出目标（通常为标准错误），空或不调用则静默
// 进度只写该输出，不污染标准输出报告；写失败被忽略，不影响诊断
func (o *Orchestrator) SetProgress(w io.Writer) {
	if o == nil {
		return
	}
	o.progress = w
}

// 开关集群侦察；装配层在集群工具注册后开启，无集群环境保持关闭
// 关闭时侦察不尝试、不进证据链、不打印失败，避免无集群命令的假实现或持续集成环境产生噪音
func (o *Orchestrator) SetReconEnabled(enabled bool) {
	if o == nil {
		return
	}
	o.reconEnabled = enabled
}

// 向进度输出写一行；输出为空时跳过
func (o *Orchestrator) progressf(format string, args ...any) {
	if o == nil || o.progress == nil {
		return
	}
	fmt.Fprintf(o.progress, format+"\n", args...)
}

// 工具执行失败的哨兵错误，与合成的失败证据一同返回
// 调用方据此区分「可容忍的工具失败」与「致命错误」；编排层决定容忍、暂停或重试
var errToolFailed = errors.New("tool execution failed")

// 从一次运行开始依次推进全部角色；完成时 Outcome.Report 非空，需澄清时 Outcome.Suspension 非空
// 证据透出供命令行渲染调查链；线性执行到报告是最小单轮驱动方式，不是对外长期契约
func (o *Orchestrator) Execute(ctx context.Context, run core.Run) (core.Outcome, error) {
	if err := ctx.Err(); err != nil {
		return core.Outcome{}, fmt.Errorf("execute run: %w", err)
	}
	if err := o.validate(); err != nil {
		return core.Outcome{}, err
	}

	o.progressf("解析问题…")
	query, err := o.parser.Parse(ctx, run)
	if err != nil {
		return core.Outcome{}, fmt.Errorf("parse run: %w", err)
	}
	return o.continueFromResolve(ctx, run, query, ResolveState{})
}

// 用户澄清后恢复挂起运行：注入答复并自定位阶段重跑（调查等阶段日后同此入口按 Stage 派发）
func (o *Orchestrator) Resume(ctx context.Context, runID, answer string) (core.Outcome, error) {
	if err := ctx.Err(); err != nil {
		return core.Outcome{}, fmt.Errorf("resume run: %w", err)
	}
	if err := o.validate(); err != nil {
		return core.Outcome{}, err
	}
	if strings.TrimSpace(runID) == "" {
		return core.Outcome{}, errors.New("resume: run id is required")
	}
	if strings.TrimSpace(answer) == "" {
		return core.Outcome{}, errors.New("resume: answer is required")
	}

	snap, ok := o.takeSuspended(runID)
	if !ok {
		return core.Outcome{}, fmt.Errorf("resume: no suspended run %q", runID)
	}

	state := snap.resolve
	state.Clarifications = append(slices.Clone(state.Clarifications), strings.TrimSpace(answer))
	// 澄清后重跑定位：保留已取证据与任务，但重置轮次预算计数，避免触顶后无法消歧
	// 证据/任务仍回喂驱动；Round 仅表示本段工具调用次数
	state.Round = 0

	o.progressf("恢复运行 %s（澄清已注入）…", runID)
	return o.continueFromResolve(ctx, snap.run, snap.query, state)
}

// 查找会话内挂起运行编号；无则空串
// 约定单会话至多一条挂起；多条时取 map 遍历首个（进程内极少并发挂起）
func (o *Orchestrator) FindSuspended(sessionID string) string {
	if o == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for id, snap := range o.suspended {
		if snap != nil && snap.run.SessionID == sessionID {
			return id
		}
	}
	return ""
}

// 自定位阶段继续：可被 Execute（空 state）与 Resume（带澄清）共用
func (o *Orchestrator) continueFromResolve(
	ctx context.Context,
	run core.Run,
	query core.Query,
	seed ResolveState,
) (core.Outcome, error) {
	targets, resolveEvidence, clarify, pausedState, err := o.resolveLoop(ctx, query, seed)
	if err != nil {
		return core.Outcome{}, fmt.Errorf("resolve targets: %w", err)
	}
	if clarify != nil {
		// 挂起：保存快照，返回澄清问题；不进入侦察/调查
		o.putSuspended(run, query, *clarify, pausedState)
		return core.Outcome{
			Suspension: &core.Suspension{
				RunID:     run.ID,
				SessionID: run.SessionID,
				Stage:     core.StageResolve,
				Question:  clarify.Question,
				Options:   slices.Clone(clarify.Options),
			},
		}, nil
	}

	// 集群侦察：发现集群实际可用资源类型（含自定义资源），让规划器知道集群装了什么
	// 侦察证据单独存放，不进调查循环种子（不漏到验证器）；由本方法合并进返回链
	reconEvidence, clusterResources := o.reconCluster(ctx, run.ID)
	if clusterResources != nil {
		o.progressf("侦察到 %d 种资源类型", len(clusterResources))
	}
	// 调查阶段为编排可见循环：计划、执行、验证，证据不足时带历史证据再计划
	// 默认只跑一轮与早期单轮等价；预算调高后配合提示词才真正迭代
	// 定位阶段已取的作为首轮种子复用，不白查已取信息；侦察证据不在此列
	evidence, verdicts, err := o.investigateLoop(ctx, query, targets, resolveEvidence, clusterResources)
	if err != nil {
		return core.Outcome{}, err
	}
	// 按时间序组装完整证据链：定位 → 侦察 → 调查，全部对用户透明可追溯
	// 报告器只看定位与调查证据（侦察是上下文，不是结论依据）；返回链含侦察供命令行渲染
	chain := appendEvidence(evidence, reconEvidence, len(resolveEvidence))
	o.progressf("生成报告…")
	report, err := o.reporter.Report(ctx, run, verdicts, evidence)
	if err != nil {
		return core.Outcome{}, fmt.Errorf("build report: %w", err)
	}
	return core.Outcome{
		Report:   &report,
		Evidence: chain,
	}, nil
}

// 存入挂起快照并标记运行状态为等待用户；深拷贝定位状态避免污染
func (o *Orchestrator) putSuspended(run core.Run, query core.Query, clarify ClarifyRequest, state ResolveState) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.suspended == nil {
		o.suspended = make(map[string]*suspendedRun)
	}
	// 深拷贝切片，避免后续修改污染快照
	snap := &suspendedRun{
		run:     run,
		query:   query,
		resolve: cloneResolveState(state),
		clarify: ClarifyRequest{
			Question: clarify.Question,
			Options:  slices.Clone(clarify.Options),
		},
	}
	snap.run.Status = core.RunStatusWaitingUser
	o.suspended[run.ID] = snap
}

// 取出并删除挂起快照；不存在时返回 false，调用方据此报错或降级
func (o *Orchestrator) takeSuspended(runID string) (*suspendedRun, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.suspended == nil {
		return nil, false
	}
	snap, ok := o.suspended[runID]
	if !ok {
		return nil, false
	}
	delete(o.suspended, runID)
	return snap, true
}

// 复制定位状态中的切片字段，避免挂起快照与进行中状态共享底层数组
func cloneResolveState(state ResolveState) ResolveState {
	return ResolveState{
		Query:          state.Query,
		Tasks:          slices.Clone(state.Tasks),
		Evidence:       slices.Clone(state.Evidence),
		Round:          state.Round,
		MaxRounds:      state.MaxRounds,
		Clarifications: slices.Clone(state.Clarifications),
	}
}

// 把侦察证据插入到证据链的定位块之后、调查块之前（按发生时间顺序）
// 定位计数为定位阶段证据数量（即证据切片的前缀长度）；侦察为空时原样返回
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

// 调查阶段默认规划轮数：一轮，与早期单轮行为等价
// 调高装配默认值并改多轮提示词后才真正迭代取证
const defaultInvestigateMaxRounds = 1

// 调查阶段循环：计划、执行、验证，证据不足时带历史证据再计划
//
// 镜像定位循环的编排可见模式：累积状态在编排层，角色只返回本轮计划
// 工具仍经任务执行与调度器，角色不私自调工具
// 三个出口：找到被支持的猜想、预算耗尽、后续轮规划器返回空任务
// 种子证据为定位阶段已登证据，作为首轮上下文复用（调查首轮规划器能看到）
// 集群资源为侦察发现，每次计划调用都带入规划状态
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

		// 猜想跨轮累积，验证器每轮拿全量重判
		hypotheses = append(hypotheses, plan.Hypotheses...)
		for _, task := range plan.Tasks {
			item, executeErr := o.executeTask(ctx, task)
			if executeErr != nil && (!errors.Is(executeErr, errToolFailed) || item == nil) {
				return nil, nil, fmt.Errorf("execute task %q: %w", task.ID, executeErr)
			}
			// 工具失败时条目为合成的失败证据，容忍继续（未来此处可改暂停问用户）
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
// 全部被排除不算完成——应继续生成新猜想，由预算兜底
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

// 集群侦察：经编排的统一执行通道跑一次只读集群资源列表，发现实际可用资源类型（含自定义资源）
//
// 与定位、调查同源信任模型：走任务执行与调度器，任务编号由工厂发放（不硬编码），
// 产出的证据进报告链供用户追溯；资源列表命令在只读白名单内
// 返回侦察证据与解析后的精简资源清单：
//   - 成功：摘要为发现说明，原始输出含标准输出；资源列表为解析结果
//   - 工具失败：合成失败证据并透传进链（透明），资源列表为空
//   - 无集群工具注册或任务编号生成失败：返回空，根本未尝试，无可展示
//
// 侦察证据不进调查循环的种子切片，因此不漏到验证器（侦察是规划器的上下文，
// 不是支持或排除猜想的依据）；由执行入口单独合并进最终返回链
func (o *Orchestrator) reconCluster(ctx context.Context, runID string) (*core.Evidence, []ClusterResource) {
	if !o.reconEnabled || o.executor == nil || o.factory == nil {
		return nil, nil
	}
	taskID, err := o.factory.NewID("t")
	if err != nil || taskID == "" {
		// 无法发号则放弃侦察，不阻断诊断
		return nil, nil
	}
	// 资源列表为集群级发现，不关联具体节点或目标，引用为空
	task := core.Task{
		ID:        taskID,
		RunID:     runID,
		ToolName:  "k8s",
		Arguments: json.RawMessage(`{"argv":["api-resources"]}`),
		Purpose:   "侦察集群可用资源类型（含自定义资源）",
	}
	item, execErr := o.executeTask(ctx, task)
	// 仅上下文取消等致命错误才放弃；工具失败已带合成失败证据，透传以保持透明
	if execErr != nil && (!errors.Is(execErr, errToolFailed) || item == nil) {
		return nil, nil
	}
	if item == nil {
		return nil, nil
	}
	// 解析原始标准输出得精简清单；失败（含失败证据无可用原始输出）时资源列表为空
	resources := parseAPIResources(extractStdout(item.Raw))
	if execErr != nil {
		// 工具失败：保留任务执行合成的失败证据，覆写摘要标注侦察目的
		item.Summary = "侦察集群可用资源类型失败"
		return item, nil
	}
	if len(resources) > 0 {
		item.Summary = fmt.Sprintf("侦察集群可用资源类型：发现 %d 种（含自定义资源）", len(resources))
	}
	return item, resources
}

// 从集群工具证据原始输出中取出标准输出文本；非预期结构返回空串
func extractStdout(raw json.RawMessage) string {
	var parsed struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ""
	}
	return parsed.Stdout
}

// 解析集群资源列表命令的标准输出（空格或制表分隔的列：名称、短名、是否命名空间级、种类、可选接口版本）
// 锚定是否命名空间级列（恒为真或假）定位种类，规避短名为空导致的列错位
// 精简为四字段清单全量送规划器；仅留安全上限防病态集群，截断不挑类别（避免硬编码偏见）
func parseAPIResources(stdout string) []ClusterResource {
	const maxKeep = 300
	var out []ClusterResource
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		// 至少名称、是否命名空间级、种类三列；跳过表头
		if len(fields) < 3 || fields[0] == "NAME" {
			continue
		}
		// 找到是否命名空间级列（首个真或假）；其右一列为种类，再右若含斜杠即接口版本
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

// 定位阶段循环：驱动提议 → 统一执行工具并发号 → 回喂 → 提交目标 / 澄清挂起
// 角色不得在此循环外私自调工具；预算耗尽或失败动作时返回错误
// seed 可带已有证据与澄清答复（Resume）；空 seed 表示首跑
// 返回：目标、定位证据、澄清请求、挂起时完整定位状态、错误
// 澄清与成功提交互斥；澄清时 targets/evidence 为空，paused 为当前状态快照
func (o *Orchestrator) resolveLoop(
	ctx context.Context,
	query core.Query,
	seed ResolveState,
) (targets []core.Target, evidence []core.Evidence, clarify *ClarifyRequest, paused ResolveState, err error) {
	maxRounds := o.resolveMaxRounds
	if maxRounds <= 0 {
		maxRounds = defaultResolveMaxRounds
	}

	state := seed
	state.Query = query
	if state.MaxRounds <= 0 {
		state.MaxRounds = maxRounds
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, ResolveState{}, err
		}

		action, err := o.resolver.Next(ctx, state)
		if err != nil {
			return nil, nil, nil, ResolveState{}, fmt.Errorf("driver next: %w", err)
		}

		switch action.Action {
		case ResolveActionCallTool:
			if len(action.ToolCalls) == 0 {
				return nil, nil, nil, ResolveState{}, errors.New("call_tool requires at least one tool call")
			}
			for _, call := range action.ToolCalls {
				if state.Round >= state.MaxRounds {
					return nil, nil, nil, ResolveState{}, fmt.Errorf("resolve budget exceeded after %d tool calls", state.MaxRounds)
				}
				if err := o.applyToolCall(ctx, &state, call); err != nil {
					return nil, nil, nil, ResolveState{}, err
				}
			}
		case ResolveActionSubmitTargets:
			targets, mErr := o.materializeTargets(query, action, state)
			if mErr != nil {
				return nil, nil, nil, ResolveState{}, mErr
			}
			o.progressf("定位到 %d 个目标", len(targets))
			return targets, slices.Clone(state.Evidence), nil, ResolveState{}, nil
		case ResolveActionClarify:
			if action.Clarify == nil || strings.TrimSpace(action.Clarify.Question) == "" {
				return nil, nil, nil, ResolveState{}, errors.New("clarify requires a non-empty question")
			}
			o.progressf("定位需澄清：%s", action.Clarify.Question)
			return nil, nil, &ClarifyRequest{
				Question: strings.TrimSpace(action.Clarify.Question),
				Options:  slices.Clone(action.Clarify.Options),
			}, cloneResolveState(state), nil
		case ResolveActionFail:
			msg := action.Error
			if msg == "" {
				msg = action.Reason
			}
			if msg == "" {
				msg = "resolve failed"
			}
			return nil, nil, nil, ResolveState{}, errors.New(msg)
		default:
			return nil, nil, nil, ResolveState{}, fmt.Errorf("unknown resolve action %q", action.Action)
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
	// 工具失败时条目为合成的失败证据，容忍继续

	state.Tasks = append(state.Tasks, task)
	state.Evidence = append(state.Evidence, *item)
	state.Round++
	return nil
}

// 执行任务并为证据发放编号与创建时间，定位与调查阶段共用
//
// 进度分两段：执行前打印工具与目的（实时反馈，长任务不静默）；
// 执行后补一行工具返回的命令视图（如参数列表），便于排查模型生成的命令是否合理
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
		// 整次运行被取消才传播；其余工具失败透传给调用方决策
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

// 校验并物化提交的目标：节点编号必须在问题内，证据编号必须是本阶段已登记编号
// 目标编号与创建时间由工厂在编排边界生成
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
