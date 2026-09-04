// 会话总控：实现会话应答器，在基线回复与正式诊断之间做有限动作决策
//
// 支持直接回复、调工具、升格诊断：
// - 直接回复：自然语言，模式为基线，无诊断运行
// - 调工具：经调度器取观察（任务无运行编号），回喂后再决策，不落会话消息
// - 升格：编号工厂建运行，经执行器走诊断管道，成功后写入诊断账本
//
// 轮内工具中间态仅在本轮内存；调工具依赖可选调度器，为空时禁止该动作
// 业务级重试：单次决策最多三次；工具失败不计入业务重试，观察回喂后再决策
package agent

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/llm"
	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/tools"
)

//go:embed prompts/tower.md
var towerPromptTemplate string

const (
	// 单次决策业务级重试上限（非法动作、空正文、非法工具等）
	maxTowerAttempts = 3
	// 基线工具环默认上限：防死环熔断，须够用复杂多步观察；触顶后自动升格，禁止对用户报预算错误
	defaultBaselineMaxToolRounds = 12
	// 本轮全部观察的原始输出注入合计预算（自上下文总预算分出）
	// 多条共享、优先保新；轮内内存仍全量保留原始输出；禁止用固定字数当业务能力墙
	defaultTowerObservationsBudgetTokens = 8_000

	towerActionReply    = "reply"
	towerActionCallTool = "call_tool"
	towerActionEscalate = "escalate"
)

// 会话总控：实现会话应答器
// 有限动作：直接回复、调工具、升格诊断
// 不持有跨轮可变状态；每次应答独立决策
// 轮内观察索引可选：有则基线工具观察写入 evidenceId，供 evidence.read 切片
type TowerResponder struct {
	// 大模型客户端，用于结构化决策
	client llm.Client
	// 领域编号工厂，升格建运行与基线任务编号
	factory *core.Factory
	// 正式诊断执行器，升格时调用
	executor session.RunExecutor
	// 正式诊断结果账本；升格成功路径写入
	ledger session.RunLedger
	// 基线工具环调度器；为空时不允许调工具
	dispatcher *tools.Dispatcher
	// 注入提示词的工具规格快照（可空切片）
	specs []tools.ToolSpec
	// 已渲染系统提示（含工具规格摘要）
	systemPrompt string
	// 本轮最多调工具次数；触顶自动升格
	baselineMaxToolRounds int
	// 可选进度与调试输出（默认丢弃）；详细模式时命令行传入标准错误
	progress io.Writer
	// 轮内观察索引；非空时 Put 成功观察并在 Respond 返回前 Discard
	obsIndex *tools.ObservationIndex
	// 记忆组装方法：ours 产品默认（tier-aware）/ D1 / D2 实验臂；空值按 ours
	memoryMethod MemoryMethod
	// 记忆组装参数（D1 条数等；预算与窗口用包内默认，不进 config）
	memoryOpts memoryOptions
	// 最近一轮 Respond 的记忆观测量（只读统计，探针实验记录消费；不影响行为）
	lastMemStats MemoryTurnStats
}

// 单轮记忆组装与分层检索的观测统计（只读副本；评测侧消费，不影响应答行为）
// 定位层口径与回灌实现一致：λ₁ 命中（含未回灌的命中）/ λ₂ 兜底 / 无命中；
// D1 / D2 实验臂无分层检索，恒为 none
type MemoryTurnStats struct {
	// 记忆方法规范名（ours / d1-last-n / d2-flat-summary）
	Method string
	// 本轮定位命中层：lambda1 / lambda2 / none
	LocateLayer string
	// λ₂ 是否实际调用（层为 lambda1 时恒 false）
	Lambda2Called bool
	// 回灌注入的对话消息条数
	RehydratedMsgs int
	// 回灌注入的合成证据条数（mode=evidence）
	RehydratedEvidence int
	// 本轮注入视图时的历史消息条数
	HistTurns int
}

