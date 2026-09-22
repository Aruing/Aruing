package session

import (
	"context"
	"fmt"

	"github.com/Aruing/Aruing/internal/core"
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
	// 可选；挂起快照存储（run 一次性进程，挂起必须落盘才能跨进程恢复）
	suspensions SuspensionStore
}

// 绑定发号器、诊断执行器与运行账本
func NewDiagnoseResponder(factory *core.Factory, executor RunExecutor, ledger RunLedger) *DiagnoseResponder {
	return &DiagnoseResponder{
		factory:  factory,
		executor: executor,
		ledger:   ledger,
	}
}

// 可选注入挂起快照存储；不注入时行为与进程内形态一致
func (r *DiagnoseResponder) SetSuspensionStore(store SuspensionStore) {
	r.suspensions = store
}

// 建运行（填会话编号与问题）→ 执行 → 完成时落账本并拼诊断回复；挂起时返回澄清正文
func (r *DiagnoseResponder) Respond(ctx context.Context, in RespondInput) (RespondOutput, error) {
	if r == nil {
		return RespondOutput{}, fmt.Errorf("diagnose responder is nil")
	}
	out, err := Escalate(ctx, r.factory, r.executor, r.ledger, in.SessionID, in.UserText)
	if err != nil {
		return RespondOutput{}, err
	}
	// run 进程随后即退出：挂起快照必须落盘，否则澄清答复无处可恢复；
	// 落盘失败 = 挂起必丢，明确失败优于带着澄清问题静默退出（#18）
	if out.Mode == ModeClarify {
		if perr := PersistSuspension(ctx, r.executor, r.suspensions, in.SessionID, out.RunID); perr != nil {
			return RespondOutput{}, perr
		}
	}
	return out, nil
}
