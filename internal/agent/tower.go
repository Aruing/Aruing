// 会话总控：实现 session.Responder，在基线回复与正式诊断之间做有限动作决策
//
// 支持 reply / call_tool / escalate：
// - reply：直接自然语言，Mode=baseline，无 Run
// - call_tool：经 Dispatcher 取观察（Task.RunID 空），回喂后再决策，不落 Message
// - escalate：Factory 建 Run，经 RunExecutor 走诊断管道
//
// 轮内 tool 中间态仅在本轮内存；call_tool 依赖可选 Dispatcher，nil 时禁止该动作
// 业务级重试：单次决策最多 3 次；工具失败不计入业务重试，观察回喂后再决策
package agent

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"aruing/internal/core"
	"aruing/internal/llm"
	"aruing/internal/session"
	"aruing/internal/tools"
)

//go:embed prompts/tower.md
var towerPromptTemplate string

const (
	// 单次决策业务级重试上限（非法 action / 空 content / 非法 tool 等）
	maxTowerAttempts = 3
	// 基线 tool 环默认最多执行次数；超出则硬错误（单轮熔断，非会话记忆上限，#18）
	defaultBaselineMaxToolRounds = 4
	// 本轮全部 observations 的 raw 注入合计预算（自上下文总预算分出）
	// 多条共享、优先保新；轮内内存仍全量 Evidence.Raw；禁止用固定字数当业务能力墙（#18）
	defaultTowerObservationsBudgetTokens = 8_000

	towerActionReply    = "reply"
	towerActionCallTool = "call_tool"
	towerActionEscalate = "escalate"
)

// 会话总控：实现 session.Responder
// 有限动作：reply / call_tool / escalate
// 不持有跨 Turn 可变状态；每次 Respond 独立决策
type TowerResponder struct {
	// 大模型客户端，用于结构化决策（GenerateJSON）
	client llm.Client
	// 领域编号工厂，升格建 Run 与基线 Task 编号
	factory *core.Factory
	// 正式诊断执行器，escalate 时调用
	executor session.RunExecutor
	// 基线 tool 环调度器；nil 时不允许 call_tool
	dispatcher *tools.Dispatcher
	// 注入 prompt 的工具规格快照（可空切片）
	specs []tools.ToolSpec
	// 已渲染系统提示（含工具规格摘要）
	systemPrompt string
	// 本轮最多 call_tool 次数，默认 4
	baselineMaxToolRounds int
}

// 组装总控；dispatcher 与 specs 可选
// dispatcher == nil 时校验拒绝 call_tool，便于无工具单测
// specs 为 nil 时按空列表处理；构造时复制切片，调用方后续修改不影响本实例
func NewTowerResponder(
	client llm.Client,
	factory *core.Factory,
	executor session.RunExecutor,
	dispatcher *tools.Dispatcher,
	specs []tools.ToolSpec,
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
		dispatcher:            dispatcher,
		specs:                 copied,
		systemPrompt:          systemPrompt,
		baselineMaxToolRounds: defaultBaselineMaxToolRounds,
	}, nil
}

// 覆盖基线 tool 环预算；n <= 0 时恢复默认值
func (t *TowerResponder) SetBaselineMaxToolRounds(n int) {
	if n <= 0 {
		t.baselineMaxToolRounds = defaultBaselineMaxToolRounds
		return
	}
	t.baselineMaxToolRounds = n
}