// LastMemoryStats 返回最近一轮 Respond 的记忆观测量（只读副本）
// 单线程轮次约定下读，跨轮由调用方自持锁；评测侧在探针轮后立即读取
func (t *TowerResponder) LastMemoryStats() MemoryTurnStats {
	return t.lastMemStats
}

// 组装总控；客户端、编号工厂、执行器、账本必填，调度器与工具规格可选
// 调度器为空时校验拒绝调工具，便于无工具单测
// 工具规格为空时按空列表处理；构造时复制切片，调用方后续修改不影响本实例
// obsIndex 可选：非空时基线成功观察写入索引并在本轮 Respond 结束时 Discard
func NewTowerResponder(
	client llm.Client,
	factory *core.Factory,
	executor session.RunExecutor,
	ledger session.RunLedger,
	dispatcher *tools.Dispatcher,
	specs []tools.ToolSpec,
	obsIndex *tools.ObservationIndex,
) (*TowerResponder, error) {
	if client == nil {
		return nil, errors.New("tower requires an llm client")
	}
	if factory == nil {
		return nil, errors.New("tower requires a factory")
	}
	if executor == nil {
		return nil, errors.New("tower requires a run executor")
	}
	if ledger == nil {
		return nil, errors.New("tower requires a run ledger")
	}
	copied := make([]tools.ToolSpec, len(specs))
	copy(copied, specs)
	for i := range copied {
		if i < len(specs) {
			copied[i].InputSchema = append(json.RawMessage(nil), specs[i].InputSchema...)
		}
	}
	systemPrompt, err := buildTowerSystemPrompt(towerPromptTemplate, copied)
	if err != nil {
		return nil, err
	}
	return &TowerResponder{
		client:                client,
		factory:               factory,
		executor:              executor,
		ledger:                ledger,
		dispatcher:            dispatcher,
		specs:                 copied,
		systemPrompt:          systemPrompt,
		baselineMaxToolRounds: defaultBaselineMaxToolRounds,
		progress:              io.Discard,
		obsIndex:              obsIndex,
		memoryMethod:          MemoryMethodOurs,
	}, nil
}

// 覆盖基线工具环预算；非正数时恢复默认值
func (t *TowerResponder) SetBaselineMaxToolRounds(n int) {
	if n <= 0 {
		t.baselineMaxToolRounds = defaultBaselineMaxToolRounds
		return
	}
	t.baselineMaxToolRounds = n
}

// 配置记忆组装方法（agent.memory.method）：解析失败报错，由装配层启动拦截
// lastN 为 D1 保留条数（非正走默认）；ours / D2 不读
func (t *TowerResponder) SetMemoryMethod(method string, lastN int) error {
	m, err := ParseMemoryMethod(method)
	if err != nil {
		return err
	}
	t.memoryMethod = m
	t.memoryOpts.lastN = lastN
	return nil
}

// 设置进度与调试输出；空时回退丢弃
func (t *TowerResponder) SetProgress(w io.Writer) {
	if t == nil {
		return
	}
	if w == nil {
		t.progress = io.Discard
		return
	}
	t.progress = w
}

// 写一行进度；默认无输出
func (t *TowerResponder) progressf(format string, args ...any) {
	if t == nil || t.progress == nil {
		return
	}
	fmt.Fprintf(t.progress, format+"\n", args...)
}

