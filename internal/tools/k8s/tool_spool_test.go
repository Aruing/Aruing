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

// 内存假 spool 存储：记录创建的写入流，可注入 Create 故障与盘中途写故障
type fakeSpoolStore struct {
	createErr error
	// 创建后回调（注入写故障用）
	onCreate func(*fakeSpoolFile)
	files    []*fakeSpoolFile
}

func (s *fakeSpoolStore) Create(_ context.Context, sessionID string) (tools.SpoolFile, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	f := &fakeSpoolFile{sessionID: sessionID}
	if s.onCreate != nil {
		s.onCreate(f)
	}
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
	// 非空时首次 Write 返回该错误（模拟盘中途故障）
	writeErr error
	// 非空时 Commit 返回该错误（模拟收尾故障）
	commitErr error
}

func (f *fakeSpoolFile) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		err := f.writeErr
		f.writeErr = nil
		return 0, err
	}
	return f.buf.Write(p)
}

func (f *fakeSpoolFile) Commit() (string, error) {
	if f.commitErr != nil {
		return "", f.commitErr
	}
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

// 提交失败：降级旧截断语义且盘上半文件被真清理（钉板 pr-agent R3：
// finalize 不能只标记不 Abort）
func TestToolExecuteSpillCommitFailureDegradation(t *testing.T) {
	full := strings.Repeat("x\n", 200)
	kubectl := writeFakeKubectlCat(t, full)
	spool := &fakeSpoolStore{}
	spool.onCreate = func(f *fakeSpoolFile) { f.commitErr = errors.New("dir sync down") }
	tool := mustNewTool(t, Config{
		KubectlPath:    kubectl,
		MaxStdoutBytes: 128,
		Spool:          spool,
	})

	ctx := tools.WithSpoolScope(context.Background(), "sess_cfail")
	evidence, err := tool.Execute(ctx, mustArgs(t, map[string]any{"argv": []string{"logs", "web"}}))
	if err != nil {
		t.Fatalf("execute must survive commit failure: %v", err)
	}

	raw := decodeRaw(t, evidence.Raw)
	if raw.StdoutSpool != nil {
		t.Fatal("no spool ref after commit failure")
	}
	if len(spool.files) != 1 || !spool.files[0].aborted || spool.files[0].committed {
		t.Fatal("failed spool file should be aborted (cleaned), not committed")
	}
	if !strings.Contains(evidence.Summary, "残缺原文见 raw") {
		t.Fatalf("summary should degrade to legacy note, got: %s", evidence.Summary)
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

// 盘写中途故障：写入错误不得传播到 exec 复制路径把取证打死（spill 失败族
// 第三分支，钉板 pr-agent R1）；内存内联不受影响，退回旧截断语义
func TestToolExecuteSpillWriteFailureDegradation(t *testing.T) {
	full := strings.Repeat("line\n", 50)
	kubectl := writeFakeKubectlCat(t, full)
	spool := &fakeSpoolStore{}
	spool.onCreate = func(f *fakeSpoolFile) { f.writeErr = errors.New("disk full mid-write") }
	tool := mustNewTool(t, Config{
		KubectlPath:    kubectl,
		MaxStdoutBytes: 1024,
		Spool:          spool,
	})

	ctx := tools.WithSpoolScope(context.Background(), "sess_wfail")
	evidence, err := tool.Execute(ctx, mustArgs(t, map[string]any{"argv": []string{"get", "pods"}}))
	if err != nil {
		t.Fatalf("execute must survive mid-write spool failure: %v", err)
	}

	raw := decodeRaw(t, evidence.Raw)
	if raw.Stdout != full {
		t.Fatalf("inline stdout corrupted by spool failure: len=%d want=%d", len(raw.Stdout), len(full))
	}
	if raw.StdoutSpool != nil {
		t.Fatal("no spool ref after mid-write failure")
	}
	if len(spool.files) != 1 || !spool.files[0].aborted || spool.files[0].committed {
		t.Fatal("failed spool file should be aborted, not committed")
	}
}

// stderr 截断而 stdout 完整：文案只报标准错误，不得误报 stdout 已落盘
// （钉板 pr-agent R2）
func TestToolExecuteSpillStderrOnlyTruncation(t *testing.T) {
	kubectl := writeFakeKubectl(t, "#!/bin/sh\nprintf 'ok\\n'\nprintf '"+strings.Repeat("e", 4096)+"' 1>&2\n")
	spool := &fakeSpoolStore{}
	tool := mustNewTool(t, Config{
		KubectlPath:    kubectl,
		MaxStdoutBytes: 1024,
		MaxStderrBytes: 128,
		Spool:          spool,
	})

	ctx := tools.WithSpoolScope(context.Background(), "sess_err")
	evidence, err := tool.Execute(ctx, mustArgs(t, map[string]any{"argv": []string{"logs", "web"}}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	raw := decodeRaw(t, evidence.Raw)
	if !raw.StderrTruncated || raw.StdoutTruncated {
		t.Fatalf("truncation flags: stderr=%v stdout=%v", raw.StderrTruncated, raw.StdoutTruncated)
	}
	if !strings.Contains(evidence.Summary, "标准错误已超过保留上限") {
		t.Fatalf("summary should mention stderr truncation, got: %s", evidence.Summary)
	}
	if strings.Contains(evidence.Summary, "已完整落盘") {
		t.Fatalf("summary must not claim spooled stdout when only stderr truncated, got: %s", evidence.Summary)
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
