package session

import (
	"context"
	"fmt"

	"aruing/internal/core"
)

// 诊断管道入口；与 Orchestrator.Execute 签名一致，便于注入真实现或测试替身
type RunExecutor interface {
	Execute(ctx context.Context, run core.Run) (core.Report, []core.Evidence, error)
}

// 临时脚手架：每轮强制 escalate
// 产品路径应使用 Tower；本类型仅保留兼容测试或显式强制诊断场景
type DiagnoseResponder struct {
	// 为 Run 发放编号与时间
	factory *core.Factory
	// 正式诊断执行入口
	executor RunExecutor
	// 正式诊断结果账本；产品与测试路径均须注入
	ledger RunLedger
}

// 绑定发号器、诊断执行器与 Run 账本
func NewDiagnoseResponder(factory *core.Factory, executor RunExecutor, ledger RunLedger) *DiagnoseResponder {
	return &DiagnoseResponder{
		factory:  factory,
		executor: executor,
		ledger:   ledger,
	}
}

// 建 Run（填 SessionID 与 Question）→ Execute → 落账本 → 用报告标题/摘要拼简短回复
func (r *DiagnoseResponder) Respond(ctx context.Context, in RespondInput) (RespondOutput, error) {
	if r == nil {
		return RespondOutput{}, fmt.Errorf("diagnose responder is nil")
	}
	return Escalate(ctx, r.factory, r.executor, r.ledger, in.SessionID, in.UserText)
}
