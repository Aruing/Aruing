package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aruing/internal/core"
	"aruing/internal/tools"
)

// 提供可观察的固定元数据，验证编排器是否统一生成证据身份和时间
type testFactory struct {
	// 返回给编排器的固定证据编号前缀序列
	ids []string
	// 返回给编排器的固定创建时间
	now time.Time
	// 记录请求过的实体前缀
	prefixes []string
	// 当前发放下标
	index int
	// 记录时间读取次数
	timeCalls int
}

// 按顺序返回预设编号，用尽后用 prefix_N 形式继续，保证测试可断言前几次发号
func (f *testFactory) NewID(prefix string) (string, error) {
	f.prefixes = append(f.prefixes, prefix)
	if f.index < len(f.ids) {
		id := f.ids[f.index]
		f.index++
		return id, nil
	}
	f.index++
	return prefix + "_extra", nil
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
		Type: "k8s.resource",
		Attrs: map[string]string{
			"k8s.kind":      "Deployment",
			"k8s.namespace": "default",
			"k8s.name":      "demo-api",
		},
	}})
	// Planner 仍用固定模板；Target 引用在 Plan 内按 knownRefs 校验
	// 假规划不依赖 Target.ID，Refs 使用假设与节点侧约定
	planner := NewFakePlanner(Plan{
		Hypotheses: []core.Hypothesis{{
			ID:        "h_pods",
			Statement: "后端 Pod 没有正常运行",
		}},
		Tasks: []core.Task{{
			ID:       "task_pods",
			Refs:     []string{"h_pods"},
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
		ids: []string{"target_runtime", "e_runtime"},
		now: time.Date(2026, 7, 15, 10, 30, 0, 0, time.UTC),
	}
	orchestrator := NewOrchestrator(
		parser,
		resolver,
		planner,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
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
	// 假路径：先 target 发号，再规划任务证据发号
	if len(factory.prefixes) < 2 || factory.prefixes[0] != "target" || factory.prefixes[1] != "e" {
		t.Errorf("factory prefixes = %v, want target then e", factory.prefixes)
	}
}

// 脚本化驱动：先 call_tool 再 submit_targets，证明定位循环经统一执行器取证
func TestOrchestratorResolveLoop(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(tools.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}

	nodeID := "node_loop"
	driver := &scriptedResolveDriver{steps: []ResolveAction{
		{
			Action: ResolveActionCallTool,
			Reason: "list pods",
			ToolCalls: []ProposedToolCall{{
				ToolName:  "fake.list_pods",
				Arguments: json.RawMessage(`{"namespace":"default"}`),
				Purpose:   "confirm workload",
				Refs:      []string{nodeID},
			}},
		},
		// submit 在第二轮由驱动根据 state 填充 evidence
	}}
	// 第二步在 Next 内根据 state 动态构造，见 scriptedResolveDriver

	parser := NewFakeParser(core.Query{
		ID: "query_loop",
		Nodes: []core.Node{{
			ID:   nodeID,
			Type: "resource",
			Text: "demo",
		}},
	})
	planner := NewFakePlanner(Plan{
		Hypotheses: []core.Hypothesis{{ID: "h1", Statement: "x"}},
		Tasks:      []core.Task{{ID: "t_plan", Refs: []string{"h1"}, ToolName: "fake.list_pods"}},
	})
	verifier := NewFakeVerifier([]core.Verdict{{
		ID: "v1", HypothesisID: "h1", Result: core.VerdictSupported, Reason: "ok",
	}})
	reporter := NewFakeReporter(core.Report{
		ID: "r1", Summary: "ok",
		Conclusions: []core.Conclusion{{
			HypothesisID: "h1", Result: core.VerdictSupported, Reason: "ok",
		}},
	})

	factory := &testFactory{
		ids: []string{"t_resolve", "e_resolve", "target_1", "e_plan"},
		now: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
	}
	orch := NewOrchestrator(
		parser, driver, planner,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		verifier, reporter, factory,
	)
	report, err := orch.Execute(context.Background(), core.Run{
		ID: "run_loop", Question: "demo?",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if report.Summary != "ok" {
		t.Errorf("report = %#v", report)
	}
	if driver.calls < 2 {
		t.Errorf("driver calls = %d, want >= 2", driver.calls)
	}
	// 定位阶段应发过任务与证据编号
	if !containsPrefix(factory.prefixes, "t") || !containsPrefix(factory.prefixes, "e") {
		t.Errorf("prefixes = %v, want t and e for resolve", factory.prefixes)
	}
	if !containsPrefix(factory.prefixes, "target") {
		t.Errorf("prefixes = %v, want target", factory.prefixes)
	}
}

// 超预算时不得伪造 Target
func TestOrchestratorResolveBudget(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(tools.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}

	driver := &alwaysCallToolDriver{}
	parser := NewFakeParser(core.Query{
		ID:    "q",
		Nodes: []core.Node{{ID: "n1", Text: "x"}},
	})
	// Plan 不应被调用；用会失败的空规划兜底
	planner := NewFakePlanner(Plan{})
	verifier := NewFakeVerifier(nil)
	reporter := NewFakeReporter(core.Report{ID: "r"})

	factory := &testFactory{ids: nil, now: time.Now().UTC()}
	orch := NewOrchestrator(
		parser, driver, planner,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		verifier, reporter, factory,
	)
	orch.SetResolveMaxRounds(2)

	_, err := orch.Execute(context.Background(), core.Run{ID: "run_budget", Question: "x"})
	if err == nil {
		t.Fatal("error = nil, want budget exceeded")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Errorf("error = %q, want budget", err)
	}
}

// 引用未知节点的 submit 应被编排拒绝
func TestOrchestratorResolveUnknownNode(t *testing.T) {
	driver := &staticResolveDriver{action: ResolveAction{
		Action: ResolveActionSubmitTargets,
		Targets: []ProposedTarget{{
			NodeID: "node_missing",
			Type:   "resource",
		}},
	}}
	parser := NewFakeParser(core.Query{
		ID:    "q",
		Nodes: []core.Node{{ID: "node_real"}},
	})
	orch := NewOrchestrator(
		parser, driver, NewFakePlanner(Plan{}),
		tools.NewDispatcher(tools.NewRegistry(), tools.NewReadonlyPolicy()),
		NewFakeVerifier(nil), NewFakeReporter(core.Report{ID: "r"}),
		&testFactory{now: time.Now().UTC()},
	)
	_, err := orch.Execute(context.Background(), core.Run{ID: "run_x", Question: "x"})
	if err == nil {
		t.Fatal("error = nil, want unknown node")
	}
	if !strings.Contains(err.Error(), "unknown node") {
		t.Errorf("error = %q", err)
	}
}

// 脚本：第一轮 call_tool，之后根据已有证据 submit
type scriptedResolveDriver struct {
	steps []ResolveAction
	calls int
}

func (d *scriptedResolveDriver) Next(ctx context.Context, state ResolveState) (ResolveAction, error) {
	d.calls++
	if d.calls == 1 {
		return d.steps[0], nil
	}
	if len(state.Evidence) == 0 {
		return ResolveAction{Action: ResolveActionFail, Error: "expected evidence after tool call"}, nil
	}
	nodeID := ""
	if len(state.Query.Nodes) > 0 {
		nodeID = state.Query.Nodes[0].ID
	}
	return ResolveAction{
		Action: ResolveActionSubmitTargets,
		Reason: "confirmed via evidence",
		Targets: []ProposedTarget{{
			NodeID:      nodeID,
			Type:        "k8s.resource",
			Attrs:       map[string]string{"k8s.name": "demo"},
			EvidenceIDs: []string{state.Evidence[0].ID},
		}},
	}, nil
}

// 每轮都 call_tool，用于预算测试
type alwaysCallToolDriver struct{}

func (d *alwaysCallToolDriver) Next(ctx context.Context, state ResolveState) (ResolveAction, error) {
	return ResolveAction{
		Action: ResolveActionCallTool,
		ToolCalls: []ProposedToolCall{{
			ToolName:  "fake.list_pods",
			Arguments: json.RawMessage(`{}`),
			Purpose:   "burn budget",
		}},
	}, nil
}

// 固定返回同一动作
type staticResolveDriver struct {
	action ResolveAction
}

func (d *staticResolveDriver) Next(ctx context.Context, state ResolveState) (ResolveAction, error) {
	return d.action, nil
}

func containsPrefix(prefixes []string, want string) bool {
	for _, p := range prefixes {
		if p == want {
			return true
		}
	}
	return false
}
