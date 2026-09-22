package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 在数据目录手造一个中间坏行的会话文件（带换行的坏行 = 真损坏），
// 供列表容错测试使用
func writeCorruptSessionFile(t *testing.T, root, id string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	content := "{\"v\":1,\"type\":\"session\",\"id\":\"" + id + "\",\"createdAt\":\"" +
		diskNow.Format(time.RFC3339Nano) + "\"}\n" +
		"{not json\n" +
		"{\"type\":\"message\",\"data\":{\"id\":\"m1\",\"sessionId\":\"" + id + "\",\"role\":\"user\",\"content\":\"x\",\"createdAt\":\"" +
		diskNow.Format(time.RFC3339Nano) + "\"}}\n"
	if err := os.WriteFile(filepath.Join(dir, "session.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatalf("write corrupt session: %v", err)
	}
}

// 列表应按最近活跃降序汇总各会话：消息数、首问、推导时间与 GetSession /
// ListMessages 同口径；坏会话跳过不 fatal；本进程新建会话（未重开）也在列
func TestDiskStoreListSessions(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	s, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// a：user 首问 + assistant 回复（最后活跃最晚，排第一）
	newDiskSessionWithMessages(t, s, "sess_a", "why is demo-api down", "checked pods")
	// 追加一条更晚的 assistant 消息拉开活跃时间
	err = s.AppendMessage(ctx, &session.Message{
		ID: "sess_a-msg-z", SessionID: "sess_a", Role: session.RoleAssistant,
		Content: "final", CreatedAt: diskNow.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	// b：仅 assistant 消息（无 user 首问，活跃时间居中）
	err = s.CreateSession(ctx, &session.Session{ID: "sess_b", CreatedAt: diskNow.Add(-30 * time.Minute), UpdatedAt: diskNow})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	err = s.AppendMessage(ctx, &session.Message{
		ID: "sess_b-msg-z", SessionID: "sess_b", Role: session.RoleAssistant,
		Content: "answer", CreatedAt: diskNow.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	// c：空会话（最近活跃 = 创建时间，排最后）
	err = s.CreateSession(ctx, &session.Session{ID: "sess_c", CreatedAt: diskNow.Add(-time.Hour), UpdatedAt: diskNow})
	if err != nil {
		t.Fatalf("create c: %v", err)
	}

	// 本进程新建会话（未重开）出现在列表
	live, err := s.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list live: %v", err)
	}
	if len(live) != 3 {
		t.Fatalf("want 3 live sessions, got %d", len(live))
	}

	// 重开后（冷路径）同样可见；另造坏会话与空壳目录验证容错
	writeCorruptSessionFile(t, root, "sess_bad")
	err = os.MkdirAll(filepath.Join(root, "sess_husk"), 0o700)
	if err != nil {
		t.Fatalf("mkdir husk: %v", err)
	}
	reopened, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	summaries, err := reopened.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(summaries) != 3 {
		t.Fatalf("bad/husk sessions must be skipped, want 3 summaries, got %d", len(summaries))
	}

	wantIDs := []string{"sess_a", "sess_b", "sess_c"}
	for i, sum := range summaries {
		if sum.ID != wantIDs[i] {
			t.Fatalf("order[%d]: want %s, got %s (recent activity first)", i, wantIDs[i], sum.ID)
		}
		got, err := reopened.GetSession(ctx, sum.ID)
		if err != nil {
			t.Fatalf("get %s: %v", sum.ID, err)
		}
		if !sum.UpdatedAt.Equal(got.UpdatedAt) {
			t.Fatalf("%s: updated %v != GetSession %v", sum.ID, sum.UpdatedAt, got.UpdatedAt)
		}
		msgs, err := reopened.ListMessages(ctx, sum.ID)
		if err != nil {
			t.Fatalf("messages %s: %v", sum.ID, err)
		}
		if sum.MessageCount != len(msgs) {
			t.Fatalf("%s: count %d != ListMessages %d", sum.ID, sum.MessageCount, len(msgs))
		}
	}

	if summaries[0].FirstQuestion != "why is demo-api down" {
		t.Fatalf("a: first question %q", summaries[0].FirstQuestion)
	}
	if summaries[1].FirstQuestion != "" {
		t.Fatalf("b: no user message, want empty first question, got %q", summaries[1].FirstQuestion)
	}
}

// 空数据目录：列表为空且不报错
func TestDiskStoreListSessionsEmpty(t *testing.T) {
	s, err := store.NewDiskStore(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	summaries, err := s.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("want no summaries, got %d", len(summaries))
	}
}
