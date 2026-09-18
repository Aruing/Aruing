package k8s

// 盘读翻页（PR-b 跨页导航）：SliceSpool 从盘上全量流切页，可翻到内存内联
// 截断点之后。复用 tool_spool_test 的假 spool 存储与假 kubectl，不触真磁盘。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Aruing/Aruing/internal/tools"
)

// 走真实 Execute 捕获一次超限输出，返回证据 Raw 与盘上全量内容
func spillOnce(t *testing.T, lines int) (raw []byte, full string, spool *fakeSpoolStore) {
	t.Helper()
	var ls []string
	for i := 0; i < lines; i++ {
		ls = append(ls, fmt.Sprintf("pod-%03d ready", i))
	}
	full = strings.Join(ls, "\n") + "\n"
	kubectl := writeFakeKubectlCat(t, full)
	spool = &fakeSpoolStore{}
	tool := mustNewTool(t, Config{KubectlPath: kubectl, MaxStdoutBytes: 1024, Spool: spool})

	evidence, err := tool.Execute(tools.WithSpoolScope(context.Background(), "sess_slice"), mustArgs(t, map[string]any{"argv": []string{"get", "pods"}}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	rr := decodeRaw(t, evidence.Raw)
	if rr.StdoutSpool == nil {
		t.Fatal("spill ref missing")
	}
	return evidence.Raw, full, spool
}

// 打开盘上留存供 SliceSpool 读取
func openSpool(t *testing.T, spool *fakeSpoolStore, raw []byte) io.Reader {
	t.Helper()
	rr := decodeRaw(t, raw)
	r, err := spool.Open(context.Background(), rr.StdoutSpool.SessionID, rr.StdoutSpool.File)
	if err != nil {
		t.Fatalf("open spool: %v", err)
	}
	return r
}

// 截断点之后的行可读：行号与内联切片同一坐标系连续，total 覆盖全量
func TestToolSliceSpoolPagesBeyondTruncation(t *testing.T) {
	raw, full, spool := spillOnce(t, 300)
	tool := mustNewTool(t, Config{KubectlPath: "kubectl"}) // 仅用其 SliceSpool，不执行

	// 内联路径只能看到截断点前的行（对照：内存截断语义仍 lossy）
	inline, err := tool.Slice(raw, tools.SliceQuery{Offset: 0, Limit: 3})
	if err != nil {
		t.Fatalf("inline slice: %v", err)
	}
	if inline.Total == 300 {
		t.Fatal("inline total should be truncated-line count, not full")
	}

	view, err := tool.SliceSpool(raw, tools.SliceQuery{Offset: 100, Limit: 3}, openSpool(t, spool, raw))
	if err != nil {
		t.Fatalf("slice spool: %v", err)
	}
	if view.Total != 300 {
		t.Fatalf("total = %d, want 300", view.Total)
	}
	if len(view.Rows) != 3 || view.Columns != nil {
		t.Fatalf("rows=%d cols=%v, want 3 rows line-mode", len(view.Rows), view.Columns)
	}
	// 行号连续性：offset 100 的首行即全量第 100 行（0 基）内容
	want := strings.Split(strings.TrimRight(full, "\n"), "\n")[100]
	if view.Rows[0][0] != want {
		t.Fatalf("row100 = %q, want %q", view.Rows[0][0], want)
	}
}

// 时间窗：流式行首 RFC3339 过滤后开窗；total/offset 相对窗内行集，回填首末时间戳
func TestToolSliceSpoolTimeWindow(t *testing.T) {
	base := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	var ls []string
	for i := 0; i < 60; i++ {
		ts := base.Add(time.Duration(i) * time.Minute)
		ls = append(ls, ts.Format(time.RFC3339)+" log line "+fmt.Sprintf("%02d", i))
	}
	full := strings.Join(ls, "\n") + "\n"
	raw := marshalResultRaw(t, full, 1<<30, &tools.SpoolRef{
		SessionID: "sess_win", File: "spool-win", TotalBytes: int64(len(full)), TotalLines: 60,
	})
	spool := &fakeSpoolStore{}
	commitFakeSpool(t, spool, "sess_win", "spool-win", full)
	tool := mustNewTool(t, Config{KubectlPath: "kubectl"})

	since := base.Add(10 * time.Minute).Format(time.RFC3339)
	until := base.Add(20 * time.Minute).Format(time.RFC3339)
	view, err := tool.SliceSpool(raw, tools.SliceQuery{Offset: 0, Limit: 50, Since: since, Until: until}, openSpool(t, spool, raw))
	if err != nil {
		t.Fatalf("slice spool window: %v", err)
	}
	// 闭区间 [10,20] 分钟 → 11 行（10..20）
	if view.Total != 11 {
		t.Fatalf("windowed total = %d, want 11", view.Total)
	}
	if want := base.Add(10 * time.Minute).Format(time.RFC3339Nano); view.WindowFirst != want {
		t.Fatalf("WindowFirst = %s, want %s", view.WindowFirst, want)
	}
	if want := base.Add(20 * time.Minute).Format(time.RFC3339Nano); view.WindowLast != want {
		t.Fatalf("WindowLast = %s, want %s", view.WindowLast, want)
	}
	if len(view.Rows) != 11 || view.Rows[0][0] != ls[10] || view.Rows[10][0] != ls[20] {
		t.Fatalf("windowed rows wrong: first=%q last=%q", view.Rows[0][0], view.Rows[len(view.Rows)-1][0])
	}
}

// 时间窗遇无时间戳行：整体失败并引导（与内联过滤同语义，不静默丢行）
func TestToolSliceSpoolTimeWindowMixedLines(t *testing.T) {
	full := "2026-09-17T10:00:00Z a\nno-timestamp line\n2026-09-17T10:01:00Z b\n"
	raw := marshalResultRaw(t, full, 1<<30, &tools.SpoolRef{SessionID: "s", File: "spool-mix", TotalLines: 3})
	spool := &fakeSpoolStore{}
	commitFakeSpool(t, spool, "s", "spool-mix", full)
	tool := mustNewTool(t, Config{KubectlPath: "kubectl"})

	_, err := tool.SliceSpool(raw, tools.SliceQuery{Offset: 0, Limit: 5, Since: "2026-09-17T09:00:00Z"}, openSpool(t, spool, raw))
	if err == nil || !strings.Contains(err.Error(), "不可按时间窗切片") {
		t.Fatalf("want time-cursor guidance error, got %v", err)
	}
}

// 超长行不炸：不限行长，整行完整返回
func TestToolSliceSpoolLongLine(t *testing.T) {
	long := strings.Repeat("x", 300_000)
	full := "short-a\n" + long + "\nshort-b\n"
	raw := marshalResultRaw(t, full, 1<<30, &tools.SpoolRef{SessionID: "s", File: "spool-long", TotalLines: 3})
	spool := &fakeSpoolStore{}
	commitFakeSpool(t, spool, "s", "spool-long", full)
	tool := mustNewTool(t, Config{KubectlPath: "kubectl"})

	view, err := tool.SliceSpool(raw, tools.SliceQuery{Offset: 1, Limit: 1}, openSpool(t, spool, raw))
	if err != nil {
		t.Fatalf("slice spool: %v", err)
	}
	if len(view.Rows) != 1 || view.Rows[0][0] != long {
		t.Fatalf("long line wrong: len=%d want %d", len(view.Rows[0][0]), len(long))
	}
}

// offset 越过 total：空页，offset 钳到 total（与 summary.SliceRows 同语义）
func TestToolSliceSpoolOffsetBeyondTotal(t *testing.T) {
	raw, _, spool := spillOnce(t, 300)
	tool := mustNewTool(t, Config{KubectlPath: "kubectl"})

	view, err := tool.SliceSpool(raw, tools.SliceQuery{Offset: 10_000, Limit: 5}, openSpool(t, spool, raw))
	if err != nil {
		t.Fatalf("slice spool: %v", err)
	}
	if len(view.Rows) != 0 || view.Total != 300 || view.Offset != 300 {
		t.Fatalf("want empty clamped page, got rows=%d total=%d offset=%d", len(view.Rows), view.Total, view.Offset)
	}
}

// 行内容保真：仅剁一个 \n 终止符，行尾 \r 是内容不是终止符（与内联切片 Split("\n") 同语义，#19 投影不改写内容）
func TestToolSliceSpoolPreservesTrailingCR(t *testing.T) {
	full := "keep\r\nplain\ntail"
	raw := marshalResultRaw(t, full, 1<<30, &tools.SpoolRef{SessionID: "s", File: "spool-cr", TotalLines: 3})
	spool := &fakeSpoolStore{}
	commitFakeSpool(t, spool, "s", "spool-cr", full)
	tool := mustNewTool(t, Config{KubectlPath: "kubectl"})

	view, err := tool.SliceSpool(raw, tools.SliceQuery{Offset: 0, Limit: 3}, openSpool(t, spool, raw))
	if err != nil {
		t.Fatalf("slice spool: %v", err)
	}
	if len(view.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(view.Rows))
	}
	if view.Rows[0][0] != "keep\r" {
		t.Fatalf("CRLF 行内容应保留尾随 \r（与内联一致），got %q", view.Rows[0][0])
	}
	if view.Rows[2][0] != "tail" {
		t.Fatalf("EOF 无换行残留行 = %q, want tail", view.Rows[2][0])
	}
}

// Raw 无引用（防御）：仍可流式全扫计数切页
func TestToolSliceSpoolWithoutRefCounts(t *testing.T) {
	full := "a\nb\nc\n"
	raw := marshalResultRaw(t, full, 1<<30, nil)
	spool := &fakeSpoolStore{}
	commitFakeSpool(t, spool, "s", "spool-noref", full)
	tool := mustNewTool(t, Config{KubectlPath: "kubectl"})

	r, err := spool.Open(context.Background(), "s", "spool-noref")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	view, err := tool.SliceSpool(raw, tools.SliceQuery{Offset: 1, Limit: 2}, r)
	if err != nil {
		t.Fatalf("slice spool: %v", err)
	}
	if view.Total != 3 || len(view.Rows) != 2 || view.Rows[0][0] != "b" {
		t.Fatalf("want full-scan total 3 page [b c], got total=%d rows=%v", view.Total, view.Rows)
	}
}

// 组装 resultRaw JSON（测试专用，不经真实捕获）
func marshalResultRaw(t *testing.T, stdout string, maxInline int, ref *tools.SpoolRef) []byte {
	t.Helper()
	truncated := len(stdout) > maxInline
	inline := stdout
	if truncated {
		inline = stdout[:maxInline]
	}
	raw, err := json.Marshal(resultRaw{
		Argv:            []string{"logs", "p"},
		ExitCode:        0,
		Stdout:          inline,
		StdoutSpool:     ref,
		StdoutTruncated: truncated,
	})
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}
	return raw
}

// 向假存储提交一个已成形文件
func commitFakeSpool(t *testing.T, s *fakeSpoolStore, sessionID, ref, content string) {
	t.Helper()
	f := &fakeSpoolFile{sessionID: sessionID, ref: ref, committed: true}
	f.buf.WriteString(content)
	s.files = append(s.files, f)
}
