package agent_test

import (
	"context"
	"testing"

	"github.com/Aruing/Aruing/internal/agent"
	"github.com/Aruing/Aruing/internal/agent/agenttest"
	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/tools"
	"github.com/Aruing/Aruing/internal/tools/toolstest"
)

// LastRunStats 只读统计：未执行为零值；Execute 后调查轮数 >= 1；nil 接收者安全
func TestOrchestratorLastRunStats(t *testing.T) {
	var nilOrch *agent.Orchestrator
	if got := nilOrch.LastRunStats(); got.InvestigateRounds != 0 {
		t.Fatalf("nil 接收者应返回零值：%+v", got)
	}

	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register fake tool: %v", err)
	}
	parser := agenttest.NewFakeParser(core.Query{ID: "q1", Goal: "g", Nodes: []core.Node{{ID: "n1", Type: "resource", Text: "demo-api"}}})
	resolver := agenttest.NewFakeResolver([]core.Target{{Type: "k8s.resource", Attrs: map[string]string{"k8s.name": "demo-api"}}})
	planner := agenttest.NewFakePlanner(agent.Plan{
		Hypotheses: []core.Hypothesis{{ID: "h1", Statement: "pod 异常"}},
		Tasks:      []core.Task{{ID: "t1", Refs: []string{"h1"}, ToolName: "fake.list_pods"}},
	})
	verifier := agenttest.NewFakeVerifier([]core.Verdict{{ID: "v1", HypothesisID: "h1", Result: core.VerdictSupported, Reason: "ok"}})
	reporter := agenttest.NewFakeReporter(core.Report{ID: "rep", Title: "t", Conclusions: []core.Conclusion{{HypothesisID: "h1", Result: core.VerdictSupported, Reason: "ok"}}})

	orch := agent.NewOrchestrator(
		parser, resolver, planner,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		verifier, reporter, core.NewFactory(),
	)
	if got := orch.LastRunStats(); got.InvestigateRounds != 0 {
		t.Fatalf("未执行应为零值：%+v", got)
	}
	if _, err := orch.Execute(context.Background(), core.Run{ID: "run_stats", Question: "q"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := orch.LastRunStats(); got.InvestigateRounds < 1 {
		t.Fatalf("执行后调查轮数应 >= 1：%+v", got)
	}
}
