// 真实定位器通过大模型提议工具调用或提交已确认目标
//
// 与假定位器共享定位驱动边界：只返回意图，不执行工具、不发放领域编号
// 工具规格来自注册表规格列表注入，禁止在提示词手写第二份工具清单
// 目标内容字段由模型产出，目标编号由编排在提交时发放
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
	"aruing/internal/tools"
)

//go:embed prompts/resolver.md
var resolverPrompt string

// 定位驱动业务级重试次数：结构合法但语义违规时重新请求
const maxResolveAttempts = 3

// 定位阶段注入模型的证据原始输出合计预算（估算词元）
// 环内证据原始输出仍全量；禁止用固定字数当业务墙
const defaultResolverEvidenceBudgetTokens = 8_000

// 用大模型驱动定位循环的实现
//
// 持有不可变依赖（客户端、工具规格快照、提示词），可被多次运行复用
// 不持有跨运行可变状态；每轮下一步独立发结构化生成
type LLMResolver struct {
	// 大模型客户端，每轮下一步发一次结构化生成
	client llm.Client
	// 注册表工具规格快照，用于拼系统提示词与校验工具名
	specs []tools.ToolSpec
	// 已注入工具规格的系统提示词全文，构造后只读
	prompt string
}

// 创建基于大模型的定位驱动
// 规格应为注册表规格列表快照；空列表时仍可构造，但调用工具将无法通过校验
func NewLLMResolver(client llm.Client, specs []tools.ToolSpec) (*LLMResolver, error) {
	if client == nil {
		return nil, errors.New("LLM resolver requires an llm client")
	}
	// 复制规格，避免调用方后续修改注册表快照影响已构造实例
	copied := make([]tools.ToolSpec, len(specs))
	copy(copied, specs)
	for i := range copied {
		copied[i].InputSchema = append(json.RawMessage(nil), specs[i].InputSchema...)
	}
	system, err := buildResolverSystemPrompt(resolverPrompt, copied)
	if err != nil {
		return nil, err
	}
	return &LLMResolver{client: client, specs: copied, prompt: system}, nil
}

// 根据当前定位状态请求模型返回下一动作，并做语义校验与业务重试
func (r *LLMResolver) Next(ctx context.Context, state ResolveState) (ResolveAction, error) {
	if ctx == nil {
		return ResolveAction{}, errors.New("resolver requires a context")
	}
	if err := ctx.Err(); err != nil {
		return ResolveAction{}, fmt.Errorf("resolve next: %w", err)
	}
	if r == nil {
		return ResolveAction{}, errors.New("resolver is required")
	}

	userPayload, err := buildResolverUserPayload(state)
	if err != nil {
		return ResolveAction{}, fmt.Errorf("build resolve prompt: %w", err)
	}

	req := llm.Request{
		System: r.prompt,
		User:   userPayload,
	}

	var lastOut resolverLLMOutput
	var lastValidateErr error
	for attempt := 0; attempt < maxResolveAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ResolveAction{}, fmt.Errorf("resolve next: %w", err)
		}

		var out resolverLLMOutput
		if gErr := r.client.GenerateJSON(ctx, req, &out); gErr != nil {
			return ResolveAction{}, fmt.Errorf("resolve with LLM: %w", gErr)
		}

		action, vErr := mapResolverOutput(out, state, r.specs)
		if vErr != nil {
			lastOut = out
			lastValidateErr = vErr
			continue
		}
		return action, nil
	}

	return ResolveAction{}, fmt.Errorf("%w: last error: %v, last output: %+v",
		ErrLLMOutputInconsistent, lastValidateErr, lastOut)
}

// 将嵌入提示词中的工具规格占位符替换为结构化文本
func buildResolverSystemPrompt(template string, specs []tools.ToolSpec) (string, error) {
	raw, err := json.MarshalIndent(specs, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal tool specs: %w", err)
	}
	if !strings.Contains(template, "{{TOOL_SPECS}}") {
		return "", errors.New("resolver prompt missing {{TOOL_SPECS}} placeholder")
	}
	return strings.Replace(template, "{{TOOL_SPECS}}", string(raw), 1), nil
}

