package agent

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
)

// 标准路径：假设回填系统编号与运行绑定、先验保留，非法动作丢弃计数
func TestLLMDecisionPlannerPlanDecision(t *testing.T) {
	body := `{
		"hypotheses": [
			{"statement": "选择器配错", "reason": "路由断点优先", "expected_signals": [], "prior": 0.6},
			{"statement": "Pod 未就绪", "reason": "次优先", "expected_signals": [], "prior": 0.3}
		],
		"actions": [
			{"name": "get-endpoints", "argv": ["get", "endpoints", "demo-api", "-n", "demo"], "purpose": "看后端登记", "cost": 1, "outcomes": ["empty", "full"], "matrix": [[0.85, 0.15], [0.2, 0.8]]},
			{"name": "bad-matrix", "argv": ["get", "pods"], "purpose": "坏动作", "cost": 1, "outcomes": ["u", "v"], "matrix": [[0.5, 0.5]]}
		]
	}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	planner, err := NewLLMDecisionPlanner(client, newTestFactory(t), testPlannerSpecs())
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	decision, err := planner.PlanDecision(context.Background(), PlanState{
		Query:   testPlanQuery(),
		Targets: testPlanTargets(),
	})
	if err != nil {
		t.Fatalf("plan decision: %v", err)
	}
	if len(decision.Hypotheses) != 2 {
		t.Fatalf("hypotheses = %d, want 2", len(decision.Hypotheses))
	}
	for i, h := range decision.Hypotheses {
		if !strings.HasPrefix(h.ID, "h_") {
			t.Errorf("hypothesis[%d] id = %q, want h_ prefix", i, h.ID)
		}
		if h.RunID != "run_1" {
			t.Errorf("hypothesis[%d] runID = %q, want run_1", i, h.RunID)
		}
		if h.CreatedAt.IsZero() {
			t.Errorf("hypothesis[%d] createdAt zero", i)
		}
	}
	if decision.Hypotheses[0].Confidence != 0.6 {
		t.Errorf("prior lost: %v", decision.Hypotheses[0].Confidence)
	}
	if len(decision.Actions) != 1 || decision.Actions[0].Name != "get-endpoints" {
		t.Fatalf("kept actions = %+v", decision.Actions)
	}
	if decision.DroppedActions != 1 {
		t.Errorf("dropped = %d, want 1", decision.DroppedActions)
	}
}

// 计划级违规触发业务级重试：上限次仍不合规则返回模型输出不一致错误
func TestLLMDecisionPlannerRetry(t *testing.T) {
	var attempts atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		writeChatCompletion(w, `{"hypotheses": [{"statement": "a"}], "actions": []}`)
	})
	planner, err := NewLLMDecisionPlanner(client, newTestFactory(t), testPlannerSpecs())
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	_, err = planner.PlanDecision(context.Background(), PlanState{Query: testPlanQuery()})
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Fatalf("err = %v, want ErrLLMOutputInconsistent", err)
	}
	if got := attempts.Load(); got != int32(maxPlanAttempts) {
		t.Errorf("attempts = %d, want %d", got, maxPlanAttempts)
	}
}

// 构造依赖与运行前提：缺客户端 / 缺工厂 / 缺运行编号直接报错
func TestLLMDecisionPlannerRequires(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {})
	if _, err := NewLLMDecisionPlanner(nil, newTestFactory(t), testPlannerSpecs()); err == nil {
		t.Error("nil client: expected error")
	}
	if _, err := NewLLMDecisionPlanner(client, nil, testPlannerSpecs()); err == nil {
		t.Error("nil factory: expected error")
	}
	planner, err := NewLLMDecisionPlanner(client, newTestFactory(t), testPlannerSpecs())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := planner.PlanDecision(context.Background(), PlanState{Query: core.Query{RunID: " "}}); err == nil {
		t.Error("empty run ID: expected error")
	}
}
