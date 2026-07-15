package agent

import (
	"context"
	"testing"

	"aruing/internal/core"
	"aruing/internal/tools"
)

// 完整假闭环必须实际执行任务并让最终报告引用生成的证据
func TestOrchestratorExecute(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(tools.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register fake tool: %v", err)
	}

	parser := NewFakeParser(core.Query{
		ID:   "query_demo",
		Goal: "定位 demo-api 无法访问的原因",
		Nodes: []core.Node{{
			ID:   "node_demo",
			Type: "resource",
			Text: "demo-api",
		}},
	})
	resolver := NewFakeResolver([]core.Target{{
		ID:     "target_demo",
		NodeID: "node_demo",
		Type:   "k8s.resource",
		Attrs: map[string]string{
			"k8s.kind":      "Deployment",
			"k8s.namespace": "default",
			"k8s.name":      "demo-api",
		},
	}})
	planner := NewFakePlanner(Plan{
		Hypotheses: []core.Hypothesis{{
			ID:        "h_pods",
			Statement: "后端 Pod 没有正常运行",
		}},
		Tasks: []core.Task{{
			ID:       "task_pods",
			Refs:     []string{"target_demo", "h_pods"},
			ToolName: "fake.list_pods",
		}},
	})
	verifier := NewFakeVerifier([]core.Verdict{{
		ID:           "verdict_pods",
		HypothesisID: "h_pods",
		Result:       core.VerdictSupported,
		Reason:       "Pod 处于 CrashLoopBackOff",
		EvidenceIDs:  []string{"e_pods"},
	}})
	reporter := NewFakeReporter(core.Report{
		ID:      "report_demo",
		Title:   "demo-api 诊断报告",
		Summary: "后端 Pod 未正常运行",
		Conclusions: []core.Conclusion{{
			HypothesisID: "h_pods",
			Result:       core.VerdictSupported,
			Reason:       "Pod 处于 CrashLoopBackOff",
			EvidenceIDs:  []string{"e_pods"},
		}},
	})

	generatedIDs := 0
	orchestrator := NewOrchestrator(
		parser,
		resolver,
		planner,
		tools.NewDispatcher(registry),
		verifier,
		reporter,
		func() string {
			generatedIDs++
			return "e_pods"
		},
	)
	report, err := orchestrator.Execute(context.Background(), core.Run{
		ID:       "run_demo",
		Question: "demo-api 为什么访问不了",
	})
	if err != nil {
		t.Fatalf("execute run: %v", err)
	}

	if report.RunID != "run_demo" || report.Summary != "后端 Pod 未正常运行" {
		t.Errorf("report was not bound to run: %#v", report)
	}
	if len(report.Conclusions) != 1 || report.Conclusions[0].EvidenceIDs[0] != "e_pods" {
		t.Errorf("report evidence chain was not preserved: %#v", report.Conclusions)
	}
	if generatedIDs != 1 {
		t.Errorf("generated evidence IDs = %d, want 1", generatedIDs)
	}
}
