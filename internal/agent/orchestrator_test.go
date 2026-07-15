package agent

import (
	"context"
	"testing"
	"time"

	"aruing/internal/core"
	"aruing/internal/tools"
)

// 提供可观察的固定元数据，验证编排器是否统一生成证据身份和时间
type testFactory struct {
	// 返回给编排器的固定证据编号
	id string
	// 返回给编排器的固定创建时间
	now time.Time
	// 记录最近一次请求的实体前缀
	prefix string
	// 记录编号生成次数，防止一次任务重复创建身份
	idCalls int
	// 记录时间读取次数，确认动态证据具有创建时间
	timeCalls int
}

// 返回固定编号并记录调用信息
func (f *testFactory) NewID(prefix string) (string, error) {
	f.prefix = prefix
	f.idCalls++
	return f.id, nil
}

// 返回固定时间并记录调用次数
func (f *testFactory) Now() time.Time {
	f.timeCalls++
	return f.now
}

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
	}})
	reporter := NewFakeReporter(core.Report{
		ID:      "report_demo",
		Title:   "demo-api 诊断报告",
		Summary: "后端 Pod 未正常运行",
		Conclusions: []core.Conclusion{{
			HypothesisID: "h_pods",
			Result:       core.VerdictSupported,
			Reason:       "Pod 处于 CrashLoopBackOff",
		}},
	})

	factory := &testFactory{
		id:  "e_runtime",
		now: time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC),
	}
	orchestrator := NewOrchestrator(
		parser,
		resolver,
		planner,
		tools.NewDispatcher(registry),
		verifier,
		reporter,
		factory,
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
	if len(report.Conclusions) != 1 || report.Conclusions[0].EvidenceIDs[0] != "e_runtime" {
		t.Errorf("report evidence chain was not preserved: %#v", report.Conclusions)
	}
	if factory.prefix != "e" || factory.idCalls != 1 || factory.timeCalls != 1 {
		t.Errorf("factory calls = prefix %q, IDs %d, times %d; want e, 1, 1", factory.prefix, factory.idCalls, factory.timeCalls)
	}
}
