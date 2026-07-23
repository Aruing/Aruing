package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"aruing/internal/core"
	"aruing/internal/tools"
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

// 标准路径：猜想与任务回填系统编号，局部 hypothesis ref 映射到任务 Refs
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

	plan, err := planner.Plan(context.Background(), testPlanQuery(), testPlanTargets())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Hypotheses) != 1 || len(plan.Tasks) != 1 {
		t.Fatalf("size = %d hyp, %d tasks", len(plan.Hypotheses), len(plan.Tasks))
	}
	h := plan.Hypotheses[0]
	if !strings.HasPrefix(h.ID, "h_") {
		t.Errorf("hypothesis ID = %q", h.ID)
	}
	if h.RunID != "run_1" || h.Statement != "后端 Pod 未就绪" {
		t.Errorf("hypothesis = %+v", h)
	}
	if h.CreatedAt.IsZero() {
		t.Error("hypothesis CreatedAt should not be zero")
	}
	if len(h.ExpectedSignals) != 1 || h.ExpectedSignals[0] != "Pod 未 Ready" {
		t.Errorf("signals = %+v", h.ExpectedSignals)
	}

	task := plan.Tasks[0]
	if !strings.HasPrefix(task.ID, "t_") {
		t.Errorf("task ID = %q", task.ID)
	}
	if task.RunID != "run_1" || task.ToolName != "fake.list_pods" {
		t.Errorf("task = %+v", task)
	}
	if task.Purpose != "检查 Pod" {
		t.Errorf("purpose = %q", task.Purpose)
	}
	// h1 应被替换为系统 h_ id；其余透传
	foundH, foundTarget, foundNode, foundQuery := false, false, false, false
	for _, ref := range task.Refs {
		switch {
		case ref == h.ID:
			foundH = true
		case ref == "target_1":
			foundTarget = true
		case ref == "node_1":
			foundNode = true
		case ref == "query_1":
			foundQuery = true
		case ref == "h1":
			t.Errorf("task refs still contain local hyp ref: %v", task.Refs)
		}
	}
	if !foundH || !foundTarget || !foundNode || !foundQuery {
		t.Errorf("task refs incomplete: %v", task.Refs)
	}
	var args map[string]any
	if err := json.Unmarshal(task.Arguments, &args); err != nil || args["namespace"] != "default" {
		t.Errorf("arguments = %s", task.Arguments)
	}
}

// depends_on 局部 task ref 应映射为系统 task id
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
	plan, err := planner.Plan(context.Background(), testPlanQuery(), testPlanTargets())
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

// 未知工具应触发业务重试并最终 ErrLLMOutputInconsistent
func TestLLMPlannerUnknownTool(t *testing.T) {
	body := `{
		"hypotheses":[{"ref":"h1","statement":"x","reason":"y","expected_signals":[]}],
		"tasks":[{"ref":"t1","tool_name":"not.a.tool","arguments":{},"purpose":"p","refs":["h1"],"depends_on":[]}]
	}`
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeChatCompletion(w, body)
	})
	planner, err := NewLLMPlanner(client, newTestFactory(t), testPlannerSpecs())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = planner.Plan(context.Background(), testPlanQuery(), testPlanTargets())
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Fatalf("error = %v, want ErrLLMOutputInconsistent", err)
	}
	if calls.Load() != maxPlanAttempts {
		t.Errorf("calls = %d, want %d", calls.Load(), maxPlanAttempts)
	}
}

// 任务引用未知数据应拒绝
func TestLLMPlannerUnknownRef(t *testing.T) {
	body := `{
		"hypotheses":[{"ref":"h1","statement":"x","reason":"y","expected_signals":[]}],
		"tasks":[{"ref":"t1","tool_name":"k8s","arguments":{"argv":[]},"purpose":"p","refs":["target_missing"],"depends_on":[]}]
	}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	planner, err := NewLLMPlanner(client, newTestFactory(t), testPlannerSpecs())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = planner.Plan(context.Background(), testPlanQuery(), testPlanTargets())
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Fatalf("error = %v", err)
	}
}

// 重复 hypothesis ref 应拒绝
func TestLLMPlannerDuplicateHypRef(t *testing.T) {
	body := `{
		"hypotheses":[
			{"ref":"h1","statement":"a","reason":"","expected_signals":[]},
			{"ref":"h1","statement":"b","reason":"","expected_signals":[]}
		],
		"tasks":[{"ref":"t1","tool_name":"k8s","arguments":{},"purpose":"p","refs":["h1"],"depends_on":[]}]
	}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	planner, err := NewLLMPlanner(client, newTestFactory(t), testPlannerSpecs())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = planner.Plan(context.Background(), testPlanQuery(), testPlanTargets())
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Fatalf("error = %v", err)
	}
}

// 校验失败后下一次合规输出应成功（业务重试）
func TestLLMPlannerRetryThenOK(t *testing.T) {
	bad := `{"hypotheses":[],"tasks":[]}`
	good := `{
		"hypotheses":[{"ref":"h1","statement":"ok","reason":"r","expected_signals":[]}],
		"tasks":[{"ref":"t1","tool_name":"fake.list_pods","arguments":{"namespace":"default"},"purpose":"p","refs":["h1"],"depends_on":[]}]
	}`
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			writeChatCompletion(w, bad)
			return
		}
		writeChatCompletion(w, good)
	})
	planner, err := NewLLMPlanner(client, newTestFactory(t), testPlannerSpecs())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	plan, err := planner.Plan(context.Background(), testPlanQuery(), testPlanTargets())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Hypotheses) != 1 {
		t.Fatalf("hypotheses = %d", len(plan.Hypotheses))
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

// 系统 prompt 必须注入 Specs，且 New 时复制快照
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
		t.Errorf("prompt missing tool names: %s", planner.prompt[:min(200, len(planner.prompt))])
	}
	// 修改调用方 specs 不应影响已构造实例的校验集合
	specs[0].Name = "mutated"
	_, err = planner.Plan(context.Background(), testPlanQuery(), testPlanTargets())
	if err != nil {
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

// 外键 Target 的 RunID 不匹配应拒绝
func TestLLMPlannerForeignTarget(t *testing.T) {
	body := `{
		"hypotheses":[{"ref":"h1","statement":"x","reason":"y","expected_signals":[]}],
		"tasks":[{"ref":"t1","tool_name":"k8s","arguments":{},"purpose":"p","refs":["h1"],"depends_on":[]}]
	}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	planner, err := NewLLMPlanner(client, newTestFactory(t), testPlannerSpecs())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	targets := []core.Target{{ID: "target_x", RunID: "other_run", NodeID: "node_1"}}
	_, err = planner.Plan(context.Background(), testPlanQuery(), targets)
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Fatalf("error = %v", err)
	}
}
