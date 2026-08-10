package session

import (
	"context"
	"fmt"

	"aruing/internal/core"
)

// 诊断管道入口；与编排器执行签名一致，便于注入真实现或测试替身
// 完成时 Outcome.Report 非空；需用户澄清时 Outcome.Suspension 非空
type RunExecutor interface {
	Execute(ctx context.Context, run core.Run) (core.Outcome, error)
}

// 挂起运行恢复入口；澄清答复后继续诊断
// 编排器实现；会话层在会话有挂起 run 时优先走此路径
type SuspendedRunner interface {
	// 注入用户答复并恢复；完成或再次挂起
	Resume(ctx context.Context, runID, answer string) (core.Outcome, error)
	// 查找会话内挂起运行编号；无则空串
	FindSuspended(sessionID string) string
}

// 临时脚手架：每轮强制升格诊断
// 产品路径应使用基线塔；本类型仅保留兼容测试或显式强制诊断场景
type DiagnoseResponder struct {
	// 为运行发放编号与时间
	factory *core.Factory
	// 正式诊断执行入口
	executor RunExecutor
	// 正式诊断结果账本；产品与测试路径均须注入
	ledger RunLedger
}

// 绑定发号器、诊断执行器与运行账本
func NewDiagnoseResponder(factory *core.Factory, executor RunExecutor, ledger RunLedger) *DiagnoseResponder {
	return &DiagnoseResponder{
		factory:  factory,
		executor: executor,
		ledger:   ledger,
	}
}

// 建运行（填会话编号与问题）→ 执行 → 落账本 → 用报告标题与摘要拼简短回复
func (r *DiagnoseResponder) Respond(ctx context.Context, in RespondInput) (RespondOutput, error) {
	if r == nil {
		return RespondOutput{}, fmt.Errorf("diagnose responder is nil")
	}
	return Escalate(ctx, r.factory, r.executor, r.ledger, in.SessionID, in.UserText)
}
