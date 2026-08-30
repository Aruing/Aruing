package agent_test

// 选择策略实验臂的编排级测试（步骤 4）：B4 恒选最低成本、B2 种子随机全程轨迹可复现；
// ours 行为不变由既有 acquire_loop 测试回归（TestAcquireLoopSupported 等）

import (
	"context"
	"testing"

	"github.com/Aruing/Aruing/internal/agent"
	"github.com/Aruing/Aruing/internal/agent/agenttest"
	"github.com/Aruing/Aruing/internal/core"
)

// 成本不对称的双动作决策：贵动作信息量更大（ours 会优先选），便宜动作弱区分
func costAsymmetricDecision() agent.PlanDecision {
	return agent.PlanDecision{
		Hypotheses: []core.Hypothesis{
			{ID: "h_1", Statement: "Pod 崩溃", Confidence: 0.5},
			{ID: "h_2", Statement: "选择器配错", Confidence: 0.5},
		},
		Actions: []agent.ActionProposal{
			{
				Name:     "check-pods",
				Argv:     []string{"get", "pods"},
				Purpose:  "看 Pod 状态",
				Cost:     5,
				Outcomes: []string{"crash", "running"},
				Matrix:   [][]float64{{0.8, 0.2}, {0.1, 0.9}},
			},
			{
				Name:     "check-events",
				Argv:     []string{"get", "events"},
				Purpose:  "看事件",
				Cost:     1,
				Outcomes: []string{"backoff", "none"},
				Matrix:   [][]float64{{0.85, 0.15}, {0.2, 0.8}},
			},
		},
	}
}

// 跑一次指定方法与种子的编排，返回证据链的命令视图序列（执行轨迹的可观测面）
func runAcquireArm(t *testing.T, method agent.AcquireMethod, seed int64) []string {
	t.Helper()
	decision := agenttest.NewFakeDecisionPlanner(costAsymmetricDecision())
	verifier := &recordingVerifier{FakeVerifier: agenttest.NewFakeVerifier([]core.Verdict{{
		ID: "v_1", HypothesisID: "h_1", Result: core.VerdictSupported, Reason: "证据支持",
	}})}
	executor := &scriptedExecutor{script: map[string]string{
		"pods":   "pod CrashLoopBackOff restarts 8",
		"events": "事件 BackOff 反复出现",
	}}
	orch := newAcquireOrchestrator(t, decision, verifier, executor)
	orch.SetAcquireMethod(method)
	orch.SetAcquireSeed(seed)

	outcome, err := orch.Execute(context.Background(), core.Run{ID: "run_1", Question: "demo-api 不可达"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	commands := make([]string, 0, len(outcome.Evidence))
	for _, ev := range outcome.Evidence {
		commands = append(commands, ev.CommandView)
	}
	return commands
}

// B4：首轮必执行最低成本动作（check-events），贵动作后执行
func TestAcquireArmB4CheapestPicksLowestCost(t *testing.T) {
	commands := runAcquireArm(t, agent.AcquireMethodB4Cheapest, 0)
	if len(commands) < 2 {
		t.Fatalf("evidence = %d, want >= 2（两动作都应执行）", len(commands))
	}
	if commands[0] != "kubectl get events" {
		t.Fatalf("首轮应选最低成本动作：got %q, want kubectl get events", commands[0])
	}
	if commands[1] != "kubectl get pods" {
		t.Fatalf("次轮应执行剩余动作：got %q", commands[1])
	}
}

// B2：同种子两次完整运行的执行轨迹一致（可复现）；信念演化确定 → 选择确定
func TestAcquireArmB2RandomReproducible(t *testing.T) {
	first := runAcquireArm(t, agent.AcquireMethodB2Random, 42)
	second := runAcquireArm(t, agent.AcquireMethodB2Random, 42)
	if len(first) == 0 {
		t.Fatal("无证据产出，实验臂未跑通")
	}
	if len(first) != len(second) {
		t.Fatalf("同种子轨迹长度不一致：%d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("同种子轨迹第 %d 步不一致：%q vs %q", i, first[i], second[i])
		}
	}
}
