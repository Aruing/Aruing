package session_test

import (
	"context"
	"errors"
	"testing"

	"aruing/internal/core"
	"aruing/internal/session"
	"aruing/internal/store"
)

type escalateFakeExecutor struct {
	report   core.Report
	evidence []core.Evidence
	err      error
	lastRun  core.Run
}

func (e *escalateFakeExecutor) Execute(_ context.Context, run core.Run) (core.Report, []core.Evidence, error) {
	e.lastRun = run
	if e.err != nil {
		return core.Report{}, nil, e.err
	}
	rep := e.report
	rep.RunID = run.ID
	return rep, e.evidence, nil
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
