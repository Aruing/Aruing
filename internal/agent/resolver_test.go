package agent

import (
	"context"
	"testing"
	"time"

	"aruing/internal/core"
)

// 已确认目标必须绑定当前运行并保留来源线索，才能安全进入后续诊断阶段
func TestFakeResolverResolve(t *testing.T) {
	resolver := NewFakeResolver([]core.Target{{
		ID:     "target_demo",
		RunID:  "stale_run",
		NodeID: "node_demo",
		Type:   "k8s.resource",
		Attrs: map[string]string{
			"k8s.kind":      "Service",
			"k8s.namespace": "helloworld",
			"k8s.name":      "demo",
		},
		EvidenceIDs: []string{"evidence_lookup"},
		CreatedAt:   time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC),
	}})
	query := core.Query{
		ID:    "query_test",
		RunID: "run_test",
		Nodes: []core.Node{{
			ID:   "node_demo",
			Type: "resource",
			Text: "demo",
		}},
	}

	got, err := resolver.Resolve(context.Background(), query)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("targets length = %d, want 1", len(got))
	}
	target := got[0]
	if target.RunID != query.RunID || target.NodeID != "node_demo" {
		t.Errorf("target relation was not preserved: %#v", target)
	}
	if target.Attrs["k8s.namespace"] != "helloworld" || target.EvidenceIDs[0] != "evidence_lookup" {
		t.Errorf("target identity was not preserved: %#v", target)
	}

	// 多次定位不能共享可变属性，否则一次结果可能污染后续运行
	got[0].Attrs["k8s.namespace"] = "changed"
	got[0].EvidenceIDs[0] = "changed"
	again, err := resolver.Resolve(context.Background(), query)
	if err != nil {
		t.Fatalf("resolve target again: %v", err)
	}
	if again[0].Attrs["k8s.namespace"] != "helloworld" || again[0].EvidenceIDs[0] != "evidence_lookup" {
		t.Errorf("target template was mutated: %#v", again[0])
	}
}

// 目标来源必须能在问题节点中找到，避免把无关资源带入诊断流程
func TestFakeResolverValidate(t *testing.T) {
	resolver := NewFakeResolver([]core.Target{{
		ID:     "target_demo",
		NodeID: "node_unknown",
	}})
	query := core.Query{
		ID:    "query_test",
		RunID: "run_test",
		Nodes: []core.Node{{ID: "node_demo"}},
	}

	if _, err := resolver.Resolve(context.Background(), query); err == nil {
		t.Fatal("resolve target: error = nil")
	}
}
