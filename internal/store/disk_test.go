package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 固定时间基，便于断言最近活跃时间的推导
var diskNow = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

// 建会话并追加若干消息的公共路径
func newDiskSessionWithMessages(t *testing.T, s *store.DiskStore, id string, contents ...string) {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateSession(ctx, &session.Session{ID: id, CreatedAt: diskNow, UpdatedAt: diskNow}); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	for i, c := range contents {
		msg := &session.Message{
			ID:        id + "-msg-" + string(rune('a'+i)),
			SessionID: id,
			Role:      session.RoleUser,
			Content:   c,
			CreatedAt: diskNow.Add(time.Duration(i) * time.Minute),
		}
		if err := s.AppendMessage(ctx, msg); err != nil {
			t.Fatalf("append %s: %v", c, err)
		}
	}
}

// 落盘后重开应全量恢复：消息顺序、会话头、最近活跃时间推导一致
func TestDiskStoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	s, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	newDiskSessionWithMessages(t, s, "sess_a", "u1", "a1", "u2")

	reopened, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	msgs, err := reopened.ListMessages(ctx, "sess_a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "u1" || msgs[2].Content != "u2" {
		t.Fatalf("order broken: %q %q", msgs[0].Content, msgs[2].Content)
	}

	sess, err := reopened.GetSession(ctx, "sess_a")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	// 最近活跃时间由最后条目推导，不落盘
	want := diskNow.Add(2 * time.Minute)
	if !sess.UpdatedAt.Equal(want) {
		t.Fatalf("updated at: want %v, got %v", want, sess.UpdatedAt)
	}
	if !sess.CreatedAt.Equal(diskNow) {
		t.Fatalf("created at: want %v, got %v", diskNow, sess.CreatedAt)
	}
}

// 空会话重开后最近活跃时间退回创建时间
func TestDiskStoreEmptySessionUpdatedAt(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	newDiskSessionWithMessages(t, s, "sess_empty")

	sess, err := s.GetSession(ctx, "sess_empty")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !sess.UpdatedAt.Equal(diskNow) {
		t.Fatalf("empty session updated at: want created at %v, got %v", diskNow, sess.UpdatedAt)
	}
}

// 检查点消息（深层压缩交接）随消息流落盘并恢复
func TestDiskStoreCheckpointMessageSurvives(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	newDiskSessionWithMessages(t, s, "sess_cp", "u1")
	cp := &session.Message{
		ID:        "sess_cp-cp",
		SessionID: "sess_cp",
		Role:      session.RoleAssistant,
		Content:   "checkpoint summary",
		CreatedAt: diskNow.Add(time.Minute),
		Mode:      session.ModeCheckpoint,
	}
	if cpErr := s.AppendMessage(ctx, cp); cpErr != nil {
		t.Fatalf("append checkpoint: %v", err)
	}

	reopened, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	msgs, err := reopened.ListMessages(ctx, "sess_cp")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 2 || msgs[1].Mode != session.ModeCheckpoint {
		t.Fatalf("checkpoint message lost: %+v", msgs)
	}
}