// 看历史与当前句，在 reply / call_tool / escalate 间决策；写库由 session.Service.Turn 负责
// call_tool 在本方法内循环执行，中间观察不落 Message
// 入口 prepareTowerContext 一次（L0/L1/L2）；tool 环复用同一视图
// 每个 Turn 最多一次轻量集群资源侦察（context，非 Verdict 证据）；失败降级为空
// reply / escalate 时把 view.CheckpointContent 带回，供 Turn 落 ModeCheckpoint
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

	view, err := prepareTowerContext(
		ctx,
		t.client,
		in.History,
		defaultTowerContextBudgetTokens,
		defaultMaxMessageContentTokens,
		defaultTruncatedPreviewTokens,
	)
	if err != nil {
		return session.RespondOutput{}, fmt.Errorf("tower context: %w", err)
	}

	// 每 Turn 一次；与正式管道 recon 同源解析；空 RunID，不进 Evidence 账本
	clusterResources := t.fetchBaselineClusterResources(ctx)

	var observations []towerObservation
	toolRounds := 0

	for {
		if err := ctx.Err(); err != nil {
			return session.RespondOutput{}, fmt.Errorf("tower respond: %w", err)
		}

		decision, err := t.decide(ctx, in, view, observations, clusterResources)
		if err != nil {
			return session.RespondOutput{}, err
		}

		switch decision.Action {
		case towerActionReply:
			return session.RespondOutput{
				Content:           decision.Content,
				Mode:              session.ModeBaseline,
				CheckpointContent: view.CheckpointContent,
			}, nil

		case towerActionCallTool:
			if toolRounds >= t.baselineMaxToolRounds {
				return session.RespondOutput{}, fmt.Errorf(
					"tower: baseline tool budget exhausted (%d rounds)", t.baselineMaxToolRounds)
			}
			obs, execErr := t.executeBaselineTool(ctx, decision.ToolCall)
			if execErr != nil {
				return session.RespondOutput{}, execErr
			}
			observations = append(observations, obs)
			toolRounds++

		case towerActionEscalate:
			question := strings.TrimSpace(decision.Question)
			if question == "" {
				question = in.UserText
			}
			out, escErr := session.Escalate(ctx, t.factory, t.executor, in.SessionID, question)
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
	// 动作：reply、call_tool 或 escalate
	Action string
	// reply 时的助手正文，其它动作可空
	Content string
	// escalate 时写入 Run 的诊断问题；空则回退为用户原文
	Question string
	// call_tool 时的工具提议
	ToolCall towerToolCall
}

// 模型提议的一次工具调用（恰好一条）
type towerToolCall struct {
	// 白名单工具名，须在 specs 内
	ToolName string
	// 工具参数 JSON 对象；空则执行时用 {}
	Arguments json.RawMessage
	// 调用目的说明，写入 Task 与观察
	Purpose string
}

// 轮内 tool 观察，仅进程内存，对齐 Evidence 可回放子集
// 内存侧 Raw 全量；注入模型前经 prepareTowerObservationsForPrompt 按预算截断
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
	// 工具 Evidence.Raw 全量拷贝（k8s 含 stdout/stderr/exitCode 等）
	Raw json.RawMessage `json:"raw,omitempty"`
	// 仅当注入副本对 raw 做了预算截断时为 true；权威内存观察不置位
	RawTruncated bool `json:"rawTruncated,omitempty"`
}

// 模型每轮输出契约（GenerateJSON 反序列化目标）
type towerLLMOutput struct {
	// 动作：reply、call_tool 或 escalate
	Action string `json:"action"`
	// reply 时的助手正文
	Content string `json:"content"`
	// escalate 时的诊断问题
	Question string `json:"question"`
	// call_tool 时的工具字段
	ToolCall *towerToolCallJSON `json:"tool_call"`
}

// 模型 tool_call JSON 形状
type towerToolCallJSON struct {
	// 工具名
	ToolName string `json:"tool_name"`
	// 参数对象；允许任意 JSON，校验时要求对象类型
	Arguments json.RawMessage `json:"arguments"`
	// 目的说明
	Purpose string `json:"purpose"`
}

// 调用模型直至得到合法决策或业务重试耗尽
// clusterResources 为本 Turn 轻量侦察结果（可空）；仅注入 prompt，不写入观察账本
func (t *TowerResponder) decide(
	ctx context.Context,
	in session.RespondInput,
	view towerContextView,
	observations []towerObservation,
	clusterResources []ClusterResource,
) (towerDecision, error) {
	userPayload, err := buildTowerUserPayload(in, view, observations, t.specs, clusterResources)
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
			return towerDecision{}, fmt.Errorf("tower decide with LLM: %w", gErr)
		}
		if vErr := t.validateTowerDecision(out); vErr != nil {
			lastOut = out
			lastValidateErr = vErr
			continue
		}
		return t.mapTowerDecision(out), nil
	}
	return towerDecision{}, fmt.Errorf("%w: last error: %v, last output: %+v",
		ErrLLMOutputInconsistent, lastValidateErr, lastOut)
}

