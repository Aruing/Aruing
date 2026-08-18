package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/Aruing/Aruing/internal/core"
)

// 建运行并执行正式诊断；完成写账本，挂起则返回澄清模式应答
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

	outcome, err := executor.Execute(ctx, run)
	if err != nil {
		return RespondOutput{}, fmt.Errorf("execute run: %w", err)
	}
	return outcomeToRespond(ctx, ledger, sessionID, question, run.ID, outcome)
}

// 恢复挂起运行：注入用户答复，完成写账本，再次挂起则再澄清
func Resume(
	ctx context.Context,
	runner SuspendedRunner,
	ledger RunLedger,
	sessionID, runID, answer string,
) (RespondOutput, error) {
	if err := ctx.Err(); err != nil {
		return RespondOutput{}, fmt.Errorf("resume: %w", err)
	}
	if runner == nil {
		return RespondOutput{}, fmt.Errorf("resume: runner is nil")
	}
	if ledger == nil {
		return RespondOutput{}, fmt.Errorf("resume: ledger is nil")
	}
	if strings.TrimSpace(sessionID) == "" {
		return RespondOutput{}, fmt.Errorf("resume: session id is required")
	}
	if strings.TrimSpace(runID) == "" {
		return RespondOutput{}, fmt.Errorf("resume: run id is required")
	}
	if strings.TrimSpace(answer) == "" {
		return RespondOutput{}, fmt.Errorf("resume: answer is required")
	}

	outcome, err := runner.Resume(ctx, runID, answer)
	if err != nil {
		return RespondOutput{}, fmt.Errorf("resume run: %w", err)
	}
	// 原问题在挂起快照内，账本 question 用用户澄清答复标注上下文；完成报告仍以 run 为准
	return outcomeToRespond(ctx, ledger, sessionID, answer, runID, outcome)
}

// 将编排 Outcome 转为会话应答：完成落账本，挂起返回澄清正文
func outcomeToRespond(
	ctx context.Context,
	ledger RunLedger,
	sessionID, question, runID string,
	outcome core.Outcome,
) (RespondOutput, error) {
	if outcome.Suspension != nil {
		content := formatClarifyReply(outcome.Suspension)
		return RespondOutput{
			Content: content,
			Mode:    ModeClarify,
			RunID:   outcome.Suspension.RunID,
		}, nil
	}
	if outcome.Report == nil {
		return RespondOutput{}, fmt.Errorf("outcome: report and suspension both empty")
	}
	report := *outcome.Report
	if err := ledger.Put(ctx, DiagnosticRecord{
		RunID:     runID,
		SessionID: sessionID,
		Question:  question,
		Report:    report,
		Evidence:  outcome.Evidence,
	}); err != nil {
		return RespondOutput{}, fmt.Errorf("put run ledger: %w", err)
	}
	return RespondOutput{
		Content: formatDiagnosticReply(report),
		Mode:    ModeDiagnostic,
		RunID:   runID,
		Report:  &report,
	}, nil
}

// 将澄清问题与候选格式化为用户可见正文
func formatClarifyReply(s *core.Suspension) string {
	if s == nil {
		return "需要更多信息才能继续诊断"
	}
	q := strings.TrimSpace(s.Question)
	if q == "" {
		q = "需要更多信息才能继续诊断"
	}
	if len(s.Options) == 0 {
		return q
	}
	var b strings.Builder
	b.WriteString(q)
	b.WriteString("\n")
	for _, opt := range s.Options {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}
		b.WriteString("\n- ")
		b.WriteString(opt)
	}
	return b.String()
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
