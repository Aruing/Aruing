package agent_test

import (
	"context"
	"github.com/Aruing/Aruing/internal/agent"
	"strings"
	"testing"
	"time"

	"github.com/Aruing/Aruing/internal/agent/agenttest"
	"github.com/Aruing/Aruing/internal/core"
)

// 假定位器应按问题节点生成目标，节点编号来自输入而非模板固定值
func TestFakeResolverNext(t *testing.T) {
	resolver := agenttest.NewFakeResolver([]core.Target{{
		ID:     "stale_target",
		RunID:  "stale_run",
		NodeID: "stale_node",
		Type:   "k8s.resource",
		Attrs: map[string]string{
			"k8s.kind":      "Service",
			"k8s.namespace": "helloworld",
			"k8s.name":      "demo",
		},
		CreatedAt: time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC),
	}})
	state := agent.ResolveState{
		Query: core.Query{
			ID:    "query_test",
			RunID: "run_test",
			Nodes: []core.Node{{
				ID:   "node_from_parser",
				Type: "resource",
				Text: "demo",
			}},
		},
		MaxRounds: 8,
	}

	action, err := resolver.Next(context.Background(), state)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if action.Action != agent.ResolveActionSubmitTargets {
		t.Fatalf("action = %q, want submit_targets", action.Action)
	}
	if len(action.Targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(action.Targets))
	}
	target := action.Targets[0]
	if target.NodeID != "node_from_parser" {
		t.Errorf("NodeID = %q, want node_from_parser (L-1)", target.NodeID)
	}
	if target.Type != "k8s.resource" {
		t.Errorf("Type = %q, want k8s.resource from template", target.Type)
	}
	if target.Attrs["k8s.namespace"] != "helloworld" {
		t.Errorf("attrs = %+v", target.Attrs)
	}

	// 修改返回属性不能污染后续调用的模板
	action.Targets[0].Attrs["k8s.namespace"] = "changed"
	again, err := resolver.Next(context.Background(), state)
	if err != nil {
		t.Fatalf("next again: %v", err)
	}
	if again.Targets[0].Attrs["k8s.namespace"] != "helloworld" {
		t.Errorf("template mutated: %+v", again.Targets[0].Attrs)
	}
}

// 多节点应各自生成目标，节点编号与输入顺序一一对应
func TestFakeResolverMultiNode(t *testing.T) {
	resolver := agenttest.NewFakeResolver([]core.Target{
		{Type: "k8s.resource", Attrs: map[string]string{"k8s.name": "a"}},
		{Type: "k8s.resource", Attrs: map[string]string{"k8s.name": "b"}},
	})
	state := agent.ResolveState{
		Query: core.Query{
			RunID: "run_multi",
			Nodes: []core.Node{
				{ID: "node_a", Text: "a"},
				{ID: "node_b", Text: "b"},
			},
		},
	}

	action, err := resolver.Next(context.Background(), state)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if len(action.Targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(action.Targets))
	}
	if action.Targets[0].NodeID != "node_a" || action.Targets[1].NodeID != "node_b" {
		t.Errorf("targets = %+v", action.Targets)
	}
}

// 无节点时应失败，而不是提交空目标列表
func TestFakeResolverEmptyNodes(t *testing.T) {
	resolver := agenttest.NewFakeResolver(nil)
	action, err := resolver.Next(context.Background(), agent.ResolveState{
		Query: core.Query{RunID: "run_empty"},
	})
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if action.Action != agent.ResolveActionFail {
		t.Fatalf("action = %q, want fail", action.Action)
	}
	if !strings.Contains(action.Error, "no nodes") {
		t.Errorf("error = %q", action.Error)
	}
}