// 末行解析失败按断电残留半行容忍跳过；中间坏行启动报错并指明文件与行号
func TestDiskStoreCorruptLineHandling(t *testing.T) {
	ctx := context.Background()

	t.Run("truncated tail skipped", func(t *testing.T) {
		root := t.TempDir()
		s, _ := store.NewDiskStore(ctx, root)
		newDiskSessionWithMessages(t, s, "sess_t", "u1", "a1")
		path := filepath.Join(root, "sess_t", "session.jsonl")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatalf("open append: %v", err)
		}
		// 模拟断电半行：无换行结尾的残缺 JSON
		if _, wErr := f.WriteString(`{"type":"message","data":{"id":"x`); wErr != nil {
			t.Fatalf("write half line: %v", wErr)
		}
		f.Close()

		reopened, err := store.NewDiskStore(ctx, root)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		msgs, err := reopened.ListMessages(ctx, "sess_t")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(msgs) != 2 {
			t.Fatalf("want 2 messages after tail truncation, got %d", len(msgs))
		}
	})

	// 完整（带换行）但损坏的末行：非断电撕裂形态（撕裂不会落盘末尾换行），
	// 按真损坏报错，不得静默跳过后截掉（#18，pr-agent #145 R3 发现）
	t.Run("corrupt complete tail line errors", func(t *testing.T) {
		root := t.TempDir()
		s, _ := store.NewDiskStore(ctx, root)
		newDiskSessionWithMessages(t, s, "sess_c", "u1")
		path := filepath.Join(root, "sess_c", "session.jsonl")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatalf("open append: %v", err)
		}
		if _, wErr := f.WriteString("{broken" + "\n"); wErr != nil {
			t.Fatalf("write corrupt line: %v", wErr)
		}
		f.Close()

		reopened, err := store.NewDiskStore(ctx, root)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		_, err = reopened.ListMessages(ctx, "sess_c")
		if err == nil {
			t.Fatal("want error on corrupt complete tail line")
		}
		if !strings.Contains(err.Error(), "session.jsonl:3") {
			t.Fatalf("error should name file and line, got: %v", err)
		}
	})

	// 容忍的半行必须在追加前从盘上截掉：否则新行把半行顶成中间行，
	// 下次重开整个会话拒载（pr-agent #145 R2 发现的毒化路径）
	t.Run("append after truncated tail survives reload", func(t *testing.T) {
		root := t.TempDir()
		s, _ := store.NewDiskStore(ctx, root)
		newDiskSessionWithMessages(t, s, "sess_p", "u1", "a1")
		path := filepath.Join(root, "sess_p", "session.jsonl")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatalf("open append: %v", err)
		}
		if _, wErr := f.WriteString(`{"type":"message","data":{"id":"x`); wErr != nil {
			t.Fatalf("write half line: %v", wErr)
		}
		f.Close()

		// 重开后追加新消息（半行应被截掉，新行接在干净末尾）
		reopened, err := store.NewDiskStore(ctx, root)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		msg := &session.Message{
			ID:        "sess_p-m2",
			SessionID: "sess_p",
			Role:      session.RoleUser,
			Content:   "after crash",
			CreatedAt: diskNow.Add(3 * time.Minute),
		}
		err = reopened.AppendMessage(ctx, msg)
		if err != nil {
			t.Fatalf("append: %v", err)
		}

		// 再重开：会话仍可加载，旧两条 + 新一条，半行不存在
		again, err := store.NewDiskStore(ctx, root)
		if err != nil {
			t.Fatalf("reopen again: %v", err)
		}
		msgs, err := again.ListMessages(ctx, "sess_p")
		if err != nil {
			t.Fatalf("list after poison check: %v", err)
		}
		if len(msgs) != 3 || msgs[2].Content != "after crash" {
			t.Fatalf("want 3 messages with new tail, got %+v", msgs)
		}
	})

	t.Run("middle corrupt line errors", func(t *testing.T) {
		root := t.TempDir()
		s, _ := store.NewDiskStore(ctx, root)
		newDiskSessionWithMessages(t, s, "sess_m", "u1", "a1", "u2")
		path := filepath.Join(root, "sess_m", "session.jsonl")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		lines := strings.SplitN(string(data), "\n", 3)
		// 第二行（首条消息）替换为坏 JSON，保持行结构
		lines[1] = "{broken"
		if wErr := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); wErr != nil {
			t.Fatalf("rewrite: %v", wErr)
		}

		// 打开只扫目录名不读正文，坏行在读会话时暴露
		reopened, err := store.NewDiskStore(ctx, root)
		if err != nil {
			t.Fatalf("open should not read content: %v", err)
		}
		_, err = reopened.ListMessages(ctx, "sess_m")
		if err == nil {
			t.Fatal("want error on middle corrupt line")
		}
		if !strings.Contains(err.Error(), "session.jsonl:2") {
			t.Fatalf("error should name file and line, got: %v", err)
		}
	})
}

// 未知条目种类加载时跳过：新版本写入的条目不阻断旧进程（前向兼容）
func TestDiskStoreUnknownEntrySkipped(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, _ := store.NewDiskStore(ctx, root)
	newDiskSessionWithMessages(t, s, "sess_f", "u1")
	path := filepath.Join(root, "sess_f", "session.jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	if _, wErr := f.WriteString(`{"type":"future-thing","data":{"x":1}}` + "\n"); wErr != nil {
		t.Fatalf("write future entry: %v", wErr)
	}
	f.Close()

	reopened, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	msgs, err := reopened.ListMessages(ctx, "sess_f")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
}