// 序列化回喂状态：证据带摘要与按预算治理的原始预览，避免撑爆上下文
// 权威状态中的证据不修改；完整原始输出仍在定位环内存
func buildResolverUserPayload(state ResolveState) (string, error) {
	// 喂给模型的证据视图
	type evidenceView struct {
		// 证据系统编号
		ID string `json:"id"`
		// 产出该证据的任务编号
		TaskID string `json:"taskId"`
		// 实际调用的工具名
		ToolName string `json:"toolName"`
		// 人类可读摘要
		Summary string `json:"summary"`
		// 工具失败时的错误文案，成功时可空
		Error string `json:"error,omitempty"`
		// 可展示的命令视图（如参数列表拼串）
		CommandView string `json:"commandView,omitempty"`
		// 原始输出预览；预算内全文，超预算截断或占位
		RawPreview string `json:"rawPreview,omitempty"`
		// 注入时对原始输出做了预算截断或占位时为真
		RawTruncated bool `json:"rawTruncated,omitempty"`
	}
	// 喂给模型的本轮已发起任务视图
	type taskView struct {
		// 任务系统编号
		ID string `json:"id"`
		// 工具名
		ToolName string `json:"toolName"`
		// 取证目的说明
		Purpose string `json:"purpose,omitempty"`
		// 关联的节点、目标等通用引用
		Refs []string `json:"refs,omitempty"`
		// 工具入参
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	// 定位轮次完整用户载荷
	type payload struct {
		// 当前问题的开放线索图
		Query core.Query `json:"query"`
		// 本定位阶段已执行过的任务
		Tasks []taskView `json:"tasks"`
		// 本定位阶段已收集的证据摘要
		Evidence []evidenceView `json:"evidence"`
		// 当前轮次（从 1 或 0 起，与编排约定一致）
		Round int `json:"round"`
		// 编排允许的最大定位轮数
		MaxRounds int `json:"maxRounds"`
	}

	previews := prepareResolverRawPreviews(state.Evidence, defaultResolverEvidenceBudgetTokens)
	p := payload{
		Query:     state.Query,
		Tasks:     make([]taskView, 0, len(state.Tasks)),
		Evidence:  make([]evidenceView, 0, len(state.Evidence)),
		Round:     state.Round,
		MaxRounds: state.MaxRounds,
	}
	for _, task := range state.Tasks {
		p.Tasks = append(p.Tasks, taskView{
			ID:        task.ID,
			ToolName:  task.ToolName,
			Purpose:   task.Purpose,
			Refs:      task.Refs,
			Arguments: task.Arguments,
		})
	}
	for i, item := range state.Evidence {
		view := evidenceView{
			ID:           item.ID,
			TaskID:       item.TaskID,
			ToolName:     item.ToolName,
			Summary:      item.Summary,
			Error:        item.Error,
			CommandView:  item.CommandView,
			RawPreview:   previews[i].Preview,
			RawTruncated: previews[i].Truncated,
		}
		p.Evidence = append(p.Evidence, view)
	}

	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// 注入模型用的单条原始输出预览结果
type resolverRawPreview struct {
	// 预算内全文，或截断预览 / 占位说明
	Preview string
	// 是否因共享预算被截断或占位
	Truncated bool
}

// 生成注入模型的原始输出预览，不修改权威证据切片
// 全部原始输出共享一份预算；从最新向旧分配，优先保留较新证据的全文
// 预算小于等于零时使用默认合计预算
func prepareResolverRawPreviews(items []core.Evidence, budgetTokens int) []resolverRawPreview {
	out := make([]resolverRawPreview, len(items))
	if len(items) == 0 {
		return out
	}
	if budgetTokens <= 0 {
		budgetTokens = defaultResolverEvidenceBudgetTokens
	}

	// 从尾部（最新）向前扣减；够则全文，不够则截断预览，耗尽则占位
	remaining := budgetTokens
	for i := len(items) - 1; i >= 0; i-- {
		if len(items[i].Raw) == 0 {
			continue
		}
		raw := string(items[i].Raw)
		cost := estimateTokens(raw)
		if cost <= remaining {
			out[i].Preview = raw
			remaining -= cost
			continue
		}
		if remaining > 0 {
			out[i].Preview = truncateResolverRawPreview(raw, remaining)
			out[i].Truncated = true
			remaining = 0
			continue
		}
		out[i].Preview = omitResolverRawPreview()
		out[i].Truncated = true
	}
	return out
}

// 将超预算原始输出收成带截断标记的预览文本
// 预算按估算单位换算为预览字符上限（约四个字符单元一词元）
func truncateResolverRawPreview(raw string, budgetTokens int) string {
	runes := []rune(raw)
	maxRunes := budgetTokens * 4
	if maxRunes <= 0 {
		maxRunes = 200
	}
	shown := len(runes)
	preview := raw
	if len(runes) > maxRunes {
		preview = string(runes[:maxRunes])
		shown = maxRunes
	}
	return fmt.Sprintf(
		"%s…[truncated for model budget; full result retained in resolve state; shown %d/%d runes]",
		preview, shown, len(runes),
	)
}

// 共享预算已耗尽时的原始输出占位文案
// 摘要与命令视图仍注入模型，完整原始输出仍在定位环内存
func omitResolverRawPreview() string {
	return "[omitted for shared model budget; newer evidence prioritized; full result retained in resolve state]"
}

// 模型输出中间结构，映射为定位动作前须校验
type resolverLLMOutput struct {
	// 动作：调用工具 / 提交目标 / 失败
	Action string `json:"action"`
	// 选择该动作的理由，供编排与排障阅读
	Reason string `json:"reason"`
	// 调用工具时的工具提议列表，契约上本步仅允许恰好一条
	ToolCalls []resolverToolCall `json:"tool_calls"`
	// 提交目标时的目标内容（无系统目标编号）
	Targets []resolverTargetOut `json:"targets"`
	// 失败时的错误说明
	Error string `json:"error"`
}

// 模型提议的一次工具调用，不含任务编号（由编排发放）
type resolverToolCall struct {
	// 注册表中的工具名
	ToolName string `json:"tool_name"`
	// 符合对应工具规格的参数
	Arguments json.RawMessage `json:"arguments"`
	// 本步取证目的
	Purpose string `json:"purpose"`
	// 关联线索节点等通用引用
	Refs []string `json:"refs"`
}

// 模型提交的已确认目标内容，目标编号由编排在提交时发放
type resolverTargetOut struct {
	// 对应问题结构中节点的系统编号
	NodeID string `json:"node_id"`
	// 目标类型，如集群资源
	Type string `json:"type"`
	// 环境中确认的属性（命名空间、种类、名称等）
	Attrs map[string]string `json:"attrs"`
	// 支撑该目标的证据编号列表
	EvidenceIDs []string `json:"evidence_ids"`
}

// 校验并映射为编排使用的定位动作
func mapResolverOutput(out resolverLLMOutput, state ResolveState, specs []tools.ToolSpec) (ResolveAction, error) {
	action := ResolveActionKind(strings.TrimSpace(out.Action))
	switch action {
	case ResolveActionCallTool, ResolveActionSubmitTargets, ResolveActionFail:
	default:
		return ResolveAction{}, fmt.Errorf("unknown action %q", out.Action)
	}

	knownTools := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		knownTools[spec.Name] = struct{}{}
	}
	nodeIDs := make(map[string]struct{}, len(state.Query.Nodes))
	for _, node := range state.Query.Nodes {
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

	result := ResolveAction{
		Action: action,
		Reason: out.Reason,
		Error:  out.Error,
	}

	switch action {
	case ResolveActionCallTool:
		if len(out.ToolCalls) != 1 {
			return ResolveAction{}, fmt.Errorf("call_tool requires exactly one tool_call, got %d", len(out.ToolCalls))
		}
		call := out.ToolCalls[0]
		if strings.TrimSpace(call.ToolName) == "" {
			return ResolveAction{}, errors.New("tool_name is required")
		}
		if _, ok := knownTools[call.ToolName]; !ok {
			return ResolveAction{}, fmt.Errorf("unknown tool %q", call.ToolName)
		}
		if len(call.Arguments) == 0 {
			return ResolveAction{}, errors.New("tool arguments are required")
		}
		// 确保参数是对象
		var probe any
		if err := json.Unmarshal(call.Arguments, &probe); err != nil {
			return ResolveAction{}, fmt.Errorf("tool arguments: %w", err)
		}
		for _, ref := range call.Refs {
			if _, ok := nodeIDs[ref]; !ok {
				// 引用允许指向节点；未知引用视为违规以便重试
				return ResolveAction{}, fmt.Errorf("tool call refs unknown node %q", ref)
			}
		}
		result.ToolCalls = []ProposedToolCall{{
			ToolName:  call.ToolName,
			Arguments: append(json.RawMessage(nil), call.Arguments...),
			Purpose:   call.Purpose,
			Refs:      append([]string(nil), call.Refs...),
		}}
	case ResolveActionSubmitTargets:
		if len(out.Targets) == 0 {
			return ResolveAction{}, errors.New("submit_targets requires at least one target")
		}
		result.Targets = make([]ProposedTarget, 0, len(out.Targets))
		for i, t := range out.Targets {
			if strings.TrimSpace(t.NodeID) == "" {
				return ResolveAction{}, fmt.Errorf("target[%d] node_id is required", i)
			}
			if _, ok := nodeIDs[t.NodeID]; !ok {
				return ResolveAction{}, fmt.Errorf("target references unknown node %q", t.NodeID)
			}
			if len(t.EvidenceIDs) == 0 {
				return ResolveAction{}, fmt.Errorf("target[%d] evidence_ids is required", i)
			}
			for _, eid := range t.EvidenceIDs {
				if _, ok := evidenceIDs[eid]; !ok {
					return ResolveAction{}, fmt.Errorf("target references unknown evidence %q", eid)
				}
			}
			attrs := t.Attrs
			if attrs == nil {
				attrs = map[string]string{}
			}
			result.Targets = append(result.Targets, ProposedTarget{
				NodeID:      t.NodeID,
				Type:        t.Type,
				Attrs:       attrs,
				EvidenceIDs: append([]string(nil), t.EvidenceIDs...),
			})
		}
	case ResolveActionFail:
		if strings.TrimSpace(out.Error) == "" && strings.TrimSpace(out.Reason) == "" {
			return ResolveAction{}, errors.New("fail requires error or reason")
		}
		if result.Error == "" {
			result.Error = out.Reason
		}
	}

	return result, nil
}
