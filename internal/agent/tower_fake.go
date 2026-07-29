package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"aruing/internal/core"
	"aruing/internal/session"
	"aruing/internal/tools"
)

// 测试用 Tower：用 Decide 选动作，不调 LLM
// Decide 为 nil 时默认 reply，内容为「基线：」+ 用户原文
// 支持 call_tool：需 Dispatcher；CallTool 提供工具提议；Decide 可按调用次数切换动作
type FakeTowerResponder struct {
	// 为 escalate / call_tool 发编号
	Factory *core.Factory
	// 正式诊断入口；仅 escalate 时使用
	Executor session.RunExecutor
	// 基线 tool 环；nil 时 call_tool 返回错误
	Dispatcher *tools.Dispatcher
	// 可选；返回 action（reply|call_tool|escalate）、content、question
	// 每轮决策都会调用（含 tool 观察回喂后），可用闭包计数
	Decide func(in session.RespondInput) (action, content, question string)
	// action=call_tool 时使用的工具提议；可在 Decide 闭包外修改
	CallTool towerToolCall
	// 本轮最多 call_tool 次数，0 表示使用默认
	BaselineMaxToolRounds int
}

// 按 Decide 分支 reply、call_tool 或 escalate；call_tool 在环内执行后再次 Decide
func (f *FakeTowerResponder) Respond(ctx context.Context, in session.RespondInput) (session.RespondOutput, error) {
	if err := ctx.Err(); err != nil {
		return session.RespondOutput{}, fmt.Errorf("fake tower: %w", err)
	}
	if f == nil {
		return session.RespondOutput{}, fmt.Errorf("fake tower is nil")
	}

	maxRounds := f.BaselineMaxToolRounds
	if maxRounds <= 0 {
		maxRounds = defaultBaselineMaxToolRounds
	}
	toolRounds := 0

	for {
		if err := ctx.Err(); err != nil {
			return session.RespondOutput{}, fmt.Errorf("fake tower: %w", err)
		}

		action, content, question := towerActionReply, "基线："+in.UserText, ""
		if f.Decide != nil {
			action, content, question = f.Decide(in)
		}
		action = strings.TrimSpace(strings.ToLower(action))

		switch action {
		case towerActionReply:
			if strings.TrimSpace(content) == "" {
				content = "基线：" + in.UserText
			}
			return session.RespondOutput{
				Content: content,
				Mode:    session.ModeBaseline,
			}, nil

		case towerActionCallTool:
			if f.Dispatcher == nil {
				return session.RespondOutput{}, fmt.Errorf("fake tower: call_tool requires dispatcher")
			}
			if toolRounds >= maxRounds {
				return session.RespondOutput{}, fmt.Errorf(
					"fake tower: baseline tool budget exhausted (%d rounds)", maxRounds)
			}
			if err := f.executeCallTool(ctx); err != nil {
				return session.RespondOutput{}, err
			}
			toolRounds++

		case towerActionEscalate:
			q := strings.TrimSpace(question)
			if q == "" {
				q = in.UserText
			}
			return session.Escalate(ctx, f.Factory, f.Executor, in.SessionID, q)

		default:
			return session.RespondOutput{}, fmt.Errorf("fake tower: unknown action %q", action)
		}
	}
}

// 经 Dispatcher 执行 CallTool；工具失败不中断环（与真 Tower 对齐：仅 ctx 取消传播）
// Fake 不把 observation 回传给 Decide，测试用闭包计数模拟多步
func (f *FakeTowerResponder) executeCallTool(ctx context.Context) error {
	if f.Factory == nil {
		return fmt.Errorf("fake tower: factory is required for call_tool")
	}
	taskID, err := f.Factory.NewID("t")
	if err != nil {
		return fmt.Errorf("fake tower: create task id: %w", err)
	}
	args := f.CallTool.Arguments
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	toolName := f.CallTool.ToolName
	if toolName == "" {
		return fmt.Errorf("fake tower: call_tool requires tool name")
	}
	task := core.Task{
		ID:        taskID,
		RunID:     "",
		ToolName:  toolName,
		Arguments: args,
		Purpose:   f.CallTool.Purpose,
	}
	_, execErr := f.Dispatcher.Execute(ctx, task)
	if execErr != nil && ctx.Err() != nil {
		return fmt.Errorf("fake tower: execute tool: %w", ctx.Err())
	}
	return nil
}