// 看历史与当前句，在直接回复、调工具、升格诊断间决策；写库由会话服务负责
// 调工具在本方法内循环执行，中间观察不落会话消息
// 入口准备上下文一次；工具环复用同一视图
// 每轮最多一次轻量集群资源侦察（上下文用，非正式判决证据）；失败降级为空
// 直接回复或升格时带回检查点正文，供会话轮次落检查点消息
func (t *TowerResponder) Respond(ctx context.Context, in session.RespondInput) (session.RespondOutput, error) {
	if err := ctx.Err(); err != nil {
		return session.RespondOutput{}, fmt.Errorf("tower respond: %w", err)
	}
	if t == nil {
		return session.RespondOutput{}, errors.New("tower responder is nil")
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return session.RespondOutput{}, errors.New("tower requires a session id")
	}
	if strings.TrimSpace(in.UserText) == "" {
		return session.RespondOutput{}, errors.New("tower requires user text")
	}

	t.progressf("tower: session=%s history=%d user_chars=%d", in.SessionID, len(in.History), len(in.UserText))
	// 轮首重置记忆观测量：本轮任何路径（含挂起恢复）都有可读统计
	t.lastMemStats = MemoryTurnStats{Method: string(t.memoryMethod), LocateLayer: "none", HistTurns: len(in.History)}

	// 本轮 Put 进索引的 evidenceId；Respond 任一出口 Discard，避免跨轮泄漏
	var putEvidenceIDs []string
	defer func() {
		if t.obsIndex != nil && len(putEvidenceIDs) > 0 {
			t.obsIndex.Discard(putEvidenceIDs)
		}
	}()

	// 挂起恢复优先：会话内有 waiting_user 的 run 时，本轮用户原文作澄清答复，不经动作决策
	if runner, ok := t.executor.(session.SuspendedRunner); ok {
		if runID := runner.FindSuspended(in.SessionID); runID != "" {
			t.progressf("tower: resume suspended run=%s", runID)
			out, resumeErr := session.Resume(ctx, runner, t.ledger, in.SessionID, runID, in.UserText)
			if resumeErr != nil {
				return session.RespondOutput{}, resumeErr
			}
			return out, nil
		}
	}

	// 本会话正式诊断记录；权威源为诊断账本，非消息摘要
	records, listErr := t.ledger.ListBySession(ctx, in.SessionID)
	if listErr != nil {
		return session.RespondOutput{}, fmt.Errorf("tower list diagnostic runs: %w", listErr)
	}

	// 记忆组装按方法分派：ours = tier-aware（R 卡片锁定常驻 + W 窗口 + C 压缩）
	// D1 / D2 为纯记忆策略实验臂——无卡片无回灌（对照口径：回灌与卡片归 ours 组件）
	var view towerContextView
	var priorRuns []towerPriorRunDetail
	var rehydrated []rehydratedMsg
	switch t.memoryMethod {
	case MemoryMethodD1LastN:
		view = assembleLastN(in.History, t.memoryOpts.lastN)
	case MemoryMethodD2FlatSummary:
		v, asmErr := assembleFlatSummary(ctx, t.client, in.History, 0)
		if asmErr != nil {
			return session.RespondOutput{}, fmt.Errorf("tower memory assemble: %w", asmErr)
		}
		view = v
	default:
		v, cards, asmErr := assembleTieredView(ctx, t.client, in.History, records, t.memoryOpts)
		if asmErr != nil {
			return session.RespondOutput{}, fmt.Errorf("tower memory assemble: %w", asmErr)
		}
		view = v
		priorRuns = cards
		// 分层检索回灌（仅 ours）：λ₁ 确定性寻址每轮必跑，λ₁ 空且压缩丢细节时
		// λ₂ 大模型兜底；命中窗与证据预览压进子预算，作为回灌消息注入
		var locStats LocateStats
		rehydrated, locStats = rehydrateLayered(ctx, t.client, in.UserText, in.History, records, view)
		// 回填定位层与回灌条目数：合成证据条目按 mode 标记区分对话消息
		t.lastMemStats.LocateLayer = "none"
		if locStats.MsgHits+locStats.EvidenceHits > 0 {
			t.lastMemStats.LocateLayer = "lambda1"
		} else if locStats.Lambda2Called {
			t.lastMemStats.LocateLayer = "lambda2"
		}
		t.lastMemStats.Lambda2Called = locStats.Lambda2Called
		for _, m := range rehydrated {
			if m.Mode == rehydratedModeEvidence {
				t.lastMemStats.RehydratedEvidence++
			} else {
				t.lastMemStats.RehydratedMsgs++
			}
		}
		if len(rehydrated) > 0 {
			t.progressf("tower: rehydrated=%d", len(rehydrated))
		}
	}
	t.progressf("tower: memory=%s hist=%d checkpoint=%v prior_cards=%d",
		t.memoryMethod, len(view.Hist), view.CheckpointContent != "", len(priorRuns))

	// 每轮一次；与正式管道侦察同源解析；无运行编号，不进证据账本
	clusterResources := t.fetchBaselineClusterResources(ctx)
	t.progressf("tower: cluster_resources=%d", len(clusterResources))

	var observations []towerObservation
	toolRounds := 0

	for {
		if err := ctx.Err(); err != nil {
			return session.RespondOutput{}, fmt.Errorf("tower respond: %w", err)
		}

		t.progressf("tower: decide obs=%d tool_rounds=%d", len(observations), toolRounds)
		decision, err := t.decide(ctx, in, view, priorRuns, observations, clusterResources, rehydrated)
		if err != nil {
			return session.RespondOutput{}, err
		}
		t.progressf("tower: action=%s", decision.Action)

		switch decision.Action {
		case towerActionReply:
			return session.RespondOutput{
				Content:           decision.Content,
				Mode:              session.ModeBaseline,
				CheckpointContent: view.CheckpointContent,
			}, nil

		case towerActionCallTool:
			if toolRounds >= t.baselineMaxToolRounds {
				// 防死环触顶：升格正式诊断，用户仍拿结果；不暴露内部轮次错误（#18）
				t.progressf("tower: tool budget exhausted, escalate")
				out, escErr := session.Escalate(ctx, t.factory, t.executor, t.ledger, in.SessionID, in.UserText)
				if escErr != nil {
					return session.RespondOutput{}, escErr
				}
				out.CheckpointContent = view.CheckpointContent
				return out, nil
			}
			t.progressf("tower: call_tool %s", decision.ToolCall.ToolName)
			obs, execErr := t.executeBaselineTool(ctx, decision.ToolCall)
			if execErr != nil {
				return session.RespondOutput{}, execErr
			}
			if obs.EvidenceID != "" {
				putEvidenceIDs = append(putEvidenceIDs, obs.EvidenceID)
			}
			observations = append(observations, obs)
			toolRounds++

		case towerActionEscalate:
			question := strings.TrimSpace(decision.Question)
			if question == "" {
				question = in.UserText
			}
			t.progressf("tower: escalate question_chars=%d", len(question))
			out, escErr := session.Escalate(ctx, t.factory, t.executor, t.ledger, in.SessionID, question)
			if escErr != nil {
				return session.RespondOutput{}, escErr
			}
			out.CheckpointContent = view.CheckpointContent
			return out, nil

		default:
			return session.RespondOutput{}, fmt.Errorf("tower: unknown action %q", decision.Action)
		}
	}
}

