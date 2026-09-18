package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 在临时数据目录落一个带首问的会话（不依赖 LLM，直接走磁盘存储）
func writeSessionsFixture(t *testing.T, dir, id, firstQuestion string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.NewDiskStore(ctx, dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	if err := s.CreateSession(ctx, &session.Session{ID: id, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.AppendMessage(ctx, &session.Message{
		ID: id + "-m1", SessionID: id, Role: session.RoleUser,
		Content: firstQuestion, CreatedAt: now,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.AppendMessage(ctx, &session.Message{
		ID: id + "-m2", SessionID: id, Role: session.RoleAssistant,
		Content: "reply", CreatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// 表格视图：含表头、会话编号、折叠后的首问预览与截断标注；
// 标准错误承载生效数据目录
func TestRunSessionsTable(t *testing.T) {
	dir := t.TempDir()
	long := "line one\nline two  " + strings.Repeat("x", 80)
	writeSessionsFixture(t, dir, "sess_tbl", long)

	var out, errOut bytes.Buffer
	if err := runSessions([]string{"--data-dir", dir}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "SESSION") || !strings.Contains(out.String(), "sess_tbl") {
		t.Fatalf("table missing header or id:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "line one line two") {
		t.Fatalf("preview should fold newlines and spaces:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "…") {
		t.Fatalf("long first question should be truncated with ellipsis:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "data dir: "+dir) {
		t.Fatalf("stderr should print data dir:\n%s", errOut.String())
	}
}

// json 视图：首问全文不截断，字段为 snake_case
func TestRunSessionsJSON(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("问", 60)
	writeSessionsFixture(t, dir, "sess_json", long)

	var out, errOut bytes.Buffer
	if err := runSessions([]string{"--data-dir", dir, "--format", "json"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v\n%s", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0]["first_question"] != long {
		t.Fatalf("json first_question should be full text")
	}
	if _, ok := rows[0]["message_count"]; !ok {
		t.Fatalf("json should use snake_case fields: %v", rows[0])
	}
}

// 空数据目录：表格模式打印人读提示；json 模式输出合法空数组（机器契约），
// 人读提示不落在 stdout 污染机器消费
func TestRunSessionsEmpty(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := runSessions([]string{"--data-dir", t.TempDir()}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "no sessions found") {
		t.Fatalf("empty state message missing:\n%s", out.String())
	}

	var jsonOut, jsonErrOut bytes.Buffer
	if err := runSessions([]string{"--data-dir", t.TempDir(), "--format", "json"}, &jsonOut, &jsonErrOut); err != nil {
		t.Fatalf("run json: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(jsonOut.Bytes(), &rows); err != nil {
		t.Fatalf("json empty state must be a valid empty array: %v\n%s", err, jsonOut.String())
	}
	if len(rows) != 0 {
		t.Fatalf("want empty array, got %d rows", len(rows))
	}
}

// 非法格式与多余位置参数明确报错
func TestRunSessionsBadArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := runSessions([]string{"--data-dir", t.TempDir(), "--format", "yaml"}, &out, &errOut); err == nil {
		t.Fatalf("unknown format should error")
	}
	if err := runSessions([]string{"--data-dir", t.TempDir(), "extra"}, &out, &errOut); err == nil {
		t.Fatalf("positional arg should error")
	}
}
