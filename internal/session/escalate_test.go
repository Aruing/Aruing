package session_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

type escalateFakeExecutor struct {
	report     core.Report
	evidence   []core.Evidence
	suspension *core.Suspension
	err        error
	lastRun    core.Run
}

func (e *escalateFakeExecutor) Execute(_ context.Context, run core.Run) (core.Outcome, error) {
	e.lastRun = run
	if e.err != nil {
		return core.Outcome{}, e.err
	}
	if e.suspension != nil {
		s := *e.suspension
		if s.RunID == "" {
			s.RunID = run.ID
		}
		if s.SessionID == "" {
			s.SessionID = run.SessionID
		}
		return core.Outcome{Suspension: &s}, nil
	}
	rep := e.report
	rep.RunID = run.ID
	return core.Outcome{Report: &rep, Evidence: e.evidence}, nil
}

// 升格成功后写入诊断账本，模式为诊断
func TestEscalateWritesLedger(t *testing.T) {
	ctx := context.Background()
	factory := core.NewFactory()
	ledger := store.NewMemoryRunLedger()
	exec := &escalateFakeExecutor{
		report:   core.Report{Title: "t", Summary: "摘要"},
		evidence: []core.Evidence{{ID: "e_1", Summary: "obs"}},
	}

	out, err := session.Escalate(ctx, factory, exec, ledger, "sess_1", "q1")
	if err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if out.Mode != session.ModeDiagnostic || out.RunID == "" {
		t.Fatalf("output: %+v", out)
	}

	rec, err := ledger.Get(ctx, out.RunID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.SessionID != "sess_1" || rec.Question != "q1" {
		t.Fatalf("record: %+v", rec)
	}
	if rec.Report.Summary != "摘要" || len(rec.Evidence) != 1 || rec.Evidence[0].ID != "e_1" {
		t.Fatalf("payload: %+v", rec)
	}
}

// 执行失败时不写账本
func TestEscalateExecuteErrorSkipsLedger(t *testing.T) {
	ctx := context.Background()
	factory := core.NewFactory()
	ledger := store.NewMemoryRunLedger()
	exec := &escalateFakeExecutor{err: errors.New("boom")}

	_, err := session.Escalate(ctx, factory, exec, ledger, "sess_1", "q1")
	if err == nil {
		t.Fatal("expected error")
	}
	if _, err := ledger.Get(ctx, exec.lastRun.ID); !errors.Is(err, session.ErrRunNotFound) {
		t.Fatalf("ledger should be empty, got %v", err)
	}
}

// 台账为空时拒绝
func TestEscalateRequiresLedger(t *testing.T) {
	factory := core.NewFactory()
	_, err := session.Escalate(context.Background(), factory, &escalateFakeExecutor{}, nil, "s", "q")
	if err == nil {
		t.Fatal("expected error")
	}
}

// 挂起时不写账本，返回澄清模式
func TestEscalateSuspensionNoLedger(t *testing.T) {
	ctx := context.Background()
	factory := core.NewFactory()
	ledger := store.NewMemoryRunLedger()
	exec := &escalateFakeExecutor{
		suspension: &core.Suspension{
			Stage:    core.StageResolve,
			Question: "是哪个命名空间？",
			Options:  []string{"ns-a", "ns-b"},
		},
	}
	out, err := session.Escalate(ctx, factory, exec, ledger, "sess_1", "q1")
	if err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if out.Mode != session.ModeClarify || out.Report != nil {
		t.Fatalf("output: %+v", out)
	}
	if out.RunID == "" || !strings.Contains(out.Content, "命名空间") {
		t.Fatalf("clarify content: %+v", out)
	}
	if _, err := ledger.Get(ctx, out.RunID); !errors.Is(err, session.ErrRunNotFound) {
		t.Fatalf("ledger should be empty, got %v", err)
	}
}

// 假挂起恢复器：实现 SuspendedRunner
type resumeFakeRunner struct {
	outcome core.Outcome
	err     error
	lastID  string
	lastAns string
}

func (r *resumeFakeRunner) Resume(_ context.Context, runID, answer string) (core.Outcome, error) {
	r.lastID = runID
	r.lastAns = answer
	if r.err != nil {
		return core.Outcome{}, r.err
	}
	return r.outcome, nil
}

func (r *resumeFakeRunner) FindSuspended(string) string { return "" }

// 恢复完成后写账本，模式为诊断
func TestResumeWritesLedger(t *testing.T) {
	ctx := context.Background()
	ledger := store.NewMemoryRunLedger()
	runner := &resumeFakeRunner{
		outcome: core.Outcome{
			Report: &core.Report{Title: "t", Summary: "澄清后完成"},
		},
	}
	out, err := session.Resume(ctx, runner, ledger, "sess_1", "run_1", "ns-a")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if out.Mode != session.ModeDiagnostic || out.RunID != "run_1" {
		t.Fatalf("output: %+v", out)
	}
	if runner.lastID != "run_1" || runner.lastAns != "ns-a" {
		t.Fatalf("runner got run=%q ans=%q", runner.lastID, runner.lastAns)
	}
	rec, err := ledger.Get(ctx, "run_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.Question != "ns-a" || rec.Report.Summary != "澄清后完成" {
		t.Fatalf("record: %+v", rec)
	}
}

// 恢复后再次挂起：不写账本，返回澄清
func TestResumeAgainSuspends(t *testing.T) {
	ctx := context.Background()
	ledger := store.NewMemoryRunLedger()
	runner := &resumeFakeRunner{
		outcome: core.Outcome{
			Suspension: &core.Suspension{
				RunID:    "run_1",
				Stage:    core.StageResolve,
				Question: "是哪个工作负载？",
				Options:  []string{"deploy/a", "deploy/b"},
			},
		},
	}
	out, err := session.Resume(ctx, runner, ledger, "sess_1", "run_1", "ns-a")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if out.Mode != session.ModeClarify || out.RunID != "run_1" {
		t.Fatalf("output: %+v", out)
	}
	if !strings.Contains(out.Content, "工作负载") {
		t.Fatalf("content: %q", out.Content)
	}
	if _, err := ledger.Get(ctx, "run_1"); !errors.Is(err, session.ErrRunNotFound) {
		t.Fatalf("ledger should be empty, got %v", err)
	}
}

// 空答复拒绝恢复
func TestResumeRequiresAnswer(t *testing.T) {
	_, err := session.Resume(context.Background(), &resumeFakeRunner{}, store.NewMemoryRunLedger(), "s", "r", "  ")
	if err == nil {
		t.Fatal("expected error")
	}
}