// 单轮决策结果（校验后）
type towerDecision struct {
	// 动作：直接回复、调工具或升格诊断
	Action string
	// 直接回复时的助手正文，其它动作可空
	Content string
	// 升格时写入运行的诊断问题；空则回退为用户原文
	Question string
	// 调工具时的工具提议
	ToolCall towerToolCall
}

// 模型提议的一次工具调用（恰好一条）
type towerToolCall struct {
	// 白名单工具名，须在工具规格内
	ToolName string
	// 工具参数对象；空则执行时用空对象
	Arguments json.RawMessage
	// 调用目的说明，写入任务与观察
	Purpose string
}

// 轮内工具观察，仅进程内存，对齐证据可回放子集
// 内存侧原始输出全量；注入模型前按预算截断
type towerObservation struct {
	// 本轮任务编号
	TaskID string `json:"taskId"`
	// 实际工具名
	ToolName string `json:"toolName"`
	// 调用目的
	Purpose string `json:"purpose,omitempty"`
	// 成功时的摘要
	Summary string `json:"summary,omitempty"`
	// 可展示的命令视图
	CommandView string `json:"commandView,omitempty"`
	// 工具或策略失败时非空
	Error string `json:"error,omitempty"`
	// 工具原始输出全量拷贝（集群工具含标准输出、标准错误、退出码等）
	Raw json.RawMessage `json:"raw,omitempty"`
	// 仅当注入副本对原始输出做了预算截断时为真；权威内存观察不置位
	RawTruncated bool `json:"rawTruncated,omitempty"`
	// 轮内观察索引编号（e_…）；有索引且成功写出 Raw 时设置，供 evidence.read
	EvidenceID string `json:"evidenceId,omitempty"`
}

