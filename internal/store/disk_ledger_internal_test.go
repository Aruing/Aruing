package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/session"
)

// 目录 fsync 在 rename 成功后失败：Put 报持久化降级错误，但记录已落位、
// 索引已更新——同号换会话重试仍被跨会话守卫拒绝，不留孤儿记录导致重启拒启
func TestDiskRunLedgerDirSyncFailureKeepsIndex(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	l, err := NewDiskRunLedger(ctx, root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	prev := dirSync
	dirSync = func(string) error { return errors.New("io error") }
	defer func() { dirSync = prev }()

	rec := session.DiagnosticRecord{RunID: "run_1", SessionID: "sess_a", Question: "q"}
	putErr := l.Put(ctx, rec)
	if putErr == nil || !strings.Contains(putErr.Error(), "sync runs dir") {
		t.Fatalf("put: want dir sync error, got %v", putErr)
	}
	// 记录已 rename 落位，索引可读回
	if _, getErr := l.Get(ctx, "run_1"); getErr != nil {
		t.Fatalf("get after degraded put: %v", getErr)
	}
	// 同号换会话重试被跨会话守卫拒绝（索引如实反映盘上事实）
	retryErr := l.Put(ctx, session.DiagnosticRecord{RunID: "run_1", SessionID: "sess_b", Question: "q"})
	if retryErr == nil || !strings.Contains(retryErr.Error(), "cross-session") {
		t.Fatalf("want cross-session rejection, got %v", retryErr)
	}
	// 盘上不存在第二会话目录
	if _, statErr := os.Stat(filepath.Join(root, "sess_b")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("sess_b should not exist on disk")
	}
}
