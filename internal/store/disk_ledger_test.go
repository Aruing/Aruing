package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 组装一条最小可判定的诊断记录
func newDiskRecord(runID, sessionID string) session.DiagnosticRecord {
	return session.DiagnosticRecord{
		RunID:     runID,
		SessionID: sessionID,
		Question:  "why is demo-api unreachable",
		Report: core.Report{
			ID:    "rep_" + runID,
			Title: "demo-api crashloop",
			Conclusions: []core.Conclusion{{
				HypothesisID: "hyp_1",
				Result:       core.VerdictSupported,
				Reason:       "bad image tag",
				EvidenceIDs:  []string{"ev_1"},
			}},
		},
		Evidence: []core.Evidence{{
			ID:          "ev_1",
			RunID:       runID,
			Source:      "k8s",
			ToolName:    "k8s",
			CommandView: "kubectl get pods",
			Summary:     "1 pod crashloop",
			Raw:         json.RawMessage(`{"rows":1}`),
		}},
	}
}

// 落盘后重开：按编号读回、按会话列出（首现序）全量一致
func TestDiskRunLedgerRoundtrip(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	l, err := store.NewDiskRunLedger(ctx, root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, id := range []struct{ run, sess string }{
		{"run_1", "sess_a"}, {"run_2", "sess_a"}, {"run_3", "sess_b"},
	} {
		if putErr := l.Put(ctx, newDiskRecord(id.run, id.sess)); putErr != nil {
			t.Fatalf("put %s: %v", id.run, putErr)
		}
	}

	reopened, err := store.NewDiskRunLedger(ctx, root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	rec, err := reopened.Get(ctx, "run_2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.Report.Title != "demo-api crashloop" || len(rec.Evidence) != 1 {
		t.Fatalf("record content broken: %+v", rec)
	}

	a, err := reopened.ListBySession(ctx, "sess_a")
	if err != nil {
		t.Fatalf("list sess_a: %v", err)
	}
	if len(a) != 2 || a[0].RunID != "run_1" || a[1].RunID != "run_2" {
		t.Fatalf("session order broken: %+v", a)
	}
	if b, _ := reopened.ListBySession(ctx, "sess_b"); len(b) != 1 {
		t.Fatalf("sess_b records: %d", len(b))
	}
}

// 同运行编号覆盖：内容更新，会话内首现序不变
func TestDiskRunLedgerOverwriteKeepsOrder(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	l, _ := store.NewDiskRunLedger(ctx, root)

	if err := l.Put(ctx, newDiskRecord("run_1", "sess_a")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := l.Put(ctx, newDiskRecord("run_2", "sess_a")); err != nil {
		t.Fatalf("put: %v", err)
	}
	overwritten := newDiskRecord("run_1", "sess_a")
	overwritten.Report.Title = "updated root cause"
	if err := l.Put(ctx, overwritten); err != nil {
		t.Fatalf("overwrite: %v", err)
	}

	recs, err := l.ListBySession(ctx, "sess_a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
	if recs[0].RunID != "run_1" || recs[0].Report.Title != "updated root cause" {
		t.Fatalf("overwrite broken: %+v", recs[0])
	}
	if recs[1].RunID != "run_2" {
		t.Fatalf("order changed by overwrite: %+v", recs)
	}
}

// 会话编号必填：run 与 chat 统一为会话模型后空值是接线错误
func TestDiskRunLedgerRejectsEmptySession(t *testing.T) {
	ctx := context.Background()
	l, _ := store.NewDiskRunLedger(ctx, t.TempDir())
	err := l.Put(ctx, newDiskRecord("run_x", ""))
	if err == nil || !strings.Contains(err.Error(), "session id") {
		t.Fatalf("want session id error, got %v", err)
	}
}

// 同运行编号换会话写入按接线错误拒绝：旧会话目录会残留记录文件，
// 放行会在下次启动扫描时被判跨会话重复而拒启
func TestDiskRunLedgerRejectsCrossSessionOverwrite(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	l, _ := store.NewDiskRunLedger(ctx, root)
	if err := l.Put(ctx, newDiskRecord("run_1", "sess_a")); err != nil {
		t.Fatalf("put: %v", err)
	}

	err := l.Put(ctx, newDiskRecord("run_1", "sess_b"))
	if err == nil || !strings.Contains(err.Error(), "cross-session") {
		t.Fatalf("want cross-session error, got %v", err)
	}
	// 拒绝发生在写入前：原记录仍可读回，且新会话目录无残留文件
	if _, err := l.Get(ctx, "run_1"); err != nil {
		t.Fatalf("get after reject: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "sess_b", "runs", "run_1.json")); statErr == nil {
		t.Fatal("rejected write must not leave a file in session b")
	}
	// 拒绝后重开仍能干净启动（无跨会话重复）
	if _, err := store.NewDiskRunLedger(ctx, root); err != nil {
		t.Fatalf("reopen after reject: %v", err)
	}
}

// 记录文件临时残留不进索引；读回是深拷贝（改动 Raw 不影响再读）
func TestDiskRunLedgerTmpResidueAndIsolation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	l, _ := store.NewDiskRunLedger(ctx, root)
	if err := l.Put(ctx, newDiskRecord("run_1", "sess_a")); err != nil {
		t.Fatalf("put: %v", err)
	}
	// 手造残留临时文件（断电在 rename 前中断的形态）
	runsDir := filepath.Join(root, "sess_a", "runs")
	if err := os.WriteFile(filepath.Join(runsDir, ".run-tmp-x.json"), []byte("partial"), 0o600); err != nil {
		t.Fatalf("write tmp: %v", err)
	}

	reopened, err := store.NewDiskRunLedger(ctx, root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := reopened.Get(ctx, ".run-tmp-x"); err == nil {
		t.Fatal("tmp residue should not be indexed")
	}
	recs, listErr := reopened.ListBySession(ctx, "sess_a")
	if listErr != nil || len(recs) != 1 {
		t.Fatalf("list: %v %d", listErr, len(recs))
	}

	rec, _ := reopened.Get(ctx, "run_1")
	rec.Evidence[0].Raw[0] = 'X'
	again, _ := reopened.Get(ctx, "run_1")
	if again.Evidence[0].Raw[0] == 'X' {
		t.Fatal("raw isolation broken")
	}
}

// 并发同号不同会话写入：恰一个胜出（判重在锁内），重开无跨会话重复；
// 未修锁前此测试在 -race 下报数据竞争
func TestDiskRunLedgerConcurrentPutSameRun(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	l, _ := store.NewDiskRunLedger(ctx, root)

	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := l.Put(ctx, newDiskRecord("run_race", fmt.Sprintf("sess_%d", i)))
			mu.Lock()
			if err == nil {
				wins++
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("want exactly 1 winner, got %d", wins)
	}
	if _, err := store.NewDiskRunLedger(ctx, root); err != nil {
		t.Fatalf("reopen: %v", err)
	}
}

// 记录内容的会话归属与所在目录不符视为损坏（文件被挪动或写错位置）
func TestDiskRunLedgerSessionOwnershipMismatch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	// 手造内容归属 sess_other、却放在 sess_a 目录的记录
	dir := filepath.Join(root, "sess_a", "runs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(newDiskRecord("run_m", "sess_other"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	err = os.WriteFile(filepath.Join(dir, "run_m.json"), data, 0o600)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	l, err := store.NewDiskRunLedger(ctx, root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = l.Get(ctx, "run_m")
	if err == nil || !strings.Contains(err.Error(), "does not match directory") {
		t.Fatalf("want ownership error, got %v", err)
	}
}

// 不存在的运行编号返回未找到错误
func TestDiskRunLedgerNotFound(t *testing.T) {
	ctx := context.Background()
	l, _ := store.NewDiskRunLedger(ctx, t.TempDir())
	if _, err := l.Get(ctx, "run_missing"); err == nil {
		t.Fatal("want not found")
	}
}
