package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 数据目录分派：空走内存实现，非空走磁盘实现（产品路径默认非空）
func TestOpenStoresDispatch(t *testing.T) {
	memStore, memLedger, err := openStores(context.Background(), "")
	if err != nil {
		t.Fatalf("open memory: %v", err)
	}
	if _, ok := memStore.(*store.MemoryStore); !ok {
		t.Fatalf("empty data dir should use memory store, got %T", memStore)
	}
	if _, ok := memLedger.(*store.MemoryRunLedger); !ok {
		t.Fatalf("empty data dir should use memory ledger, got %T", memLedger)
	}

	root := t.TempDir()
	diskStore, _, err := openStores(context.Background(), root)
	if err != nil {
		t.Fatalf("open disk: %v", err)
	}
	if _, ok := diskStore.(*store.DiskStore); !ok {
		t.Fatalf("non-empty data dir should use disk store, got %T", diskStore)
	}
	// 磁盘实现落盘可见：建会话后会话目录成形
	ctx := context.Background()
	if err := diskStore.CreateSession(ctx, &session.Session{ID: "sess_d", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sess_d", "session.jsonl")); err != nil {
		t.Fatalf("session file on disk: %v", err)
	}
}
