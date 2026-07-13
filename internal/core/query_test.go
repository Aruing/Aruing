package core

import (
	"encoding/json"
	"testing"
	"time"
)

// 问题结构必须保留开放类型、扩展属性和关系方向，避免新增资源时修改核心字段
func TestQueryJSON(t *testing.T) {
	start := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	want := Query{
		ID:    "query_test",
		RunID: "run_test",
		Goal:  "定位 frontend 调用 backend 超时的原因",
		Nodes: []Node{
			{
				ID:   "node_frontend",
				Type: "future.resource",
				Text: "frontend",
				Attrs: map[string]string{
					"future.kind": "CustomWorkload",
				},
			},
			{
				ID:   "node_backend",
				Type: "resource",
				Text: "backend",
			},
		},
		Edges: []Edge{
			{
				ID:   "edge_calls",
				From: "node_frontend",
				To:   "node_backend",
				Type: "calls",
				Attrs: map[string]string{
					"symptom": "timeout",
				},
			},
		},
		TimeRange: &TimeRange{
			Since: "30m",
			Start: &start,
		},
		CreatedAt: time.Date(2026, 7, 13, 10, 30, 0, 0, time.UTC),
	}

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}

	var got Query
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal query: %v", err)
	}

	if got.RunID != want.RunID {
		t.Errorf("RunID = %q, want %q", got.RunID, want.RunID)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("Nodes length = %d, want 2", len(got.Nodes))
	}
	if got.Nodes[0].Type != "future.resource" || got.Nodes[0].Attrs["future.kind"] != "CustomWorkload" {
		t.Errorf("open node data was not preserved: %#v", got.Nodes[0])
	}
	if len(got.Edges) != 1 {
		t.Fatalf("Edges length = %d, want 1", len(got.Edges))
	}
	edge := got.Edges[0]
	if edge.ID != "edge_calls" || edge.From != "node_frontend" || edge.To != "node_backend" || edge.Type != "calls" {
		t.Errorf("edge identity or direction was not preserved: %#v", edge)
	}
	if got.TimeRange == nil {
		t.Fatal("time range was not preserved")
	}
	if got.TimeRange.Start == nil || !got.TimeRange.Start.Equal(start) {
		t.Errorf("time range start was not preserved: %#v", got.TimeRange.Start)
	}
	if got.TimeRange.End != nil {
		t.Errorf("time range end = %v, want nil", got.TimeRange.End)
	}
}

// 用户没有提供时间约束时不应生成空对象，避免后续模块误判为明确的时间范围
func TestQueryTimeRange(t *testing.T) {
	data, err := json.Marshal(Query{})
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}

	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("unmarshal query: %v", err)
	}
	if _, exists := fields["timeRange"]; exists {
		t.Errorf("timeRange should be omitted: %s", data)
	}
}
