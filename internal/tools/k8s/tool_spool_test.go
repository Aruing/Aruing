package k8s

// 超巨输出 spill（PR-a 捕获留存）：stdout 超内存上限时全量落盘、Raw 携带引用；
// 失败降级不废取证。测试用内存假 SpoolStore 驱动，不触真磁盘。

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/tools"
)

// 内存假 spool 存储：记录创建的写入流，可注入 Create 故障
type fakeSpoolStore struct {
	createErr error
	files     []*fakeSpoolFile
}

func (s *fakeSpoolStore) Create(_ context.Context, sessionID string) (tools.SpoolFile, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	f := &fakeSpoolFile{sessionID: sessionID}
	s.files = append(s.files, f)
	return f, nil
}

func (s *fakeSpoolStore) Open(_ context.Context, sessionID, ref string) (io.ReadCloser, error) {
	for _, f := range s.files {
		if f.sessionID == sessionID && f.ref == ref {
			return io.NopCloser(bytes.NewReader(f.buf.Bytes())), nil
		}
	}
	return nil, fmt.Errorf("no spool %s/%s", sessionID, ref)
}

type fakeSpoolFile struct {
	sessionID string
	buf       bytes.Buffer
	ref       string
	committed bool
	aborted   bool
}

func (f *fakeSpoolFile) Write(p []byte) (int, error) { return f.buf.Write(p) }

func (f *fakeSpoolFile) Commit() (string, error) {
	f.committed = true
	f.ref = fmt.Sprintf("spool-%d", len(f.sessionID))
	return f.ref, nil
}

func (f *fakeSpoolFile) Abort() { f.aborted = true }

