package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/tools"
)

// 实现 Slicer 的假工具，供 evidence.read 走通路径
type sliceableFake struct {
	name string
	cols []string
	rows [][]string
}

func (f *sliceableFake) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        f.name,
		Description: "fake slicer",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func (f *sliceableFake) Execute(context.Context, json.RawMessage) (*core.Evidence, error) {
	return &core.Evidence{ToolName: f.name, Summary: "unused", Raw: json.RawMessage(`{}`)}, nil
}

func (f *sliceableFake) Slice(raw []byte, q tools.SliceQuery) (tools.SliceView, error) {
	if len(raw) == 0 {
		return tools.SliceView{}, errors.New("empty raw")
	}
	end := q.Offset + q.Limit
	if end > len(f.rows) {
		end = len(f.rows)
	}
	var page [][]string
	if q.Offset < end {
		page = f.rows[q.Offset:end]
	}
	return tools.SliceView{
		Total:   len(f.rows),
		Offset:  q.Offset,
		Limit:   q.Limit,
		Columns: f.cols,
		Rows:    page,
	}, nil
}

type nonSliceFake struct{ name string }

func (f *nonSliceFake) Spec() tools.ToolSpec {
	return tools.ToolSpec{Name: f.name, Description: "d", InputSchema: json.RawMessage(`{"type":"object"}`)}
}
func (f *nonSliceFake) Execute(context.Context, json.RawMessage) (*core.Evidence, error) {
	return &core.Evidence{ToolName: f.name, Raw: json.RawMessage(`{}`)}, nil
}

