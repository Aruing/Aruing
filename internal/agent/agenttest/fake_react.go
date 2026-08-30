// B3 ReAct 选择器的测试替身：按脚本顺序回放选择，耗尽后重复最后一条
// 与既有假角色同边界：不调模型、不执行工具，行为确定可断言
package agenttest

import (
	"context"
	"fmt"

	"github.com/Aruing/Aruing/internal/agent"
)

// 按脚本回放的假 ReAct 选择器
//
// 脚本按调用次序消费，耗尽后重复最后一条（预算尽等长循环测试不必预填全部
// 轮次）；脚本动作名必须在当轮菜单内（与 LLM 路径同契约，写错脚本立刻暴露）
type FakeReActSelector struct {
	// 选择脚本（按轮次顺序回放）
	Script []agent.ReActChoice
	// SelectAction 调用次数（断言「挂起恢复不重规划」类行为用）
	Calls int
	// 最近一次请求（载荷断言用；字段为调用方切片的浅拷贝，勿改内容）
	LastRequest agent.ReActSelectRequest
}

// 用给定脚本创建可重复使用的假选择器
func NewFakeReActSelector(script ...agent.ReActChoice) *FakeReActSelector {
	return &FakeReActSelector{Script: append([]agent.ReActChoice(nil), script...)}
}

// 校验菜单非空后按脚本回放当前轮次的选择
func (s *FakeReActSelector) SelectAction(ctx context.Context, req agent.ReActSelectRequest) (agent.ReActChoice, error) {
	if ctx == nil {
		return agent.ReActChoice{}, fmt.Errorf("react selector requires a context")
	}
	if err := ctx.Err(); err != nil {
		return agent.ReActChoice{}, fmt.Errorf("react select: %w", err)
	}
	if s == nil {
		return agent.ReActChoice{}, fmt.Errorf("react selector is required")
	}
	if len(req.Actions) == 0 {
		return agent.ReActChoice{}, fmt.Errorf("react select requires a non-empty action menu")
	}
	if len(s.Script) == 0 {
		return agent.ReActChoice{}, fmt.Errorf("react selector requires a script")
	}

	s.Calls++
	s.LastRequest = req
	idx := s.Calls - 1
	if idx >= len(s.Script) {
		idx = len(s.Script) - 1
	}
	choice := s.Script[idx]
	if !choice.Sufficient {
		found := false
		for _, a := range req.Actions {
			if a.Name == choice.ActionName {
				found = true
				break
			}
		}
		if !found {
			return agent.ReActChoice{}, fmt.Errorf("script action %q not in menu", choice.ActionName)
		}
	}
	return choice, nil
}
