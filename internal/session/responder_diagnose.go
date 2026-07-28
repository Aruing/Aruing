package session

import (
	"context"
	"fmt"
	"strings"

	"aruing/internal/core"
)

// 诊断管道入口；与 Orchestrator.Execute 签名一致，便于注入真实现或测试替身
type RunExecutor interface {
	Execute(ctx context.Context, run core.Run) (core.Report, []core.Evidence, error)
}

// 临时脚手架：每轮建 Run 并调 Execute，Tower 接上后应删除本路径
// 证明 SessionID 能写入 Run，以及 Turn 与诊断管道的衔接
type DiagnoseResponder struct {
	// 为 Run 发放编号与时间
	factory *core.Factory
	// 正式诊断执行入口
	executor RunExecutor
}

// 绑定发号器与诊断执行器
func NewDiagnoseResponder(factory *core.Factory, executor RunExecutor) *DiagnoseResponder {
	return &DiagnoseResponder{
		factory:  factory,
		executor: executor,
	}
}

// 建 Run（填 SessionID 与 Question）→ Execute → 用报告标题/摘要拼简短回复
func (r *DiagnoseResponder) Respond(ctx context.Context, in RespondInput) (RespondOutput, error) {
	if err := ctx.Err(); err != nil {
		return RespondOutput{}, fmt.Errorf("diagnose respond: %w", err)
	}
	if r == nil || r.factory == nil {
		return RespondOutput{}, fmt.Errorf("diagnose responder factory is nil")
	}
	if r.executor == nil {
		return RespondOutput{}, fmt.Errorf("diagnose responder executor is nil")
	}

	runID, err := r.factory.NewID("run")
	if err != nil {
		return RespondOutput{}, fmt.Errorf("new run id: %w", err)
	}
	now := r.factory.Now()
	// SessionID 必须写入，便于后续按会话追溯正式诊断
	run := core.Run{
		ID:        runID,
		SessionID: in.SessionID,
		Question:  in.UserText,
		Status:    core.RunStatusCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}

	report, _, err := r.executor.Execute(ctx, run)
	if err != nil {
		return RespondOutput{}, fmt.Errorf("execute run: %w", err)
	}

	return RespondOutput{
		Content: formatDiagnosticReply(report),
		Mode:    ModeDiagnostic,
		RunID:   run.ID,
		Report:  &report,
	}, nil
}

// 用报告标题与摘要拼一段面向用户的短回复
func formatDiagnosticReply(report core.Report) string {
	title := strings.TrimSpace(report.Title)
	summary := strings.TrimSpace(report.Summary)
	switch {
	case title != "" && summary != "":
		return title + "：" + summary
	case title != "":
		return title
	case summary != "":
		return summary
	default:
		return "诊断已完成"
	}
}