// 正常：索引命中 + Slicer → 摘要含分页元信息与本页行
func TestEvidenceReadSlicesTable(t *testing.T) {
	idx := tools.NewObservationIndex()
	reg := tools.NewRegistry()
	fake := &sliceableFake{
		name: "fake.table",
		cols: []string{"NAME", "STATUS"},
		rows: [][]string{
			{"p0", "Running"},
			{"p1", "Error"},
			{"p2", "Pending"},
		},
	}
	if err := reg.Register(fake); err != nil {
		t.Fatalf("register: %v", err)
	}
	idx.Put("e_abc", tools.ObsRecord{Raw: json.RawMessage(`{"ok":true}`), ToolName: "fake.table"})

	tool, err := tools.NewEvidenceReadTool(idx, reg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ev, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"evidenceId": "e_abc",
		"offset":     1,
		"limit":      1,
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if ev.Error != "" {
		t.Fatalf("unexpected error field: %s", ev.Error)
	}
	if ev.ToolName != "evidence.read" {
		t.Fatalf("toolName: %s", ev.ToolName)
	}
	if !strings.Contains(ev.Summary, "total=3") || !strings.Contains(ev.Summary, "offset=1") {
		t.Fatalf("summary missing page meta:\n%s", ev.Summary)
	}
	if !strings.Contains(ev.Summary, "p1") || !strings.Contains(ev.Summary, "Error") {
		t.Fatalf("summary missing page row:\n%s", ev.Summary)
	}
	// 导航结果不得回写索引
	if _, ok := idx.Get("e_nav"); ok {
		t.Fatal("must not put nav result into index")
	}
}

// 非表格切片（Columns 为空）走行渲染：带绝对行号，超长行截断标注，元信息含 total/offset
func TestEvidenceReadSlicesLines(t *testing.T) {
	idx := tools.NewObservationIndex()
	reg := tools.NewRegistry()
	long := strings.Repeat("x", 300)
	fake := &sliceableFake{
		name: "fake.logs",
		cols: nil,
		rows: [][]string{
			{"2026-08-14T01:00:00Z starting"},
			{""},
			{long},
			{"2026-08-14T01:00:02Z ready"},
		},
	}
	if err := reg.Register(fake); err != nil {
		t.Fatalf("register: %v", err)
	}
	idx.Put("e_logs", tools.ObsRecord{Raw: json.RawMessage(`{"ok":true}`), ToolName: "fake.logs"})

	tool, err := tools.NewEvidenceReadTool(idx, reg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ev, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"evidenceId": "e_logs",
		"offset":     2,
		"limit":      2,
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if ev.Error != "" {
		t.Fatalf("unexpected error field: %s", ev.Error)
	}
	if !strings.Contains(ev.Summary, "total=4") || !strings.Contains(ev.Summary, "offset=2") {
		t.Fatalf("summary missing page meta:\n%s", ev.Summary)
	}
	// 行号从 offset 起计，超长行截断并计数标注
	if !strings.Contains(ev.Summary, "2: "+long[:240]+"…") || !strings.Contains(ev.Summary, "3: 2026-08-14T01:00:02Z ready") {
		t.Fatalf("summary missing numbered lines:\n%s", ev.Summary)
	}
	if !strings.Contains(ev.Summary, "1 行超长截断") {
		t.Fatalf("summary missing truncation note:\n%s", ev.Summary)
	}
	// 全长行不得整段进入摘要
	if strings.Contains(ev.Summary, long) {
		t.Fatal("untruncated long line leaked into summary")
	}
}

// 未知 evidenceId → 业务错误写在 Evidence.Error，非语言错误
func TestEvidenceReadUnknownID(t *testing.T) {
	idx := tools.NewObservationIndex()
	reg := tools.NewRegistry()
	tool, err := tools.NewEvidenceReadTool(idx, reg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ev, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"evidenceId": "e_missing",
		"offset":     0,
		"limit":      10,
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if ev.Error == "" || !strings.Contains(ev.Error, "不在本轮") {
		t.Fatalf("want missing-id error, got %q", ev.Error)
	}
}

// 源工具不支持 Slicer
func TestEvidenceReadNonSlicer(t *testing.T) {
	idx := tools.NewObservationIndex()
	reg := tools.NewRegistry()
	if err := reg.Register(&nonSliceFake{name: "fake.noslice"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	idx.Put("e_1", tools.ObsRecord{Raw: json.RawMessage(`{}`), ToolName: "fake.noslice"})
	tool, err := tools.NewEvidenceReadTool(idx, reg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ev, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"evidenceId": "e_1",
		"offset":     0,
		"limit":      5,
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(ev.Error, "不支持切片") {
		t.Fatalf("want non-slicer message, got %q", ev.Error)
	}
}

// 构造依赖缺失
func TestNewEvidenceReadToolRequiresDeps(t *testing.T) {
	if _, err := tools.NewEvidenceReadTool(nil, tools.NewRegistry()); err == nil {
		t.Fatal("expected nil index error")
	}
	if _, err := tools.NewEvidenceReadTool(tools.NewObservationIndex(), nil); err == nil {
		t.Fatal("expected nil registry error")
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// 支持时间窗的假 Slicer：按行首 RFC3339 前缀过滤（模拟 k8s --timestamps 行为），回填窗内首末时间戳
type timeWindowFake struct {
	name string
	rows [][]string
}

func (f *timeWindowFake) Spec() tools.ToolSpec {
	return tools.ToolSpec{Name: f.name, Description: "fake time slicer", InputSchema: json.RawMessage(`{"type":"object"}`)}
}

func (f *timeWindowFake) Execute(context.Context, json.RawMessage) (*core.Evidence, error) {
	return &core.Evidence{ToolName: f.name, Raw: json.RawMessage(`{}`)}, nil
}

func (f *timeWindowFake) Slice(raw []byte, q tools.SliceQuery) (tools.SliceView, error) {
	kept := f.rows
	first, last := "", ""
	if q.Since != "" || q.Until != "" {
		kept = nil
		for _, r := range f.rows {
			line := r[0]
			sp := strings.IndexByte(line, ' ')
			if sp <= 0 {
				continue
			}
			ts := line[:sp]
			if (q.Since != "" && ts < q.Since) || (q.Until != "" && ts > q.Until) {
				continue
			}
			if first == "" {
				first = ts
			}
			last = ts
			kept = append(kept, r)
		}
	}
	end := q.Offset + q.Limit
	if end > len(kept) {
		end = len(kept)
	}
	var page [][]string
	if q.Offset < end {
		page = kept[q.Offset:end]
	}
	return tools.SliceView{
		Total: len(kept), Offset: q.Offset, Limit: q.Limit, Rows: page,
		WindowFirst: first, WindowLast: last,
	}, nil
}

// 时间窗参数透传给源 Slicer：meta 含时间窗与窗内首末时间戳，翻页相对过滤结果集
func TestEvidenceReadTimeWindow(t *testing.T) {
	idx := tools.NewObservationIndex()
	reg := tools.NewRegistry()
	fake := &timeWindowFake{name: "fake.logs", rows: [][]string{
		{"2026-08-15T01:00:00Z boot"},
		{"2026-08-15T01:00:05Z warn"},
		{"2026-08-15T01:00:10Z crash"},
	}}
	if err := reg.Register(fake); err != nil {
		t.Fatalf("register: %v", err)
	}
	idx.Put("e_ts", tools.ObsRecord{Raw: json.RawMessage(`{}`), ToolName: "fake.logs"})

	tool, err := tools.NewEvidenceReadTool(idx, reg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ev, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"evidenceId": "e_ts",
		"offset":     0,
		"limit":      10,
		"since":      "2026-08-15T01:00:00Z",
		"until":      "2026-08-15T01:00:10Z",
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if ev.Error != "" {
		t.Fatalf("unexpected error field: %s", ev.Error)
	}
	for _, want := range []string{
		"total=3", "since=2026-08-15T01:00:00Z", "until=2026-08-15T01:00:10Z",
		"窗内首行=2026-08-15T01:00:00Z", "末行=2026-08-15T01:00:10Z",
	} {
		if !strings.Contains(ev.Summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, ev.Summary)
		}
	}
}

// 非法 RFC3339 时间参数直接拒绝；不传时间窗时零回归（纯位置切片不带时间 meta）
func TestEvidenceReadTimeWindowArgs(t *testing.T) {
	idx := tools.NewObservationIndex()
	reg := tools.NewRegistry()
	fake := &timeWindowFake{name: "fake.logs", rows: [][]string{{"2026-08-15T01:00:00Z boot"}}}
	if err := reg.Register(fake); err != nil {
		t.Fatalf("register: %v", err)
	}
	idx.Put("e_ts", tools.ObsRecord{Raw: json.RawMessage(`{}`), ToolName: "fake.logs"})
	tool, err := tools.NewEvidenceReadTool(idx, reg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if _, err = tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"evidenceId": "e_ts", "offset": 0, "limit": 5, "since": "2026-08-15 01:00:00",
	})); err == nil || !strings.Contains(err.Error(), "RFC3339") {
		t.Fatalf("want invalid since error, got: %v", err)
	}

	ev, err := tool.Execute(context.Background(), mustJSON(t, map[string]any{
		"evidenceId": "e_ts", "offset": 0, "limit": 5,
	}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(ev.Summary, "时间窗") {
		t.Fatalf("plain slice must not carry time meta:\n%s", ev.Summary)
	}
}
