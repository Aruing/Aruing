// 真实定位器通过大模型提议工具调用或提交已确认目标
//
// 与 FakeResolver 共享 ResolveDriver 边界：只返回意图，不执行工具、不发放领域编号
// 工具规格来自 Registry.Specs 注入，禁止在 prompt 手写第二份工具清单
// Target 内容字段由模型产出，Target ID 由编排在 submit 时发放
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

// 定位驱动业务级重试次数：JSON 合法但语义违规时重新请求
const maxResolveAttempts = 3

// 用大模型驱动定位循环的实现
//
// 持有不可变依赖（客户端、工具规格快照、prompt），可被多次运行复用
// 不持有跨运行可变状态；每轮 Next 独立发 GenerateJSON
type LLMResolver struct {
	// 大模型客户端，每轮 Next 发一次 GenerateJSON
	client llm.Client
	// 注册表工具规格快照，用于拼 system prompt 与校验 tool_name
	specs []tools.ToolSpec
	// 已注入工具规格的系统提示词全文，构造后只读
	prompt string
}

// 创建基于大模型的定位驱动
// specs 应为 Registry.Specs() 快照；空列表时仍可构造，但 call_tool 将无法通过校验
func NewLLMResolver(client llm.Client, specs []tools.ToolSpec) (*LLMResolver, error) {
	if client == nil {
		return nil, errors.New("LLM resolver requires an llm client")
	}
	// 复制 specs，避免调用方后续修改注册表快照影响已构造实例
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

// 将嵌入 prompt 中的工具规格占位符替换为 JSON 文本
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

// 序列化回喂状态：证据只带摘要与截断 raw，避免撑爆上下文
func buildResolverUserPayload(state ResolveState) (string, error) {
	// 喂给模型的证据摘要，不含完整 Raw
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
		// 可展示的命令视图（如 argv 拼串）
		CommandView string `json:"commandView,omitempty"`
		// 原始输出预览，超长已截断
		RawPreview string `json:"rawPreview,omitempty"`
	}
	// 喂给模型的本轮已发起任务视图
	type taskView struct {
		// 任务系统编号
		ID string `json:"id"`
		// 工具名
		ToolName string `json:"toolName"`
		// 取证目的说明
		Purpose string `json:"purpose,omitempty"`
		// 关联的 Node/Target 等通用引用
		Refs []string `json:"refs,omitempty"`
		// 工具入参 JSON
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	// 定位轮次完整 user payload
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

	const maxRawPreview = 2000
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
	for _, item := range state.Evidence {
		view := evidenceView{
			ID:          item.ID,
			TaskID:      item.TaskID,
			ToolName:    item.ToolName,
			Summary:     item.Summary,
			Error:       item.Error,
			CommandView: item.CommandView,
		}
		if len(item.Raw) > 0 {
			raw := string(item.Raw)
			if len(raw) > maxRawPreview {
				raw = raw[:maxRawPreview] + "…(truncated)"
			}
			view.RawPreview = raw
		}
		p.Evidence = append(p.Evidence, view)
	}

	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// 模型输出中间结构，映射为 ResolveAction 前须校验
type resolverLLMOutput struct {
	// 动作：call_tool / submit_targets / fail
	Action string `json:"action"`
	// 选择该动作的理由，供编排与排障阅读
	Reason string `json:"reason"`
	// call_tool 时的工具提议列表，契约上本步仅允许恰好一条
	ToolCalls []resolverToolCall `json:"tool_calls"`
	// submit_targets 时提交的目标内容（无系统 Target.ID）
	Targets []resolverTargetOut `json:"targets"`
	// fail 时的错误说明
	Error string `json:"error"`
}

// 模型提议的一次工具调用，不含 Task.ID（由编排发放）
type resolverToolCall struct {
	// 注册表中的工具名
	ToolName string `json:"tool_name"`
	// 符合对应 ToolSpec 的参数 JSON
	Arguments json.RawMessage `json:"arguments"`
	// 本步取证目的
	Purpose string `json:"purpose"`
	// 关联线索节点等通用引用
	Refs []string `json:"refs"`
}

// 模型提交的已确认目标内容，Target.ID 由编排在 submit 时发放
type resolverTargetOut struct {
	// 对应 Query 中节点的系统编号
	NodeID string `json:"node_id"`
	// 目标类型，如 k8s.resource
	Type string `json:"type"`
	// 环境中确认的属性（命名空间、kind、name 等）
	Attrs map[string]string `json:"attrs"`
	// 支撑该目标的证据编号列表
	EvidenceIDs []string `json:"evidence_ids"`
}

// 校验并映射为编排使用的 ResolveAction
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
		// 确保 arguments 是 JSON 对象
		var probe any
		if err := json.Unmarshal(call.Arguments, &probe); err != nil {
			return ResolveAction{}, fmt.Errorf("tool arguments: %w", err)
		}
		for _, ref := range call.Refs {
			if _, ok := nodeIDs[ref]; !ok {
				// refs 允许引用节点；未知 ref 视为违规以便重试
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