// 打开是惰性的：不存在的会话目录不在已知集合，坏正文的会话在读它之前不暴露
func TestDiskStoreLazyOpen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, _ := store.NewDiskStore(ctx, root)
	newDiskSessionWithMessages(t, s, "sess_good", "u1")
	// 手造一个正文损坏的会话目录
	badDir := filepath.Join(root, "sess_bad")
	if err := os.MkdirAll(badDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "session.jsonl"), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	reopened, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("open must not read content: %v", err)
	}
	// 读健康会话不受坏会话影响（崩溃半径 = 单会话）
	msgs, err := reopened.ListMessages(ctx, "sess_good")
	if err != nil {
		t.Fatalf("list good: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	if _, err := reopened.GetSession(ctx, "sess_missing"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

// 编号含路径成分时读路径不可达：known 集合来自目录基名扫描，越界编号
// 在构造任何文件路径前即判未找到，数据根内外都不产生文件或目录
// （评审「--session 用户输入可路径穿越」声称的证伪钉板）
func TestDiskStoreTraversalIDsUnreachable(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	root := filepath.Join(base, "data")

	s, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, id := range []string{"../../escape", "..", "sub/../../escape"} {
		if _, getErr := s.GetSession(ctx, id); !errors.Is(getErr, session.ErrSessionNotFound) {
			t.Fatalf("get %q: want not found, got %v", id, getErr)
		}
		if _, listErr := s.ListMessages(ctx, id); !errors.Is(listErr, session.ErrSessionNotFound) {
			t.Fatalf("list %q: want not found, got %v", id, listErr)
		}
		msg := &session.Message{ID: "msg_x", SessionID: id, Role: session.RoleUser, Content: "c", CreatedAt: diskNow}
		if appErr := s.AppendMessage(ctx, msg); !errors.Is(appErr, session.ErrSessionNotFound) {
			t.Fatalf("append %q: want not found, got %v", id, appErr)
		}
	}
	// 逃逸目标与数据根内均无痕迹
	if _, statErr := os.Stat(filepath.Join(base, "escape")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("escape path created outside data root")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("data root should stay empty, entries=%d err=%v", len(entries), err)
	}
}

// 写入口拒绝含路径成分的编号：读路径已被索引门槛挡住（见穿越不可达测试），
// 此处收口写路径，防止未来调用方把外部字符串直接当编号传入越出数据根
func TestDiskStoreCreateSessionRejectsPathComponentID(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	root := filepath.Join(base, "data")

	s, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, id := range []string{"../../escape", "..", "sub/../../escape"} {
		createErr := s.CreateSession(ctx, &session.Session{ID: id, CreatedAt: diskNow, UpdatedAt: diskNow})
		if createErr == nil || !strings.Contains(createErr.Error(), "path components") {
			t.Fatalf("create %q: want path component rejection, got %v", id, createErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(base, "escape")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("escape path created outside data root")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("data root should stay empty, entries=%d err=%v", len(entries), err)
	}
}

// 空文件按会话未创建处理（断电只落了目录项与空文件的形态）
func TestDiskStoreEmptyFileAsMissing(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, _ := store.NewDiskStore(ctx, root)
	newDiskSessionWithMessages(t, s, "sess_x", "u1")
	path := filepath.Join(root, "sess_x", "session.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	reopened, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := reopened.GetSession(ctx, "sess_x"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("want not found for empty file, got %v", err)
	}
}

// 创建失败或断电中断残留的空壳会话目录按未创建读取；同号再建报已存在。
// 编号由 Factory 发放（UUIDv7），产品调用链中不存在同号重试，空壳只作为
// 孤儿目录留存、读取不可见（评审「同号编号永久卡死」声称的证伪钉板）
func TestDiskStoreHuskRecreate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	// 模拟头部写入失败/断电中断后的残留：目录在、会话文件为空
	husk := filepath.Join(root, "sess_x")
	if err := os.MkdirAll(husk, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(husk, "session.jsonl"), nil, 0o600); err != nil {
		t.Fatalf("touch: %v", err)
	}

	s, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, getErr := s.GetSession(ctx, "sess_x"); !errors.Is(getErr, session.ErrSessionNotFound) {
		t.Fatalf("husk should read as missing, got %v", getErr)
	}
	err = s.CreateSession(ctx, &session.Session{ID: "sess_x", CreatedAt: diskNow, UpdatedAt: diskNow})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

// 读出的消息是拷贝：改动返回切片不影响再读；重复建会话报错
func TestDiskStoreIsolationAndDuplicateCreate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := store.NewDiskStore(ctx, root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	newDiskSessionWithMessages(t, s, "sess_i", "u1")

	msgs, _ := s.ListMessages(ctx, "sess_i")
	msgs[0].Content = "mutated"
	again, _ := s.ListMessages(ctx, "sess_i")
	if again[0].Content != "u1" {
		t.Fatalf("isolation broken: %q", again[0].Content)
	}

	err = s.CreateSession(ctx, &session.Session{ID: "sess_i", CreatedAt: diskNow})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}
