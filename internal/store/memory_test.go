package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"aruing/internal/session"
	"aruing/internal/store"
)

func TestMemoryStoreSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemoryStore()
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

	sess := &session.Session{ID: "sess_1", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetSession(ctx, "sess_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "sess_1" {
		t.Fatalf("id: %q", got.ID)
	}

	got.UpdatedAt = now.Add(time.Minute)
	if err := s.UpdateSession(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, err := s.GetSession(ctx, "sess_1")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if !again.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("updated at: %v", again.UpdatedAt)
	}
}

func TestMemoryStoreMessagesOrder(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemoryStore()
	now := time.Now().UTC()

	if err := s.CreateSession(ctx, &session.Session{ID: "sess_1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create: %v", err)
	}

	for i, content := range []string{"u1", "a1", "u2"} {
		msg := &session.Message{
			ID:        "msg_" + string(rune('a'+i)),
			SessionID: "sess_1",
			Role:      session.RoleUser,
			Content:   content,
			CreatedAt: now,
		}
		if err := s.AppendMessage(ctx, msg); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	msgs, err := s.ListMessages(ctx, "sess_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("len: %d", len(msgs))
	}
	if msgs[0].Content != "u1" || msgs[2].Content != "u2" {
		t.Fatalf("order: %+v", msgs)
	}
}

func TestMemoryStoreNotFound(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemoryStore()

	if _, err := s.GetSession(ctx, "missing"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("get: %v", err)
	}
	if err := s.AppendMessage(ctx, &session.Message{ID: "msg_1", SessionID: "missing"}); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("append: %v", err)
	}
	if _, err := s.ListMessages(ctx, "missing"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("list: %v", err)
	}
}
