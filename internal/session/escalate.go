package session

import (
	"context"
	"fmt"
	"strings"

	"aruing/internal/core"
)

// 建 Run 并执行正式诊断，组装 diagnostic 模式的 RespondOutput
// Tower escalate 与临时 DiagnoseResponder 共用，避免两套建 Run 逻辑
func Escalate(
	ctx context.Context,
	factory *core.Factory,
	executor RunExecutor,
	sessionID, question string,
) (RespondOutput, error) {
	if err := ctx.Err(); err != nil {
		return RespondOutput{}, fmt.Errorf("escalate: %w", err)
	}
	if factory == nil {
		return RespondOutput{}, fmt.Errorf("escalate: factory is nil")
	}
	if executor == nil {
		return RespondOutput{}, fmt.Errorf("escalate: executor is nil")
	}
	if strings.TrimSpace(sessionID) == "" {
		return RespondOutput{}, fmt.Errorf("escalate: session id is required")
	}
	if strings.TrimSpace(question) == "" {
		return RespondOutput{}, fmt.Errorf("escalate: question is required")
	}

	runID, err := factory.NewID("run")
	if err != nil {
		return RespondOutput{}, fmt.Errorf("new run id: %w", err)
	}
	now := factory.Now()
	run := core.Run{
		ID:        runID,
		SessionID: sessionID,
		Question:  question,
		Status:    core.RunStatusCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}

	report, _, err := executor.Execute(ctx, run)
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