// 模型每轮输出契约（结构化生成反序列化目标）
type towerLLMOutput struct {
	// 动作：直接回复、调工具或升格诊断
	Action string `json:"action"`
	// 直接回复时的助手正文
	Content string `json:"content"`
	// 升格时的诊断问题
	Question string `json:"question"`
	// 调工具时的工具字段
	ToolCall *towerToolCallJSON `json:"tool_call"`
}

// 模型工具调用结构化形状
type towerToolCallJSON struct {
	// 工具名
	ToolName string `json:"tool_name"`
	// 参数对象；允许任意结构，校验时要求对象类型
	Arguments json.RawMessage `json:"arguments"`
	// 目的说明
	Purpose string `json:"purpose"`
}

// 调用模型直至得到合法决策或业务重试耗尽
// 先前运行材料为本会话诊断账本的 R 层索引卡；集群资源为本轮轻量侦察（可空）
// 回灌为压缩后按范围回灌的原文窗（可空）；三者仅注入提示词，不写入观察账本
func (t *TowerResponder) decide(
	ctx context.Context,
	in session.RespondInput,
	view towerContextView,
	priorRuns []towerPriorRunDetail,
	observations []towerObservation,
	clusterResources []ClusterResource,
	rehydrated []rehydratedMsg,
) (towerDecision, error) {
	userPayload, err := buildTowerUserPayload(in, view, priorRuns, observations, t.specs, clusterResources, rehydrated)
	if err != nil {
		return towerDecision{}, fmt.Errorf("tower payload: %w", err)
	}
	req := llm.Request{
		System: t.systemPrompt,
		User:   userPayload,
	}

	var lastOut towerLLMOutput
	var lastValidateErr error
	for attempt := 0; attempt < maxTowerAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return towerDecision{}, fmt.Errorf("tower decide: %w", err)
		}
		var out towerLLMOutput
		if gErr := t.client.GenerateJSON(ctx, req, &out); gErr != nil {
			// 空正文与结构化解析失败可业务重试（兼容网关偶发坏包）；其它错误直接失败
			if errors.Is(gErr, llm.ErrJSONParse) || errors.Is(gErr, llm.ErrEmptyResponse) {
				lastValidateErr = gErr
				t.progressf("tower: decide attempt %d/%d: recoverable LLM error: %v",
					attempt+1, maxTowerAttempts, gErr)
				continue
			}
			return towerDecision{}, fmt.Errorf("tower decide with LLM: %w", gErr)
		}
		if vErr := t.validateTowerDecision(out); vErr != nil {
			lastOut = out
			lastValidateErr = vErr
			t.progressf("tower: decide attempt %d/%d: invalid decision: %v",
				attempt+1, maxTowerAttempts, vErr)
			continue
		}
		return t.mapTowerDecision(out), nil
	}
	if lastValidateErr != nil {
		// 双错误包装：上层可同时识别模型输出不一致与解析失败或空响应
		return towerDecision{}, fmt.Errorf("%w: last error: %w, last output: %+v",
			ErrLLMOutputInconsistent, lastValidateErr, lastOut)
	}
	return towerDecision{}, fmt.Errorf("%w: last output: %+v", ErrLLMOutputInconsistent, lastOut)
}

// 校验决策：依赖本实例的调度器与工具规格
func (t *TowerResponder) validateTowerDecision(out towerLLMOutput) error {
	action := strings.TrimSpace(strings.ToLower(out.Action))
	switch action {
	case towerActionReply:
		if strings.TrimSpace(out.Content) == "" {
			return errors.New("reply requires non-empty content")
		}
		return nil
	case towerActionEscalate:
		return nil
	case towerActionCallTool:
		if t.dispatcher == nil {
			return errors.New("call_tool requires a dispatcher")
		}
		if out.ToolCall == nil {
			return errors.New("call_tool requires tool_call")
		}
		name := strings.TrimSpace(out.ToolCall.ToolName)
		if name == "" {
			return errors.New("call_tool requires tool_name")
		}
		if !towerToolNameAllowed(name, t.specs) {
			return fmt.Errorf("unknown tool %q", name)
		}
		if err := validateTowerToolArguments(out.ToolCall.Arguments); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("invalid action %q", out.Action)
	}
}

