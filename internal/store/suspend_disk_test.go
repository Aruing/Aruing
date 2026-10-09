package store_test

// 挂起快照磁盘存储：往返、覆盖、多文件取最新、编号防御、残留容忍与清理。

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Aruing/Aruing/internal/store"
)

// 组装磁盘挂起快照存储（测试共用）
func newSuspStore(t *testing.T) (*store.DiskSuspensionStore, string) {
	t.Helper()
	root := t.TempDir()
	s, err := store.NewDiskSuspensionStore(context.Background(), root)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return s, root
}

// 写入后可按会话读回同一载荷与运行编号；删除后不可再读
func TestSuspensionStoreRoundtrip(t *testing.T) {
	s, _ := newSuspStore(t)
	ctx := context.Background()

	if err := s.PutSuspension(ctx, "sess_1", "run_1", []byte(`{"v":1}`)); err != nil {
		t.Fatalf("put: %v", err)
	}
	runID, payload, found, err := s.GetSuspension(ctx, "sess_1")
	if err != nil || !found {
		t.Fatalf("get: found=%v err=%v", found, err)
	}
	if runID != "run_1" || string(payload) != `{"v":1}` {
		t.Fatalf("get = %q %q", runID, payload)
	}

	// 无挂起的会话：found 为假不报错
	if _, _, found, err := s.GetSuspension(ctx, "sess_none"); err != nil || found {
		t.Fatalf("get empty: found=%v err=%v", found, err)
	}

	if err := s.DeleteSuspension(ctx, "sess_1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, found, err := s.GetSuspension(ctx, "sess_1"); err != nil || found {
		t.Fatalf("get after delete: found=%v err=%v", found, err)
	}
	// 重复删除幂等；不存在的会话目录不报错
	if err := s.DeleteSuspension(ctx, "sess_1"); err != nil {
		t.Fatalf("re-delete: %v", err)
	}
}

// 同会话覆盖写：单文件形态，不留旧版本；不同会话互不可见
func TestSuspensionStoreOverwrite(t *testing.T) {
	s, root := newSuspStore(t)
	ctx := context.Background()

	if err := s.PutSuspension(ctx, "sess_1", "run_1", []byte(`{"n":1}`)); err != nil {
		t.Fatalf("put 1: %v", err)
	}
	if err := s.PutSuspension(ctx, "sess_1", "run_1", []byte(`{"n":2}`)); err != nil {
		t.Fatalf("put 2: %v", err)
	}
	runID, payload, _, err := s.GetSuspension(ctx, "sess_1")
	if err != nil || runID != "run_1" || string(payload) != `{"n":2}` {
		t.Fatalf("get = %q %q err=%v", runID, payload, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "sess_1", "suspended"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("suspended dir has %d files, want 1", len(entries))
	}

	if err := s.PutSuspension(ctx, "sess_2", "run_2", []byte(`{"other":1}`)); err != nil {
		t.Fatalf("put other session: %v", err)
	}
	if runID, _, _, _ := s.GetSuspension(ctx, "sess_2"); runID != "run_2" {
		t.Fatalf("sess_2 runID = %q", runID)
	}
}

// 多文件并存（崩溃残留形态）取字典序最大 = 最新；残留临时文件被忽略
func TestSuspensionStorePicksLatest(t *testing.T) {
	s, root := newSuspStore(t)
	ctx := context.Background()

	if err := s.PutSuspension(ctx, "sess_1", "run_0192aaa", []byte(`{"old":1}`)); err != nil {
		t.Fatalf("put old: %v", err)
	}
	if err := s.PutSuspension(ctx, "sess_1", "run_0192zzz", []byte(`{"new":1}`)); err != nil {
		t.Fatalf("put new: %v", err)
	}
	// 残留临时文件（写入中途崩溃形态）：加载时忽略
	dir := filepath.Join(root, "sess_1", "suspended")
	if err := os.WriteFile(filepath.Join(dir, ".susp-tmp-999.json"), []byte("partial"), 0o600); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}

	runID, payload, _, err := s.GetSuspension(ctx, "sess_1")
	if err != nil || runID != "run_0192zzz" || string(payload) != `{"new":1}` {
		t.Fatalf("get = %q %q err=%v", runID, payload, err)
	}
}

// 含路径成分的编号按接线错误拒绝（同会话与运行双侧）
func TestSuspensionStoreRejectsPathIDs(t *testing.T) {
	s, _ := newSuspStore(t)
	ctx := context.Background()
	for _, id := range []string{"../escape", "a/b", "..", "."} {
		if err := s.PutSuspension(ctx, id, "run_1", []byte(`{}`)); err == nil {
			t.Fatalf("put session %q: error = nil", id)
		}
		if err := s.PutSuspension(ctx, "sess_1", id, []byte(`{}`)); err == nil {
			t.Fatalf("put run %q: error = nil", id)
		}
	}
}

// 空载荷视为调用方错误
func TestSuspensionStoreRejectsEmptyPayload(t *testing.T) {
	s, _ := newSuspStore(t)
	if err := s.PutSuspension(context.Background(), "sess_1", "run_1", nil); err == nil {
		t.Fatal("put empty payload: error = nil")
	}
}

// 内存实现镜像磁盘语义：覆盖、清理、无记录
func TestMemorySuspensionStore(t *testing.T) {
	m := store.NewMemorySuspensionStore()
	ctx := context.Background()
	if err := m.PutSuspension(ctx, "s1", "r1", []byte(`{}`)); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := m.PutSuspension(ctx, "s1", "r1", []byte(`{"v":2}`)); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	runID, payload, found, err := m.GetSuspension(ctx, "s1")
	if err != nil || !found || runID != "r1" || string(payload) != `{"v":2}` {
		t.Fatalf("get = %q %q found=%v err=%v", runID, payload, found, err)
	}
	if err := m.DeleteSuspension(ctx, "s1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, found, _ := m.GetSuspension(ctx, "s1"); found {
		t.Fatal("found after delete")
	}
}
