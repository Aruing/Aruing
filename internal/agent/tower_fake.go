package agent

import (
	"context"
	"fmt"
	"strings"

	"aruing/internal/core"
	"aruing/internal/session"
)

// 测试用 Tower：用 Decide 选动作，不调 LLM
// Decide 为 nil 时默认 reply，内容为「基线：」+ 用户原文
type FakeTowerResponder struct {
	// 为 escalate 发 Run 编号
	Factory *core.Factory
	// 正式诊断入口；仅 escalate 时使用
	Executor session.RunExecutor
	// 可选；返回 action（reply|escalate）、content、question
	Decide func(in session.RespondInput) (action, content, question string)
}

// 按 Decide 分支 reply 或 escalate
func (f *FakeTowerResponder) Respond(ctx context.Context, in session.RespondInput) (session.RespondOutput, error) {
	if err := ctx.Err(); err != nil {
		return session.RespondOutput{}, fmt.Errorf("fake tower: %w", err)
	}
	if f == nil {
		return session.RespondOutput{}, fmt.Errorf("fake tower is nil")
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
