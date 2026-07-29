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

// 将 Report 展开为可落库的诊断摘要（供后续会话解释引用，#18 不人为砍字段条数）
// 单条 Message 体积由 Tower 上下文预算治理，不在此用固定 N 截肢
func formatDiagnosticReply(report core.Report) string {
	var b strings.Builder

	title := strings.TrimSpace(report.Title)
	summary := strings.TrimSpace(report.Summary)
	switch {
	case title != "" && summary != "":
		b.WriteString(title)
		b.WriteString("：")
		b.WriteString(summary)
	case title != "":
		b.WriteString(title)
	case summary != "":
		b.WriteString(summary)
	default:
		b.WriteString("诊断已完成")
	}

	if len(report.Conclusions) > 0 {
		b.WriteString("\n\n结论：")
		for _, c := range report.Conclusions {
			reason := strings.TrimSpace(c.Reason)
			if reason == "" {
				reason = string(c.Result)
			}
			b.WriteString("\n- [")
			b.WriteString(string(c.Result))
			b.WriteString("] ")
			b.WriteString(reason)
			if id := strings.TrimSpace(c.HypothesisID); id != "" {
				b.WriteString(" (")
				b.WriteString(id)
				b.WriteString(")")
			}
		}
	}

	if len(report.Suggestions) > 0 {
		b.WriteString("\n\n建议：")
		for _, s := range report.Suggestions {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			b.WriteString("\n- ")
			b.WriteString(s)
		}
	}

	return b.String()
}
