package k8s

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/tools"
)

// 全 RFC3339 行：since 闭区间过滤保留边界行，并回填窗内首末时间戳
func TestFilterTimeWindowSince(t *testing.T) {
	lines := []string{
		"2026-08-15T01:00:00Z boot",
		"2026-08-15T01:00:05Z warn",
		"2026-08-15T01:00:10Z crash",
	}
	kept, first, last, err := filterTimeWindow(lines, "2026-08-15T01:00:05Z", "")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(kept) != 2 || !strings.HasPrefix(kept[0], "2026-08-15T01:00:05") || !strings.HasPrefix(kept[1], "2026-08-15T01:00:10") {
		t.Fatalf("kept: %#v", kept)
	}
	if first != "2026-08-15T01:00:05Z" || last != "2026-08-15T01:00:10Z" {
		t.Fatalf("first/last: %q %q", first, last)
	}
}

// until 与 since+until 组合；全部行在窗外时返回空集且不报错
func TestFilterTimeWindowBounds(t *testing.T) {
	lines := []string{
		"2026-08-15T01:00:00Z a",
		"2026-08-15T01:00:05Z b",
		"2026-08-15T01:00:10Z c",
	}
	kept, _, _, err := filterTimeWindow(lines, "", "2026-08-15T01:00:05Z")
	if err != nil || len(kept) != 2 {
		t.Fatalf("until-only: kept=%#v err=%v", kept, err)
	}
	kept, _, _, err = filterTimeWindow(lines, "2026-08-15T01:00:01Z", "2026-08-15T01:00:09Z")
	if err != nil || len(kept) != 1 || !strings.HasPrefix(kept[0], "2026-08-15T01:00:05") {
		t.Fatalf("both: kept=%#v err=%v", kept, err)
	}
	kept, first, last, err := filterTimeWindow(lines, "2026-08-15T02:00:00Z", "")
	if err != nil || len(kept) != 0 || first != "" || last != "" {
		t.Fatalf("no match: kept=%#v first=%q last=%q err=%v", kept, first, last, err)
	}
}

// 混杂无时间戳行整体失败并引导（不静默丢行，#18）；无时间窗参数时不过滤
func TestFilterTimeWindowRejectsMixed(t *testing.T) {
	mixed := []string{
		"2026-08-15T01:00:00Z ok",
		"plain line without timestamp",
	}
	_, _, _, err := filterTimeWindow(mixed, "2026-08-15T00:00:00Z", "")
	if err == nil || !strings.Contains(err.Error(), "--timestamps") {
		t.Fatalf("want guidance error, got: %v", err)
	}
	if _, _, _, err = filterTimeWindow([]string{"2026-08-15T01:00:00Z bad-since"}, "not-rfc3339", ""); err == nil {
		t.Fatal("want invalid since error")
	}
}

// --timestamps 样例 raw：时间窗先过滤，offset/limit 在过滤结果集上开窗
func TestToolSliceTimeWindow(t *testing.T) {
	tool := mustNewTool(t, Config{})
	raw, _ := json.Marshal(resultRaw{
		ExitCode: 0,
		Stdout: "2026-08-15T01:00:00Z boot\n" +
			"2026-08-15T01:00:05Z warn\n" +
			"2026-08-15T01:00:10Z crash\n" +
			"2026-08-15T01:00:15Z restart\n",
	})
	view, err := tool.Slice(raw, tools.SliceQuery{Offset: 1, Limit: 1, Since: "2026-08-15T01:00:00Z", Until: "2026-08-15T01:00:10Z"})
	if err != nil {
		t.Fatalf("slice: %v", err)
	}
	// 过滤后 3 行，窗口取第 1 行（warn）；total/offset 均相对过滤结果集
	if view.Total != 3 || view.Offset != 1 || len(view.Rows) != 1 || !strings.HasPrefix(view.Rows[0][0], "2026-08-15T01:00:05") {
		t.Fatalf("view: %#v", view)
	}
	if len(view.Columns) != 0 {
		t.Fatalf("want nil columns, got %#v", view.Columns)
	}
	if view.WindowFirst != "2026-08-15T01:00:00Z" || view.WindowLast != "2026-08-15T01:00:10Z" {
		t.Fatalf("window bounds: %q %q", view.WindowFirst, view.WindowLast)
	}
}

// 表格输出遇时间窗：明确报错引导，不猜列的时间语义
func TestToolSliceTimeWindowRejectsTable(t *testing.T) {
	tool := mustNewTool(t, Config{})
	raw, _ := json.Marshal(resultRaw{ExitCode: 0, Stdout: "NAME  STATUS\na  Running\n"})
	_, err := tool.Slice(raw, tools.SliceQuery{Offset: 0, Limit: 10, Since: "2026-08-15T01:00:00Z", Until: ""})
	if err == nil || !strings.Contains(err.Error(), "表格输出不支持时间窗") {
		t.Fatalf("want table guidance error, got: %v", err)
	}
}

// 无时间戳的非表格输出带时间窗：走时间过滤并明确失败引导（区别于纯位置切片成功）
func TestToolSliceTimeWindowRejectsPlainLines(t *testing.T) {
	tool := mustNewTool(t, Config{})
	raw, _ := json.Marshal(resultRaw{ExitCode: 0, Stdout: "Name: demo-api\nStatus: Running\n"})
	_, err := tool.Slice(raw, tools.SliceQuery{Offset: 0, Limit: 10, Since: "2026-08-15T01:00:00Z", Until: ""})
	if err == nil || !strings.Contains(err.Error(), "--timestamps") {
		t.Fatalf("want guidance error, got: %v", err)
	}
}
