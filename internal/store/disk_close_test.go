package store_test

// DiskStore 生命周期收口（P2-3）：关闭幂等、关后可再写（按需重开）、内容不受影响。

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 关闭幂等；关闭后再追加消息按需重开文件，历史全量可读
func TestDiskStoreCloseLifecycle(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	d, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	now := time.Now().UTC()
	err = d.CreateSession(ctx, &session.Session{ID: "sess_c", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	m1 := session.Message{ID: "m1", SessionID: "sess_c", Role: "user", Content: "第一句", CreatedAt: now}
	err = d.AppendMessage(ctx, &m1)
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	// 关闭幂等
	err = d.Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	err = d.Close()
	if err != nil {
		t.Fatalf("second close: %v", err)
	}

	// 关后追加：句柄已清，按需重开文件续写
	m2 := session.Message{ID: "m2", SessionID: "sess_c", Role: "user", Content: "第二句", CreatedAt: now}
	err = d.AppendMessage(ctx, &m2)
	if err != nil {
		t.Fatalf("append after close: %v", err)
	}
	msgs, err := d.ListMessages(ctx, "sess_c")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Content != "第一句" || msgs[1].Content != "第二句" {
		t.Fatalf("messages after reopen: %+v", msgs)
	}

	// 重启等价：新实例读同一数据目录，全量一致
	d2, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	msgs2, err := d2.ListMessages(ctx, "sess_c")
	if err != nil || len(msgs2) != 2 {
		t.Fatalf("reopen messages: %+v err=%v", msgs2, err)
	}
	err = d2.Close()
	if err != nil {
		t.Fatalf("close d2: %v", err)
	}

	// 会话文件在盘上成形
	if _, err := os.Stat(filepath.Join(root, "sess_c", "session.jsonl")); err != nil {
		t.Fatalf("session file: %v", err)
	}
}