// 超限输出：内存留截断内联，spool 留全量并带引用与全量统计；Summary 引导翻页
func TestToolExecuteSpillOversizedStdout(t *testing.T) {
	var lines []string
	for i := 0; i < 300; i++ {
		lines = append(lines, fmt.Sprintf("pod-%03d ready", i))
	}
	full := strings.Join(lines, "\n") + "\n"
	kubectl := writeFakeKubectlCat(t, full)
	spool := &fakeSpoolStore{}
	tool := mustNewTool(t, Config{
		KubectlPath:    kubectl,
		MaxStdoutBytes: 1024,
		Spool:          spool,
	})

	ctx := tools.WithSpoolScope(context.Background(), "sess_spill")
	evidence, err := tool.Execute(ctx, mustArgs(t, map[string]any{"argv": []string{"get", "pods"}}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	raw := decodeRaw(t, evidence.Raw)
	if !raw.StdoutTruncated {
		t.Fatal("stdout should be truncated at inline limit")
	}
	if len(raw.Stdout) != 1024 {
		t.Fatalf("inline stdout = %d bytes, want 1024", len(raw.Stdout))
	}
	if raw.StdoutSpool == nil {
		t.Fatal("stdoutSpool ref missing")
	}
	if raw.StdoutSpool.SessionID != "sess_spill" {
		t.Fatalf("spool session = %q", raw.StdoutSpool.SessionID)
	}
	if got := spool.files[0].buf.String(); got != full {
		t.Fatalf("spool content mismatch: len=%d want=%d", len(got), len(full))
	}
	if raw.StdoutSpool.TotalBytes != int64(len(full)) {
		t.Fatalf("TotalBytes = %d, want %d", raw.StdoutSpool.TotalBytes, len(full))
	}
	if raw.StdoutSpool.TotalLines != 300 {
		t.Fatalf("TotalLines = %d, want 300", raw.StdoutSpool.TotalLines)
	}
	if !spool.files[0].committed || spool.files[0].aborted {
		t.Fatal("spool file should be committed, not aborted")
	}
	if !strings.Contains(evidence.Summary, "evidence.read") {
		t.Fatalf("summary should guide paging, got: %s", evidence.Summary)
	}
}

// 未超限输出：内联完整；spill 开启时引用照带（盘上与内存同一全量）
func TestToolExecuteSpillUnderLimit(t *testing.T) {
	kubectl := writeFakeKubectlCat(t, "NAME  READY\nweb   1/1\n")
	spool := &fakeSpoolStore{}
	tool := mustNewTool(t, Config{
		KubectlPath:    kubectl,
		MaxStdoutBytes: 1024,
		Spool:          spool,
	})

	ctx := tools.WithSpoolScope(context.Background(), "sess_ok")
	evidence, err := tool.Execute(ctx, mustArgs(t, map[string]any{"argv": []string{"get", "pods"}}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	raw := decodeRaw(t, evidence.Raw)
	if raw.StdoutTruncated {
		t.Fatal("small output should not truncate")
	}
	if raw.StdoutSpool == nil {
		t.Fatal("spool ref should be present when spill enabled")
	}
	if strings.Contains(evidence.Summary, "超过保留上限") {
		t.Fatalf("summary should not mention truncation, got: %s", evidence.Summary)
	}
}

// spool 创建失败：静默降级为纯内存截断（旧语义），取证不失败
func TestToolExecuteSpillCreateFailure(t *testing.T) {
	full := strings.Repeat("x\n", 100)
	kubectl := writeFakeKubectlCat(t, full)
	spool := &fakeSpoolStore{createErr: errors.New("disk full")}
	tool := mustNewTool(t, Config{
		KubectlPath:    kubectl,
		MaxStdoutBytes: 64,
		Spool:          spool,
	})

	ctx := tools.WithSpoolScope(context.Background(), "sess_fail")
	evidence, err := tool.Execute(ctx, mustArgs(t, map[string]any{"argv": []string{"logs", "web"}}))
	if err != nil {
		t.Fatalf("execute should survive spool failure: %v", err)
	}

	raw := decodeRaw(t, evidence.Raw)
	if !raw.StdoutTruncated {
		t.Fatal("stdout should still be truncated")
	}
	if raw.StdoutSpool != nil {
		t.Fatal("no spool ref on degraded path")
	}
	if !strings.Contains(evidence.Summary, "残缺原文见 raw") {
		t.Fatalf("summary should keep legacy truncation note, got: %s", evidence.Summary)
	}
}

// ctx 无会话标注：不开 spill（编程式/旧测试路径行为不变）
func TestToolExecuteSpillWithoutScope(t *testing.T) {
	kubectl := writeFakeKubectlCat(t, "ok\n")
	spool := &fakeSpoolStore{}
	tool := mustNewTool(t, Config{
		KubectlPath: kubectl,
		Spool:       spool,
	})

	evidence, err := tool.Execute(context.Background(), mustArgs(t, map[string]any{"argv": []string{"get", "ns"}}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(spool.files) != 0 {
		t.Fatalf("spool create should not happen without scope, got %d files", len(spool.files))
	}
	if decodeRaw(t, evidence.Raw).StdoutSpool != nil {
		t.Fatal("no spool ref without scope")
	}
}

// 超时路径：spool 半文件被放弃，不留可被引用的残缺原带
func TestToolExecuteSpillAbortOnTimeout(t *testing.T) {
	kubectl := writeFakeKubectl(t, "#!/bin/sh\nprintf 'begin\\n'\nsleep 5\n")
	spool := &fakeSpoolStore{}
	tool := mustNewTool(t, Config{
		KubectlPath: kubectl,
		Spool:       spool,
	})

	ctx := tools.WithSpoolScope(context.Background(), "sess_t")
	_, err := tool.Execute(ctx, mustArgs(t, map[string]any{
		"argv":           []string{"logs", "-f", "web"},
		"timeoutSeconds": 1,
	}))
	if err == nil {
		t.Fatal("timeout should fail the call")
	}
	if len(spool.files) != 1 || !spool.files[0].aborted {
		t.Fatal("spool file should be aborted on timeout")
	}
}

// 预置内容文件 + cat 脚本：避免把大段输出写进 shell 脚本字符串
func writeFakeKubectlCat(t *testing.T, content string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl shell scripts require unix")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(out, []byte(content), 0o644); err != nil {
		t.Fatalf("write out file: %v", err)
	}
	return writeFakeKubectl(t, "#!/bin/sh\ncat \""+out+"\"\n")
}
