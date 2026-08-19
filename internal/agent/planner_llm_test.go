package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/tools"
)

func testPlannerSpecs() []tools.ToolSpec {
	return []tools.ToolSpec{{
		Name:        "fake.list_pods",
		Description: "list pods",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}, {
		Name:        "k8s",
		Description: "kubectl argv",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"argv":{"type":"array"}}}`),
	}}
}

func testPlanQuery() core.Query {
	return core.Query{
		ID:    "query_1",
		RunID: "run_1",
		Goal:  "定位 demo-api",
		Nodes: []core.Node{{ID: "node_1", Type: "resource", Text: "demo-api"}},
		Edges: []core.Edge{{ID: "edge_1", From: "node_1", To: "node_1", Type: "self"}},
	}
}

func testPlanTargets() []core.Target {
	return []core.Target{{
		ID:     "target_1",
		RunID:  "run_1",
		NodeID: "node_1",
		Type:   "k8s.resource",
		Attrs:  map[string]string{"k8s.name": "demo-api"},
	}}
}

// 标准路径：猜想与任务回填系统编号，局部猜想引用映射到任务引用
func TestLLMPlannerPlan(t *testing.T) {
	body := `{
		"hypotheses":[{
			"ref":"h1",
			"statement":"后端 Pod 未就绪",
			"reason":"服务不可访问时优先检查后端",
			"expected_signals":["Pod 未 Ready"]
		}],
		"tasks":[{
			"ref":"t1",
			"tool_name":"fake.list_pods",
			"arguments":{"namespace":"default"},
			"purpose":"检查 Pod",
			"refs":["target_1","h1","node_1","query_1"],
			"depends_on":[]
		}]
	}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	planner, err := NewLLMPlanner(client, newTestFactory(t), testPlannerSpecs())
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	plan, err := planner.Plan(context.Background(), PlanState{Query: testPlanQuery(), Targets: testPlanTargets()})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Hypotheses) != 1 || len(plan.Tasks) != 1 {
		t.Fatalf("size = %d hyp, %d tasks", len(plan.Hypotheses), len(plan.Tasks))
	}
	h, task := plan.Hypotheses[0], plan.Tasks[0]
	if !strings.HasPrefix(h.ID, "h_") || h.Statement != "后端 Pod 未就绪" {
		t.Errorf("hypothesis = %+v", h)
	}
	if !strings.HasPrefix(task.ID, "t_") || task.ToolName != "fake.list_pods" {
		t.Errorf("task = %+v", task)
	}
	// 局部猜想引用应被替换为系统猜想编号
	foundH := false
	for _, ref := range task.Refs {
		if ref == h.ID {
			foundH = true
		}
		if ref == "h1" {
			t.Errorf("task refs still contain local hyp ref: %v", task.Refs)
		}
	}
	if !foundH {
		t.Errorf("task refs missing mapped hyp id: %v", task.Refs)
	}
}