// 规范化并映射为内部决策
func (t *TowerResponder) mapTowerDecision(out towerLLMOutput) towerDecision {
	decision := towerDecision{
		Action:   strings.TrimSpace(strings.ToLower(out.Action)),
		Content:  strings.TrimSpace(out.Content),
		Question: strings.TrimSpace(out.Question),
	}
	if decision.Action == towerActionCallTool && out.ToolCall != nil {
		args := out.ToolCall.Arguments
		if len(bytes.TrimSpace(args)) == 0 {
			args = json.RawMessage(`{}`)
		}
		decision.ToolCall = towerToolCall{
			ToolName:  strings.TrimSpace(out.ToolCall.ToolName),
			Arguments: args,
			Purpose:   strings.TrimSpace(out.ToolCall.Purpose),
		}
	}
	return decision
}

// 轻量集群资源类型侦察：每轮由应答调用至多一次
// 条件：调度器非空且工具规格含集群工具；否则返回空（不尝试）
// 成功：解析资源清单；失败或空输出返回空，不挡基线
// 任务无运行编号；结果仅作模型上下文，不得当判决证据
func (t *TowerResponder) fetchBaselineClusterResources(ctx context.Context) []ClusterResource {
	if t == nil || t.dispatcher == nil || t.factory == nil {
		return nil
	}
	if !towerToolNameAllowed("k8s", t.specs) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return nil
	}

	taskID, idErr := t.factory.NewID("t")
	if idErr != nil || taskID == "" {
		return nil
	}
	task := core.Task{
		ID:        taskID,
		RunID:     "",
		ToolName:  "k8s",
		Arguments: json.RawMessage(`{"argv":["api-resources"]}`),
		Purpose:   "侦察集群可用资源类型（基线 context）",
	}
	item, execErr := t.dispatcher.Execute(ctx, task)
	if execErr != nil || item == nil {
		return nil
	}
	resources := parseAPIResources(extractStdout(item.Raw))
	if len(resources) == 0 {
		return nil
	}
	return resources
}

// 经调度器执行一次基线工具调用，成功或失败都返回观察
// 仅在无法发号或上下文取消时返回错误中断环
func (t *TowerResponder) executeBaselineTool(ctx context.Context, call towerToolCall) (towerObservation, error) {
	obs := towerObservation{
		ToolName: call.ToolName,
		Purpose:  call.Purpose,
	}
	if t.dispatcher == nil {
		obs.Error = "dispatcher is not configured"
		return obs, nil
	}

	taskID, idErr := t.factory.NewID("t")
	if idErr != nil {
		return obs, fmt.Errorf("tower: create task id: %w", idErr)
	}
	obs.TaskID = taskID

	args := call.Arguments
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	task := core.Task{
		ID:        taskID,
		RunID:     "",
		ToolName:  call.ToolName,
		Arguments: args,
		Purpose:   call.Purpose,
	}

	item, execErr := t.dispatcher.Execute(ctx, task)
	if execErr != nil {
		if ctx.Err() != nil {
			return obs, fmt.Errorf("tower: execute tool %q: %w", call.ToolName, ctx.Err())
		}
		obs.Error = execErr.Error()
		obs.Summary = "工具执行失败"
		return obs, nil
	}
	if item == nil {
		obs.Error = "tool returned nil evidence"
		obs.Summary = "工具执行失败"
		return obs, nil
	}

	// 调度器已写入归属字段；观察取可回放子集，含完整原始输出（内存不阉割）
	obs.Summary = item.Summary
	obs.CommandView = item.CommandView
	if item.Error != "" {
		obs.Error = item.Error
	}
	if len(item.Raw) > 0 {
		obs.Raw = append(json.RawMessage(nil), item.Raw...)
	}
	// 有索引且写出了 Raw 时分配 e_ 编号并 Put，供本轮 evidence.read；导航工具结果不 Put
	if t.obsIndex != nil && len(obs.Raw) > 0 && call.ToolName != "evidence.read" {
		if eid, idErr := t.factory.NewID("e"); idErr == nil {
			obs.EvidenceID = eid
			t.obsIndex.Put(eid, tools.ObsRecord{Raw: obs.Raw, ToolName: call.ToolName})
		}
	}
	return obs, nil
}

