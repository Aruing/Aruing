package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"aruing/internal/core"
	"aruing/internal/session"
	"aruing/internal/store"
)

func TestMemoryRunLedgerPutGet(t *testing.T) {
	ctx := context.Background()
	l := store.NewMemoryRunLedger()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

	rec := session.DiagnosticRecord{
		RunID:     "run_1",
		SessionID: "sess_1",
		Question:  "demo-api 访问不了",
		Report: core.Report{
			ID:      "rep_1",
			RunID:   "run_1",
			Title:   "诊断",
			Summary: "后端异常",
			Conclusions: []core.Conclusion{{
				HypothesisID: "h_1",
				Result:       core.VerdictSupported,
				Reason:       "Pod 未就绪",
				EvidenceIDs:  []string{"e_1"},
			}},
			Suggestions: []string{"重启"},
			CreatedAt:   now,
		},
		Evidence: []core.Evidence{{
			ID:      "e_1",
			RunID:   "run_1",
			TaskID:  "t_1",
			Summary: "pod not ready",
			Raw:     json.RawMessage(`{"phase":"Pending"}`),
		}},
	}

	if err := l.Put(ctx, rec); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := l.Get(ctx, "run_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Question != rec.Question || got.Report.Summary != "后端异常" {
		t.Fatalf("record: %+v", got)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].ID != "e_1" {
		t.Fatalf("evidence: %+v", got.Evidence)
	}
	if string(got.Evidence[0].Raw) != `{"phase":"Pending"}` {
		t.Fatalf("raw: %s", got.Evidence[0].Raw)
	}
}

func TestMemoryRunLedgerIsolation(t *testing.T) {
	ctx := context.Background()
	l := store.NewMemoryRunLedger()

	raw := json.RawMessage(`{"a":1}`)
	rec := session.DiagnosticRecord{
		RunID: "run_1",
		Report: core.Report{
			Summary:     "s",
			Suggestions: []string{"one"},
		},
		Evidence: []core.Evidence{{ID: "e_1", Raw: raw}},
	}
	if err := l.Put(ctx, rec); err != nil {
		t.Fatalf("put: %v", err)
	}

	rec.Report.Summary = "mutated"
	rec.Report.Suggestions[0] = "two"
	rec.Evidence[0].Raw[0] = 'X'

	got, err := l.Get(ctx, "run_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Report.Summary != "s" || got.Report.Suggestions[0] != "one" {
		t.Fatalf("put isolation broken: %+v", got.Report)
	}
	if string(got.Evidence[0].Raw) != `{"a":1}` {
		t.Fatalf("raw isolation: %s", got.Evidence[0].Raw)
	}

	got.Report.Summary = "from get"
	got.Evidence[0].Raw[0] = 'Y'
	again, err := l.Get(ctx, "run_1")
	if err != nil {
		t.Fatalf("get again: %v", err)
	}
	if again.Report.Summary != "s" || string(again.Evidence[0].Raw) != `{"a":1}` {
		t.Fatalf("get isolation broken: %+v", again)
	}
}

func TestMemoryRunLedgerOverwrite(t *testing.T) {
	ctx := context.Background()
	l := store.NewMemoryRunLedger()

	if err := l.Put(ctx, session.DiagnosticRecord{
		RunID:     "run_1",
		SessionID: "sess_1",
		Question:  "q1",
		Report:    core.Report{Summary: "first"},
	}); err != nil {
		t.Fatalf("put1: %v", err)
	}
	if err := l.Put(ctx, session.DiagnosticRecord{
		RunID:     "run_1",
		SessionID: "sess_1",
		Question:  "q2",
		Report:    core.Report{Summary: "second"},
		Evidence:  []core.Evidence{{ID: "e_2"}},
	}); err != nil {
		t.Fatalf("put2: %v", err)
	}

	got, err := l.Get(ctx, "run_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Question != "q2" || got.Report.Summary != "second" || len(got.Evidence) != 1 {
		t.Fatalf("overwrite: %+v", got)
	}

	list, err := l.ListBySession(ctx, "sess_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len after overwrite: %d", len(list))
	}
}

func TestMemoryRunLedgerNotFound(t *testing.T) {
	ctx := context.Background()
	l := store.NewMemoryRunLedger()

	if _, err := l.Get(ctx, "missing"); !errors.Is(err, session.ErrRunNotFound) {
		t.Fatalf("get: %v", err)
	}
	if _, err := l.Get(ctx, ""); !errors.Is(err, session.ErrRunNotFound) {
		t.Fatalf("empty id: %v", err)
	}
}

func TestMemoryRunLedgerListBySession(t *testing.T) {
	ctx := context.Background()
	l := store.NewMemoryRunLedger()

	for _, rec := range []session.DiagnosticRecord{
		{RunID: "run_a", SessionID: "sess_1", Question: "a"},
		{RunID: "run_b", SessionID: "sess_1", Question: "b"},
		{RunID: "run_c", SessionID: "sess_2", Question: "c"},
	} {
		if err := l.Put(ctx, rec); err != nil {
			t.Fatalf("put %s: %v", rec.RunID, err)
		}
	}

	list, err := l.ListBySession(ctx, "sess_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].RunID != "run_a" || list[1].RunID != "run_b" {
		t.Fatalf("list: %+v", list)
	}

	empty, err := l.ListBySession(ctx, "sess_none")
	if err != nil {
		t.Fatalf("empty list: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("want empty, got %d", len(empty))
	}
}

func TestMemoryRunLedgerPutRequiresRunID(t *testing.T) {
	err := store.NewMemoryRunLedger().Put(context.Background(), session.DiagnosticRecord{})
	if err == nil {
		t.Fatal("expected error")
	}
}
