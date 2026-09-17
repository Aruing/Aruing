package store

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/tools"
)

// 声明实现关系：磁盘 spool 存储满足 tools 层定义的存取边界
var _ tools.SpoolStore = (*DiskSpoolStore)(nil)

// 写入流往返：Commit 后引用可 Open 读回全量内容，文件落在会话 spool/ 目录
func TestDiskSpoolStoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	s, err := NewDiskSpoolStore(context.Background(), root)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	ctx := context.Background()
	f, err := s.Create(ctx, "sess_a")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	content := "line-0\nline-1\nline-2"
	if _, werr := io.WriteString(f, content); werr != nil {
		t.Fatalf("write: %v", err)
	}
	ref, err := f.Commit()
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !strings.HasPrefix(ref, spoolFinalPrefix) {
		t.Fatalf("ref = %q, want %s* prefix", ref, spoolFinalPrefix)
	}

	rc, err := s.Open(ctx, "sess_a", ref)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != content {
		t.Fatalf("content = %q, want %q", got, content)
	}
	// 会话目录成形：<root>/<sid>/spool/<ref>
	if _, err := os.Stat(filepath.Join(root, "sess_a", "spool", ref)); err != nil {
		t.Fatalf("spool file missing: %v", err)
	}
}

// Abort 放弃未提交内容：会话 spool 目录无残留文件
func TestDiskSpoolStoreAbort(t *testing.T) {
	root := t.TempDir()
	s, err := NewDiskSpoolStore(context.Background(), root)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	f, err := s.Create(context.Background(), "sess_b")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, werr := io.WriteString(f, "partial"); werr != nil {
		t.Fatalf("write: %v", err)
	}
	f.Abort()

	entries, err := os.ReadDir(filepath.Join(root, "sess_b", "spool"))
	if err != nil {
		t.Fatalf("list spool dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("spool dir after abort = %v, want empty", entries)
	}
}

// 编号守卫：会话与引用含路径成分或空白时拒绝，不触盘
func TestDiskSpoolStorePathGuard(t *testing.T) {
	s, err := NewDiskSpoolStore(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	ctx := context.Background()
	for _, sid := range []string{"", "  ", "../escape", "a/b"} {
		if _, err := s.Create(ctx, sid); err == nil {
			t.Fatalf("create with session %q should fail", sid)
		}
		if _, err := s.Open(ctx, sid, "spool-x"); err == nil {
			t.Fatalf("open with session %q should fail", sid)
		}
	}
	if _, err := s.Open(ctx, "sess_a", ""); err == nil {
		t.Fatal("open with empty ref should fail")
	}
	if _, err := s.Open(ctx, "sess_a", "../escape"); err == nil {
		t.Fatal("open with path ref should fail")
	}
}

// 打开不存在的引用明确报错（文件缺失可判别，调用侧据此引导重新查询）
func TestDiskSpoolStoreOpenMissing(t *testing.T) {
	s, err := NewDiskSpoolStore(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if _, err := s.Open(context.Background(), "sess_a", "spool-none"); err == nil {
		t.Fatal("open missing ref should fail")
	}
}
