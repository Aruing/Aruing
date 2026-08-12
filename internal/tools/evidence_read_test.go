package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"aruing/internal/core"
	"aruing/internal/tools"
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