// 生成写入提示词的观察副本，不修改环内权威切片
// 全部原始输出共享一份预算；从最新向旧分配，优先保留较新观察的全文
// 预算非正时使用默认合计预算
func prepareTowerObservationsForPrompt(obs []towerObservation, budgetTokens int) []towerObservation {
	if len(obs) == 0 {
		return obs
	}
	if budgetTokens <= 0 {
		budgetTokens = defaultTowerObservationsBudgetTokens
	}

	// 深拷贝原始输出，避免后续截断写穿权威切片
	out := make([]towerObservation, len(obs))
	for i, o := range obs {
		out[i] = o
		out[i].RawTruncated = false
		if len(o.Raw) == 0 {
			out[i].Raw = nil
			continue
		}
		out[i].Raw = append(json.RawMessage(nil), o.Raw...)
	}

	// 从尾部（最新）向前扣减；够则全文，不够则截断预览，耗尽则占位
	remaining := budgetTokens
	for i := len(out) - 1; i >= 0; i-- {
		if len(out[i].Raw) == 0 {
			continue
		}
		cost := estimateTokens(string(out[i].Raw))
		if cost <= remaining {
			remaining -= cost
			continue
		}
		if remaining > 0 {
			out[i].Raw = truncateObservationRaw(out[i].Raw, remaining)
			out[i].RawTruncated = true
			remaining = 0
			continue
		}
		out[i].Raw = omitObservationRawForBudget()
		out[i].RawTruncated = true
	}
	return out
}

// 将超预算原始输出收成合法对象，附截断预览；完整结果仍在轮内内存
// 预算按估算单位换算为预览字符上限
func truncateObservationRaw(raw json.RawMessage, budgetTokens int) json.RawMessage {
	runes := []rune(string(raw))
	maxRunes := budgetTokens * 4
	if maxRunes <= 0 {
		maxRunes = 200
	}
	preview := string(runes)
	shown := len(runes)
	if len(runes) > maxRunes {
		preview = string(runes[:maxRunes])
		shown = maxRunes
	}
	wrapped, err := json.Marshal(struct {
		Truncated bool   `json:"truncated"`
		Preview   string `json:"preview"`
		Note      string `json:"note"`
	}{
		Truncated: true,
		Preview:   preview,
		Note: fmt.Sprintf(
			"truncated for model budget; full result retained in-turn; shown %d/%d runes",
			shown, len(runes),
		),
	})
	if err != nil {
		return json.RawMessage(`{"truncated":true,"preview":"","note":"truncate marshal failed"}`)
	}
	return wrapped
}

// 共享预算已耗尽时写入的原始输出占位对象
// 摘要与命令视图仍注入模型，完整原始输出仍在轮内内存
func omitObservationRawForBudget() json.RawMessage {
	return json.RawMessage(
		`{"truncated":true,"preview":"","note":"omitted for shared model budget; newer observations prioritized; full result retained in-turn"}`,
	)
}

// 将提示词中的工具规格占位符替换为名称与描述摘要（截断参数模式，避免撑爆上下文）
func buildTowerSystemPrompt(template string, specs []tools.ToolSpec) (string, error) {
	if !strings.Contains(template, "{{TOOL_SPECS}}") {
		return "", errors.New("tower prompt missing {{TOOL_SPECS}} placeholder")
	}
	type toolView struct {
		// 工具名
		Name string `json:"name"`
		// 能力描述
		Description string `json:"description"`
	}
	views := make([]toolView, 0, len(specs))
	for _, s := range specs {
		views = append(views, toolView{Name: s.Name, Description: s.Description})
	}
	raw, err := json.MarshalIndent(views, "", "  ")
	if err != nil {
		return "", fmt.Errorf("tower: marshal tool specs: %w", err)
	}
	return strings.Replace(template, "{{TOOL_SPECS}}", string(raw), 1), nil
}

