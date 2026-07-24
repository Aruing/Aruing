package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"aruing/internal/core"
)

// 规划结果必须绑定当前运行并保留开放引用，才能同时支持不同问题和资源类型
func TestFakePlannerPlan(t *testing.T) {
	planner := NewFakePlanner(Plan{
		Hypotheses: []core.Hypothesis{{
			ID:              "h_demo",
			RunID:           "stale_run",
			Statement:       "服务没有可用后端",
			ExpectedSignals: []string{"端点列表为空"},
			CreatedAt:       time.Date(2026, 7, 14, 11, 0, 0, 0, time.UTC),
		}},
		Tasks: []core.Task{{
			ID:        "task_endpoints",
			RunID:     "stale_run",
			Refs:      []string{"query_test", "node_demo", "edge_demo", "target_demo", "h_demo"},
			ToolName:  "fake.list_pods",
			Arguments: json.RawMessage(`{"namespace":"helloworld"}`),
			Purpose:   "检查服务后端",
		}},
	})
	query := core.Query{
		ID:    "query_test",
		RunID: "run_test",
		Nodes: []core.Node{{ID: "node_demo"}},
		Edges: []core.Edge{{ID: "edge_demo", From: "node_demo", To: "node_demo"}},
	}
	targets := []core.Target{{ID: "target_demo", RunID: "run_test", NodeID: "node_demo"}}

	got, err := planner.Plan(context.Background(), PlanState{Query: query, Targets: targets})
	if err != nil {
		t.Fatalf("plan tasks: %v", err)
	}
	if len(got.Hypotheses) != 1 || len(got.Tasks) != 1 {
		t.Fatalf("plan size = %d hypotheses, %d tasks; want 1, 1", len(got.Hypotheses), len(got.Tasks))
	}
	if got.Hypotheses[0].RunID != query.RunID || got.Tasks[0].RunID != query.RunID {
		t.Errorf("plan was not bound to current run: %#v", got)
	}
	if len(got.Tasks[0].Refs) != 5 || got.Tasks[0].Refs[4] != "h_demo" {
		t.Errorf("task refs were not preserved: %#v", got.Tasks[0].Refs)
	}

	// 多次规划不能共享可变列表，否则一次结果可能污染后续运行
	got.Hypotheses[0].ExpectedSignals[0] = "changed"
	got.Tasks[0].Refs[0] = "changed"
	again, err := planner.Plan(context.Background(), PlanState{Query: query, Targets: targets})
	if err != nil {
		t.Fatalf("plan tasks again: %v", err)
	}
	if again.Hypotheses[0].ExpectedSignals[0] != "端点列表为空" || again.Tasks[0].Refs[0] != "query_test" {
		t.Errorf("plan template was mutated: %#v", again)
	}
}

// 任务只能引用本次规划已知的数据，避免证据回溯到不存在或其他运行的对象
func TestFakePlannerValidate(t *testing.T) {
	query := core.Query{ID: "query_test", RunID: "run_test"}
	tests := []struct {
		name    string
		plan    Plan
		targets []core.Target
	}{
		{
			name: "unknown ref",
			plan: Plan{Tasks: []core.Task{{
				ID:       "task_test",
				Refs:     []string{"target_unknown"},
				ToolName: "fake.list_pods",
			}}},
		},
		{
			name: "foreign target",
			plan: Plan{Tasks: []core.Task{{
				ID:       "task_test",
				Refs:     []string{"target_other"},
				ToolName: "fake.list_pods",
			}}},
			targets: []core.Target{{ID: "target_other", RunID: "run_other"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewFakePlanner(test.plan).Plan(context.Background(), PlanState{Query: query, Targets: test.targets}); err == nil {
				t.Fatal("plan tasks: error = nil")
			}
		})
	}
}
