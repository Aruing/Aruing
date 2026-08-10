package session

import (
	"context"
	"fmt"
	"strings"

	"aruing/internal/core"
)

// 建运行并执行正式诊断，成功时写入诊断账本，组装诊断模式的应答输出
// 基线塔升格与临时诊断应答器共用，避免两套建运行逻辑
func Escalate(
	ctx context.Context,
	factory *core.Factory,
	executor RunExecutor,
	ledger RunLedger,
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
	if ledger == nil {
		return RespondOutput{}, fmt.Errorf("escalate: ledger is nil")
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

	report, evidence, err := executor.Execute(ctx, run)
	if err != nil {
		return RespondOutput{}, fmt.Errorf("execute run: %w", err)
	}

	if err := ledger.Put(ctx, DiagnosticRecord{
		RunID:     run.ID,
		SessionID: sessionID,
		Question:  question,
		Report:    report,
		Evidence:  evidence,
	}); err != nil {
		return RespondOutput{}, fmt.Errorf("put run ledger: %w", err)
	}

	return RespondOutput{
		Content: formatDiagnosticReply(report),
		Mode:    ModeDiagnostic,
		RunID:   run.ID,
		Report:  &report,
	}, nil
}

// 将报告展开为可落库的诊断摘要（供后续会话解释引用，不人为砍字段条数）
// 单条消息体积由基线塔上下文预算治理，不在此用固定条数截肢
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