// 组装用户载荷：当前句、历史视图、先前诊断摘要与索引卡、本轮观察、工具列表、可选集群资源、可选回灌窗
// 禁止按固定条数静默截断；历史超预算见记忆组装器；
// 观察原始输出见预算治理；索引卡不带 raw（深细节归回灌与 evidence.read）；
// 回灌窗仅 ours 注入（消息原文 + 证据预览条目；D1/D2 实验臂不注入）
func buildTowerUserPayload(
	in session.RespondInput,
	view towerContextView,
	priorRuns []towerPriorRunDetail,
	observations []towerObservation,
	specs []tools.ToolSpec,
	clusterResources []ClusterResource,
	rehydrated []rehydratedMsg,
) (string, error) {
	type toolItem struct {
		// 工具名
		Name string `json:"name"`
		// 描述
		Description string `json:"description"`
	}
	type payload struct {
		// 本轮用户原文
		UserText string `json:"user_text"`
		// 会话历史视图（预算内全文；超预算分层压缩）
		History []towerHistMsg `json:"history"`
		// 本会话既有诊断摘要（消息侧；无条数上限，由预算统一治理）
		PriorDiagnostics []towerPriorDiagnostic `json:"prior_diagnostics"`
		// 本会话正式诊断索引卡（诊断账本：结论加证据卡面；不带 raw）
		PriorRunDetails []towerPriorRunDetail `json:"prior_run_details"`
		// 本轮工具观察（原始输出经预算治理后的注入副本）
		Observations []towerObservation `json:"observations"`
		// 可用工具名与描述
		Tools []toolItem `json:"tools"`
		// 本集群实际可用资源类型（含自定义资源）；基线上下文，非正式证据
		ClusterResources []ClusterResource `json:"cluster_resources,omitempty"`
		// 压缩后分层检索回灌的原文窗（λ₁ 锚定或 λ₂ 兜底命中时存在；含证据原文预览条目）
		// 步骤级细节优先依据本字段，不得编造未出现的步骤
		RehydratedMessages []rehydratedMsg `json:"rehydrated_messages,omitempty"`
	}

	hist := view.Hist
	priors := view.Priors
	toolItems := make([]toolItem, 0, len(specs))
	for _, s := range specs {
		toolItems = append(toolItems, toolItem{Name: s.Name, Description: s.Description})
	}
	if observations == nil {
		observations = []towerObservation{}
	}
	// 注入副本：多观察共享预算、优先保新；超预算带截断标记（权威切片不变）
	observations = prepareTowerObservationsForPrompt(
		observations, defaultTowerObservationsBudgetTokens)
	if priors == nil {
		priors = []towerPriorDiagnostic{}
	}
	if priorRuns == nil {
		priorRuns = []towerPriorRunDetail{}
	}
	if hist == nil {
		hist = []towerHistMsg{}
	}
	if len(clusterResources) == 0 {
		clusterResources = nil
	}
	if len(rehydrated) == 0 {
		rehydrated = nil
	}

	raw, err := json.Marshal(payload{
		UserText:           in.UserText,
		History:            hist,
		PriorDiagnostics:   priors,
		PriorRunDetails:    priorRuns,
		Observations:       observations,
		Tools:              toolItems,
		ClusterResources:   clusterResources,
		RehydratedMessages: rehydrated,
	})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// 检查工具名是否在规格白名单
func towerToolNameAllowed(name string, specs []tools.ToolSpec) bool {
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}

// 参数须为对象或空（空表示空对象）
func validateTowerToolArguments(args json.RawMessage) error {
	if len(args) == 0 {
		return nil
	}
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] != '{' {
		return errors.New("tool_call.arguments must be a JSON object")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return fmt.Errorf("tool_call.arguments: %w", err)
	}
	return nil
}
