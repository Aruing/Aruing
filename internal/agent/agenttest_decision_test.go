package agent_test

import (
	"context"
	"testing"

	"github.com/Aruing/Aruing/internal/agent"
	"github.com/Aruing/Aruing/internal/agent/agenttest"
	"github.com/Aruing/Aruing/internal/core"
)

// 固定模板克隆返回：补运行绑定，模板本身不被调用改写（可重复使用）
func TestFakeDecisionPlanner(t *testing.T) {
	template := agent.PlanDecision{
		Hypotheses: []core.Hypothesis{{
			ID:         "h_1",
			Statement:  "选择器配错",
			Confidence: 0.6,
		}},
		Actions: []agent.ActionProposal{{
			Name:     "get-endpoints",
			Argv:     []string{"get", "endpoints", "demo-api"},
			Cost:     1,
			Outcomes: []string{"empty", "full"},
			Matrix:   [][]float64{{0.85}, {0.15}},
		}},
	}
	planner := agenttest.NewFakeDecisionPlanner(template)

	for i := 0; i < 2; i++ {
		decision, err := planner.PlanDecision(context.Background(), agent.PlanState{
			Query: core.Query{RunID: "run_9"},
		})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if len(decision.Hypotheses) != 1 || decision.Hypotheses[0].RunID != "run_9" {
			t.Fatalf("call %d hypotheses = %+v", i, decision.Hypotheses)
		}
		if decision.Hypotheses[0].Confidence != 0.6 {
			t.Errorf("call %d prior = %v, want 0.6", i, decision.Hypotheses[0].Confidence)
		}
		if len(decision.Actions) != 1 || decision.Actions[0].Name != "get-endpoints" {
			t.Fatalf("call %d actions = %+v", i, decision.Actions)
		}
	}

	// 模板不可被调用污染：假设编号与矩阵保持原样
	if template.Hypotheses[0].RunID != "" {
		t.Error("template mutated by calls")
	}
}

// 关键词表回放：命中假设回放脚本 (d,s)，未命中与无关假设回放 (0, 0)
func TestFakeVerifierJudgeStrength(t *testing.T) {
	verifier := agenttest.NewFakeVerifier(nil)
	verifier.StrengthRules = []agenttest.StrengthRule{
		{HypothesisID: "h_1", Keyword: "CrashLoopBackOff", Direction: 1, Strength: 0.9},
		{HypothesisID: "h_2", Keyword: "selector-mismatch", Direction: -1, Strength: 0.7},
	}
	hypotheses := []core.Hypothesis{
		{ID: "h_1", RunID: "run_1", Statement: "Pod 崩溃"},
		{ID: "h_2", RunID: "run_1", Statement: "选择器配错"},
	}
	evidence := core.Evidence{
		ID:      "ev_1",
		Summary: "pod demo-api-abc CrashLoopBackOff (restarts 12)",
	}

	judgements, err := verifier.JudgeStrength(context.Background(), evidence, hypotheses)
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	// h_1 命中关键词 → (+1, 0.9)；h_2 关键词不在证据文本 → (0, 0)
	if judgements[0].Direction != 1 || judgements[0].Strength != 0.9 {
		t.Errorf("h_1 judgement = %+v, want +1 0.9", judgements[0])
	}
	if judgements[1].Direction != 0 || judgements[1].Strength != 0 {
		t.Errorf("h_2 judgement = %+v, want 0 0", judgements[1])
	}
}