// 依赖关系中的局部任务引用应映射为系统任务编号
func TestLLMPlannerDependsOn(t *testing.T) {
	body := `{
		"hypotheses":[{"ref":"h1","statement":"x","reason":"y","expected_signals":[]}],
		"tasks":[
			{"ref":"t1","tool_name":"k8s","arguments":{"argv":["get","pods"]},"purpose":"a","refs":["h1"],"depends_on":[]},
			{"ref":"t2","tool_name":"k8s","arguments":{"argv":["logs","x"]},"purpose":"b","refs":["h1"],"depends_on":["t1"]}
		]
	}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	planner, err := NewLLMPlanner(client, newTestFactory(t), testPlannerSpecs())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	plan, err := planner.Plan(context.Background(), PlanState{Query: testPlanQuery(), Targets: testPlanTargets()})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Tasks) != 2 {
		t.Fatalf("tasks = %d", len(plan.Tasks))
	}
	if len(plan.Tasks[1].DependsOn) != 1 || plan.Tasks[1].DependsOn[0] != plan.Tasks[0].ID {
		t.Errorf("depends_on = %v, want [%s]", plan.Tasks[1].DependsOn, plan.Tasks[0].ID)
	}
}

// 非法模型输出触发业务重试并最终报输出不一致
func TestLLMPlannerInvalidOutput(t *testing.T) {
	// 代表几类校验：未知工具、未知引用、重复猜想引用、外键目标
	tests := []struct {
		name    string
		body    string
		targets []core.Target
	}{
		{
			name: "unknown tool",
			body: `{"hypotheses":[{"ref":"h1","statement":"x","reason":"y","expected_signals":[]}],"tasks":[{"ref":"t1","tool_name":"not.a.tool","arguments":{},"purpose":"p","refs":["h1"],"depends_on":[]}]}`,
		},
		{
			name: "unknown ref",
			body: `{"hypotheses":[{"ref":"h1","statement":"x","reason":"y","expected_signals":[]}],"tasks":[{"ref":"t1","tool_name":"k8s","arguments":{"argv":[]},"purpose":"p","refs":["target_missing"],"depends_on":[]}]}`,
		},
		{
			name: "duplicate hyp ref",
			body: `{"hypotheses":[{"ref":"h1","statement":"a","reason":"","expected_signals":[]},{"ref":"h1","statement":"b","reason":"","expected_signals":[]}],"tasks":[{"ref":"t1","tool_name":"k8s","arguments":{},"purpose":"p","refs":["h1"],"depends_on":[]}]}`,
		},
		{
			name:    "foreign target",
			body:    `{"hypotheses":[{"ref":"h1","statement":"x","reason":"y","expected_signals":[]}],"tasks":[{"ref":"t1","tool_name":"k8s","arguments":{},"purpose":"p","refs":["h1"],"depends_on":[]}]}`,
			targets: []core.Target{{ID: "target_x", RunID: "other_run", NodeID: "node_1"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := test.body
			client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeChatCompletion(w, body)
			})
			planner, err := NewLLMPlanner(client, newTestFactory(t), testPlannerSpecs())
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			targets := test.targets
			if targets == nil {
				targets = testPlanTargets()
			}
			_, err = planner.Plan(context.Background(), PlanState{Query: testPlanQuery(), Targets: targets})
			if !errors.Is(err, ErrLLMOutputInconsistent) {
				t.Fatalf("error = %v, want ErrLLMOutputInconsistent", err)
			}
		})
	}
}

// 校验失败后下一次合规输出应成功；持续违规耗尽重试
func TestLLMPlannerRetry(t *testing.T) {
	t.Run("then ok", func(t *testing.T) {
		bad := `{"hypotheses":[],"tasks":[]}`
		good := `{"hypotheses":[{"ref":"h1","statement":"ok","reason":"r","expected_signals":[]}],"tasks":[{"ref":"t1","tool_name":"fake.list_pods","arguments":{"namespace":"default"},"purpose":"p","refs":["h1"],"depends_on":[]}]}`
		var calls atomic.Int32
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				writeChatCompletion(w, bad)
				return
			}
			writeChatCompletion(w, good)
		})
		planner, err := NewLLMPlanner(client, newTestFactory(t), testPlannerSpecs())
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		plan, err := planner.Plan(context.Background(), PlanState{Query: testPlanQuery(), Targets: testPlanTargets()})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if len(plan.Hypotheses) != 1 || calls.Load() != 2 {
			t.Errorf("hypotheses=%d calls=%d", len(plan.Hypotheses), calls.Load())
		}
	})

	t.Run("exhausted", func(t *testing.T) {
		body := `{"hypotheses":[{"ref":"h1","statement":"x","reason":"y","expected_signals":[]}],"tasks":[{"ref":"t1","tool_name":"not.a.tool","arguments":{},"purpose":"p","refs":["h1"],"depends_on":[]}]}`
		var calls atomic.Int32
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			writeChatCompletion(w, body)
		})
		planner, err := NewLLMPlanner(client, newTestFactory(t), testPlannerSpecs())
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		_, err = planner.Plan(context.Background(), PlanState{Query: testPlanQuery(), Targets: testPlanTargets()})
		if !errors.Is(err, ErrLLMOutputInconsistent) {
			t.Fatalf("error = %v", err)
		}
		if calls.Load() != maxPlanAttempts {
			t.Errorf("calls = %d, want %d", calls.Load(), maxPlanAttempts)
		}
	})
}

// 系统提示词必须注入工具规格，且新建时复制快照
func TestLLMPlannerSpecsInPrompt(t *testing.T) {
	specs := testPlannerSpecs()
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"hypotheses":[{"ref":"h1","statement":"s","reason":"","expected_signals":[]}],"tasks":[{"ref":"t1","tool_name":"k8s","arguments":{"argv":["get","pods"]},"purpose":"p","refs":["h1"],"depends_on":[]}]}`)
	})
	planner, err := NewLLMPlanner(client, newTestFactory(t), specs)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if !strings.Contains(planner.prompt, "fake.list_pods") || !strings.Contains(planner.prompt, "k8s") {
		t.Errorf("prompt missing tool names")
	}
	// 修改调用方规格表不应影响已构造实例的校验集合
	specs[0].Name = "mutated"
	if _, err := planner.Plan(context.Background(), PlanState{Query: testPlanQuery(), Targets: testPlanTargets()}); err != nil {
		t.Fatalf("plan after mutate: %v", err)
	}
}