// 校验决策：依赖本实例的 dispatcher / specs
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

// 轻量集群资源类型侦察：每 Turn 由 Respond 调用至多一次
// 条件：dispatcher 非空且 specs 含 k8s；否则返回 nil（不尝试）
// 成功：解析 api-resources 为 ClusterResource 清单；失败/空 stdout：nil，不挡基线
// Task.RunID 空；结果仅作模型 context，不得当 Verdict 证据
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

// 经 Dispatcher 执行一次基线工具调用，成功或失败都返回 observation
// 仅在无法发号或 ctx 取消时返回 error 中断环
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

	// Dispatcher 已写入归属字段；观察取可回放子集，含完整 Raw（#18 内存不阉割）
	obs.Summary = item.Summary
	obs.CommandView = item.CommandView
	if item.Error != "" {
		obs.Error = item.Error
	}
	if len(item.Raw) > 0 {
		obs.Raw = append(json.RawMessage(nil), item.Raw...)
	}
	return obs, nil
}

// 生成写入 prompt 的 observations 副本，不修改环内权威切片
// 全部 raw 共享一份预算；从最新向旧分配，优先保留较新观察的全文
// budgetTokens <= 0 时使用默认合计预算
func prepareTowerObservationsForPrompt(obs []towerObservation, budgetTokens int) []towerObservation {
	if len(obs) == 0 {
		return obs
	}
	if budgetTokens <= 0 {
		budgetTokens = defaultTowerObservationsBudgetTokens
	}

	// 深拷贝 raw，避免后续截断写穿权威切片
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

// 将超预算 raw 收成合法 JSON，附 truncated 预览；完整结果仍在轮内内存
// budgetTokens 按估算单位换算为预览 rune 上限（约 4 rune / token）
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

// 共享预算已耗尽时写入的 raw 占位 JSON
// summary 与 commandView 仍注入模型，完整 raw 仍在轮内内存
func omitObservationRawForBudget() json.RawMessage {
	return json.RawMessage(
		`{"truncated":true,"preview":"","note":"omitted for shared model budget; newer observations prioritized; full result retained in-turn"}`,
	)
}

// 将 prompt 中的工具规格占位符替换为名称与描述摘要（截断 schema，避免撑爆上下文）
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

// 组装 user JSON：当前句、已 prepare 的历史视图、prior_diagnostics、本轮观察、工具列表、可选集群资源类型
// 禁止 last-N 静默截断（#18）；历史超预算见 prepareTowerContext；观察 raw 见 prepareTowerObservationsForPrompt
func buildTowerUserPayload(
	in session.RespondInput,
	view towerContextView,
	observations []towerObservation,
	specs []tools.ToolSpec,
	clusterResources []ClusterResource,
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
		// 会话历史视图（预算内全文；超预算 L0/L1/L2 compact）
		History []towerHistMsg `json:"history"`
		// 本会话既有诊断摘要（无条数 cap，由预算统一治理）
		PriorDiagnostics []towerPriorDiagnostic `json:"prior_diagnostics"`
		// 本轮 tool 观察（raw 经预算治理后的注入副本）
		Observations []towerObservation `json:"observations"`
		// 可用工具名与描述
		Tools []toolItem `json:"tools"`
		// 本集群实际可用资源类型（含 CRD）；基线 context，非正式 Evidence
		ClusterResources []ClusterResource `json:"cluster_resources,omitempty"`
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
	// 注入副本：多观察共享预算、优先保新；超预算带 rawTruncated（权威切片不变）
	observations = prepareTowerObservationsForPrompt(
		observations, defaultTowerObservationsBudgetTokens)
	if priors == nil {
		priors = []towerPriorDiagnostic{}
	}
	if hist == nil {
		hist = []towerHistMsg{}
	}
	if len(clusterResources) == 0 {
		clusterResources = nil
	}

	raw, err := json.Marshal(payload{
		UserText:         in.UserText,
		History:          hist,
		PriorDiagnostics: priors,
		Observations:     observations,
		Tools:            toolItems,
		ClusterResources: clusterResources,
	})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// 检查工具名是否在 specs 白名单
func towerToolNameAllowed(name string, specs []tools.ToolSpec) bool {
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}

// arguments 须为 JSON 对象或空（空表示 {}）
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