// 缺少依赖应在构造时报错
func TestNewLLMPlannerRequiresDeps(t *testing.T) {
	if _, err := NewLLMPlanner(nil, newTestFactory(t), nil); err == nil {
		t.Fatal("want client error")
	}
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{}`)
	})
	if _, err := NewLLMPlanner(client, nil, nil); err == nil {
		t.Fatal("want factory error")
	}
}

// 首轮不得在载荷中带证据与判决；空任务仅后续轮合法
func TestLLMPlannerRoundSemantics(t *testing.T) {
	t.Run("first round omits history", func(t *testing.T) {
		body := `{"hypotheses":[{"ref":"h1","statement":"x","reason":"y","expected_signals":[]}],"tasks":[{"ref":"t1","tool_name":"k8s","arguments":{},"purpose":"p","refs":["h1"],"depends_on":[]}]}`
		var captured string
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			captured = string(raw)
			writeChatCompletion(w, body)
		})
		planner, err := NewLLMPlanner(client, newTestFactory(t), testPlannerSpecs())
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		if _, err := planner.Plan(context.Background(), PlanState{
			Query: testPlanQuery(), Targets: testPlanTargets(),
		}); err != nil {
			t.Fatalf("plan: %v", err)
		}
		if strings.Contains(captured, `"evidence"`) || strings.Contains(captured, `"verdicts"`) {
			t.Errorf("first-round payload should omit evidence/verdicts")
		}
	})

	t.Run("round0 empty tasks fail", func(t *testing.T) {
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeChatCompletion(w, `{"hypotheses":[],"tasks":[]}`)
		})
		planner, err := NewLLMPlanner(client, newTestFactory(t), testPlannerSpecs())
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		_, err = planner.Plan(context.Background(), PlanState{Query: testPlanQuery(), Targets: testPlanTargets()})
		if !errors.Is(err, ErrLLMOutputInconsistent) {
			t.Fatalf("round-0 empty tasks should fail, got: %v", err)
		}
	})

	t.Run("follow-up empty tasks ok", func(t *testing.T) {
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeChatCompletion(w, `{"hypotheses":[],"tasks":[]}`)
		})
		planner, err := NewLLMPlanner(client, newTestFactory(t), testPlannerSpecs())
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		plan, err := planner.Plan(context.Background(), PlanState{
			Query:    testPlanQuery(),
			Targets:  testPlanTargets(),
			Evidence: []core.Evidence{{ID: "e_1", RunID: "run_1", TaskID: "t_old"}},
		})
		if err != nil {
			t.Fatalf("follow-up empty tasks should succeed: %v", err)
		}
		if len(plan.Tasks) != 0 {
			t.Errorf("tasks = %d, want 0", len(plan.Tasks))
		}
	})

	// 后续轮可引用前几轮已登记的猜想编号
	t.Run("follow-up prior hypothesis", func(t *testing.T) {
		body := `{"hypotheses":[],"tasks":[{"ref":"t1","tool_name":"k8s","arguments":{},"purpose":"p","refs":["h_prior"],"depends_on":[]}]}`
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeChatCompletion(w, body)
		})
		planner, err := NewLLMPlanner(client, newTestFactory(t), testPlannerSpecs())
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		plan, err := planner.Plan(context.Background(), PlanState{
			Query:    testPlanQuery(),
			Targets:  testPlanTargets(),
			Evidence: []core.Evidence{{ID: "e_1", RunID: "run_1", TaskID: "t_old"}},
			Verdicts: []core.Verdict{{ID: "v_1", RunID: "run_1", HypothesisID: "h_prior", Result: core.VerdictInsufficient}},
		})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if len(plan.Tasks) != 1 || plan.Tasks[0].Refs[0] != "h_prior" {
			t.Errorf("task ref not preserved: %#v", plan.Tasks)
		}
	})
}
