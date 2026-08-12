package agent_test

import (
	"aruing/internal/agent"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"aruing/internal/agent/agenttest"
	"aruing/internal/core"
	"aruing/internal/tools"
	"aruing/internal/tools/toolstest"
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

// 按顺序返回预设编号，用尽后用前缀加序号形式继续，保证测试可断言前几次发号
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
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register fake tool: %v", err)
	}

	parser := agenttest.NewFakeParser(core.Query{
		ID:   "query_demo",
		Goal: "定位 demo-api 无法访问的原因",
		Nodes: []core.Node{{
			ID:   "node_demo",
			Type: "resource",
			Text: "demo-api",
		}},
	})
	resolver := agenttest.NewFakeResolver([]core.Target{{
		Type: "k8s.resource",
		Attrs: map[string]string{
			"k8s.kind":      "Deployment",
			"k8s.namespace": "default",
			"k8s.name":      "demo-api",
		},
	}})
	// 规划器仍用固定模板；目标引用在规划内按已知引用校验
	// 假规划不依赖目标编号，引用使用假设与节点侧约定
	planner := agenttest.NewFakePlanner(agent.Plan{
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
	verifier := agenttest.NewFakeVerifier([]core.Verdict{{
		ID:           "verdict_pods",
		HypothesisID: "h_pods",
		Result:       core.VerdictSupported,
		Reason:       "Pod 处于 CrashLoopBackOff",
	}})
	reporter := agenttest.NewFakeReporter(core.Report{
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
	orchestrator := agent.NewOrchestrator(
		parser,
		resolver,
		planner,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		verifier,
		reporter,
		factory,
	)
	outcome, err := orchestrator.Execute(context.Background(), core.Run{
		ID:       "run_demo",
		Question: "demo-api 为什么访问不了",
	})
	if err != nil {
		t.Fatalf("execute run: %v", err)
	}
	if outcome.Report == nil {
		t.Fatalf("expected report, got suspension: %+v", outcome.Suspension)
	}
	report := *outcome.Report

	if report.RunID != "run_demo" || report.Summary != "后端 Pod 未正常运行" {
		t.Errorf("report was not bound to run: %#v", report)
	}
	if len(report.Conclusions) != 1 || report.Conclusions[0].EvidenceIDs[0] != "e_runtime" {
		t.Errorf("report evidence chain was not preserved: %#v", report.Conclusions)
	}
	// 假路径：先目标发号，再规划任务证据发号
	if len(factory.prefixes) < 2 || factory.prefixes[0] != "target" || factory.prefixes[1] != "e" {
		t.Errorf("factory prefixes = %v, want target then e", factory.prefixes)
	}
}

// 脚本化驱动：先调用工具再提交目标，证明定位循环经统一执行器取证
func TestOrchestratorResolveLoop(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}

	nodeID := "node_loop"
	driver := &scriptedResolveDriver{steps: []agent.ResolveAction{
		{
			Action: agent.ResolveActionCallTool,
			Reason: "list pods",
			ToolCalls: []agent.ProposedToolCall{{
				ToolName:  "fake.list_pods",
				Arguments: json.RawMessage(`{"namespace":"default"}`),
				Purpose:   "confirm workload",
				Refs:      []string{nodeID},
			}},
		},
		// 提交在第二轮由驱动根据状态填充证据
	}}
	// 第二步在下一步内根据状态动态构造，见脚本化解析驱动

	parser := agenttest.NewFakeParser(core.Query{
		ID: "query_loop",
		Nodes: []core.Node{{
			ID:   nodeID,
			Type: "resource",
			Text: "demo",
		}},
	})
	planner := agenttest.NewFakePlanner(agent.Plan{
		Hypotheses: []core.Hypothesis{{ID: "h1", Statement: "x"}},
		Tasks:      []core.Task{{ID: "t_plan", Refs: []string{"h1"}, ToolName: "fake.list_pods"}},
	})
	verifier := agenttest.NewFakeVerifier([]core.Verdict{{
		ID: "v1", HypothesisID: "h1", Result: core.VerdictSupported, Reason: "ok",
	}})
	reporter := agenttest.NewFakeReporter(core.Report{
		ID: "r1", Summary: "ok",
		Conclusions: []core.Conclusion{{
			HypothesisID: "h1", Result: core.VerdictSupported, Reason: "ok",
		}},
	})

	factory := &testFactory{
		ids: []string{"t_resolve", "e_resolve", "target_1", "e_plan"},
		now: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
	}
	orch := agent.NewOrchestrator(
		parser, driver, planner,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		verifier, reporter, factory,
	)
	outcome, err := orch.Execute(context.Background(), core.Run{
		ID: "run_loop", Question: "demo?",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if outcome.Report == nil {
		t.Fatalf("expected report, got suspension: %+v", outcome.Suspension)
	}
	if outcome.Report.Summary != "ok" {
		t.Errorf("report = %#v", outcome.Report)
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

// 超预算时不得伪造目标
func TestOrchestratorResolveBudget(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}

	driver := &alwaysCallToolDriver{}
	parser := agenttest.NewFakeParser(core.Query{
		ID:    "q",
		Nodes: []core.Node{{ID: "n1", Text: "x"}},
	})
	// 规划不应被调用；用会失败的空规划兜底
	planner := agenttest.NewFakePlanner(agent.Plan{})
	verifier := agenttest.NewFakeVerifier(nil)
	reporter := agenttest.NewFakeReporter(core.Report{ID: "r"})

	factory := &testFactory{ids: nil, now: time.Now().UTC()}
	orch := agent.NewOrchestrator(
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

// 引用未知节点的提交应被编排拒绝
func TestOrchestratorResolveUnknownNode(t *testing.T) {
	driver := &staticResolveDriver{action: agent.ResolveAction{
		Action: agent.ResolveActionSubmitTargets,
		Targets: []agent.ProposedTarget{{
			NodeID: "node_missing",
			Type:   "resource",
		}},
	}}
	parser := agenttest.NewFakeParser(core.Query{
		ID:    "q",
		Nodes: []core.Node{{ID: "node_real"}},
	})
	orch := agent.NewOrchestrator(
		parser, driver, agenttest.NewFakePlanner(agent.Plan{}),
		tools.NewDispatcher(tools.NewRegistry(), tools.NewReadonlyPolicy()),
		agenttest.NewFakeVerifier(nil), agenttest.NewFakeReporter(core.Report{ID: "r"}),
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

// 先澄清再提交：首轮 clarify，Resume 注入答复后提交目标
type clarifyThenSubmitDriver struct {
	calls int
	seen  [][]string
}

func (d *clarifyThenSubmitDriver) Next(ctx context.Context, state agent.ResolveState) (agent.ResolveAction, error) {
	d.calls++
	d.seen = append(d.seen, append([]string(nil), state.Clarifications...))
	if len(state.Clarifications) == 0 {
		return agent.ResolveAction{
			Action: agent.ResolveActionClarify,
			Reason: "ambiguous namespace",
			Clarify: &agent.ClarifyRequest{
				Question: "是哪个命名空间？",
				Options:  []string{"ns-a", "ns-b"},
			},
		}, nil
	}
	nodeID := ""
	if len(state.Query.Nodes) > 0 {
		nodeID = state.Query.Nodes[0].ID
	}
	return agent.ResolveAction{
		Action: agent.ResolveActionSubmitTargets,
		Reason: "disambiguated by user",
		Targets: []agent.ProposedTarget{{
			NodeID: nodeID,
			Type:   "k8s.resource",
			Attrs:  map[string]string{"k8s.namespace": state.Clarifications[0], "k8s.name": "demo"},
		}},
	}, nil
}

// 定位驱动首轮澄清 → 挂起 → Resume 注入答复后完成
func TestOrchestratorSuspendAndResume(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	driver := &clarifyThenSubmitDriver{}
	parser := agenttest.NewFakeParser(core.Query{
		ID: "q_clarify",
		Nodes: []core.Node{{
			ID:   "n_demo",
			Type: "resource",
			Text: "demo",
		}},
	})
	planner := agenttest.NewFakePlanner(agent.Plan{
		Hypotheses: []core.Hypothesis{{ID: "h1", Statement: "x"}},
		Tasks:      []core.Task{{ID: "t1", Refs: []string{"h1"}, ToolName: "fake.list_pods"}},
	})
	verifier := agenttest.NewFakeVerifier([]core.Verdict{{
		ID: "v1", HypothesisID: "h1", Result: core.VerdictSupported, Reason: "ok",
	}})
	reporter := agenttest.NewFakeReporter(core.Report{ID: "r1", Summary: "ok"})
	orch := agent.NewOrchestrator(
		parser, driver, planner,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		verifier, reporter, &testFactory{now: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)},
	)

	run := core.Run{ID: "run_clarify", SessionID: "sess_c", Question: "demo 挂了"}
	out1, err := orch.Execute(context.Background(), run)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out1.Report != nil || out1.Suspension == nil {
		t.Fatalf("want suspension, got %+v", out1)
	}
	if out1.Suspension.RunID != "run_clarify" || out1.Suspension.Stage != core.StageResolve {
		t.Fatalf("suspension: %+v", out1.Suspension)
	}
	if !strings.Contains(out1.Suspension.Question, "命名空间") {
		t.Fatalf("question: %q", out1.Suspension.Question)
	}
	if got := orch.FindSuspended("sess_c"); got != "run_clarify" {
		t.Fatalf("FindSuspended = %q", got)
	}

	out2, err := orch.Resume(context.Background(), "run_clarify", "ns-a")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if out2.Report == nil || out2.Suspension != nil {
		t.Fatalf("want report after resume, got %+v", out2)
	}
	if out2.Report.Summary != "ok" {
		t.Fatalf("report: %+v", out2.Report)
	}
	if orch.FindSuspended("sess_c") != "" {
		t.Fatal("expected no suspended run after complete resume")
	}
	// 首轮无澄清；恢复轮应看到注入答复
	if len(driver.seen) < 2 || len(driver.seen[1]) == 0 || driver.seen[1][0] != "ns-a" {
		t.Fatalf("clarifications seen: %+v", driver.seen)
	}
}

// 无挂起快照时 Resume 失败
func TestOrchestratorResumeMissing(t *testing.T) {
	orch := agent.NewOrchestrator(
		agenttest.NewFakeParser(core.Query{ID: "q"}),
		agenttest.NewFakeResolver(nil),
		agenttest.NewFakePlanner(agent.Plan{}),
		tools.NewDispatcher(tools.NewRegistry(), tools.NewReadonlyPolicy()),
		agenttest.NewFakeVerifier(nil),
		agenttest.NewFakeReporter(core.Report{}),
		&testFactory{now: time.Now().UTC()},
	)
	_, err := orch.Resume(context.Background(), "missing", "ans")
	if err == nil {
		t.Fatal("expected error")
	}
}

// 脚本：第一轮调用工具，之后根据已有证据提交
type scriptedResolveDriver struct {
	steps []agent.ResolveAction
	calls int
}

func (d *scriptedResolveDriver) Next(ctx context.Context, state agent.ResolveState) (agent.ResolveAction, error) {
	d.calls++
	if d.calls == 1 {
		return d.steps[0], nil
	}
	if len(state.Evidence) == 0 {
		return agent.ResolveAction{Action: agent.ResolveActionFail, Error: "expected evidence after tool call"}, nil
	}
	nodeID := ""
	if len(state.Query.Nodes) > 0 {
		nodeID = state.Query.Nodes[0].ID
	}
	return agent.ResolveAction{
		Action: agent.ResolveActionSubmitTargets,
		Reason: "confirmed via evidence",
		Targets: []agent.ProposedTarget{{
			NodeID:      nodeID,
			Type:        "k8s.resource",
			Attrs:       map[string]string{"k8s.name": "demo"},
			EvidenceIDs: []string{state.Evidence[0].ID},
		}},
	}, nil
}

// 每轮都调用工具，用于预算测试
type alwaysCallToolDriver struct{}

func (d *alwaysCallToolDriver) Next(ctx context.Context, state agent.ResolveState) (agent.ResolveAction, error) {
	return agent.ResolveAction{
		Action: agent.ResolveActionCallTool,
		ToolCalls: []agent.ProposedToolCall{{
			ToolName:  "fake.list_pods",
			Arguments: json.RawMessage(`{}`),
			Purpose:   "burn budget",
		}},
	}, nil
}

// 固定返回同一动作
type staticResolveDriver struct {
	action agent.ResolveAction
}

func (d *staticResolveDriver) Next(ctx context.Context, state agent.ResolveState) (agent.ResolveAction, error) {
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

func countPrefix(prefixes []string, want string) int {
	n := 0
	for _, p := range prefixes {
		if p == want {
			n++
		}
	}
	return n
}

// 调查循环：首轮证据不足触发再规划，次轮支持即停；两轮各取证一次
func TestOrchestratorInvestigateLoop(t *testing.T) {
	taskPlan := agent.Plan{
		Hypotheses: []core.Hypothesis{{ID: "h1", RunID: "run_inv", Statement: "x"}},
		Tasks:      []core.Task{{ID: "t1", RunID: "run_inv", ToolName: "fake.list_pods"}},
	}
	planner := &scriptedPlanner{plans: []agent.Plan{taskPlan}}
	verifier := &scriptedVerifier{results: [][]core.Verdict{
		{{HypothesisID: "h1", RunID: "run_inv", Result: core.VerdictInsufficient}},
		{{HypothesisID: "h1", RunID: "run_inv", Result: core.VerdictSupported}},
	}}
	orch, reporter, factory := newInvestigateOrch(t, planner, verifier)
	orch.SetInvestigateMaxRounds(3)

	if _, err := orch.Execute(context.Background(), core.Run{ID: "run_inv", Question: "x"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if planner.calls != 2 || verifier.calls != 2 {
		t.Errorf("calls = planner %d / verifier %d, want 2 / 2", planner.calls, verifier.calls)
	}
	if reporter.calls != 1 {
		t.Errorf("reporter calls = %d, want 1", reporter.calls)
	}
	if got := countPrefix(factory.prefixes, "e"); got != 2 {
		t.Errorf("evidence count = %d, want 2 (one per round)", got)
	}
	// 第二轮规划应看到首轮累积的证据
	if len(planner.seen) < 2 || planner.seen[1] != 1 {
		t.Errorf("round-1 planner did not see prior evidence: %v", planner.seen)
	}
}

// 调查停止条件：预算、默认单轮、空任务提前结束
func TestOrchestratorInvestigateStop(t *testing.T) {
	taskPlan := agent.Plan{Tasks: []core.Task{{ID: "t1", RunID: "run_inv", ToolName: "fake.list_pods"}}}

	t.Run("budget", func(t *testing.T) {
		planner := &scriptedPlanner{plans: []agent.Plan{taskPlan}}
		verifier := &scriptedVerifier{results: [][]core.Verdict{{{Result: core.VerdictInsufficient}}}}
		orch, _, _ := newInvestigateOrch(t, planner, verifier)
		orch.SetInvestigateMaxRounds(2)
		if _, err := orch.Execute(context.Background(), core.Run{ID: "run_inv", Question: "x"}); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if planner.calls != 2 {
			t.Errorf("planner calls = %d, want 2", planner.calls)
		}
	})

	// 默认预算一：即便证据不足也只跑一轮
	t.Run("default single round", func(t *testing.T) {
		planner := &scriptedPlanner{plans: []agent.Plan{taskPlan}}
		verifier := &scriptedVerifier{results: [][]core.Verdict{{{Result: core.VerdictInsufficient}}}}
		orch, _, _ := newInvestigateOrch(t, planner, verifier)
		if _, err := orch.Execute(context.Background(), core.Run{ID: "run_inv", Question: "x"}); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if planner.calls != 1 || verifier.calls != 1 {
			t.Errorf("calls = planner %d / verifier %d, want 1 / 1", planner.calls, verifier.calls)
		}
	})

	// 后续轮空任务：提前结束，不再验证
	t.Run("empty tasks", func(t *testing.T) {
		planner := &scriptedPlanner{plans: []agent.Plan{taskPlan, {}}}
		verifier := &scriptedVerifier{results: [][]core.Verdict{{{Result: core.VerdictInsufficient}}}}
		orch, _, _ := newInvestigateOrch(t, planner, verifier)
		orch.SetInvestigateMaxRounds(3)
		if _, err := orch.Execute(context.Background(), core.Run{ID: "run_inv", Question: "x"}); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if planner.calls != 2 || verifier.calls != 1 {
			t.Errorf("calls = planner %d / verifier %d, want 2 / 1", planner.calls, verifier.calls)
		}
	})
}

// 全部猜想被排除时应继续转向；预算耗尽则出报告
func TestOrchestratorInvestigateRefuted(t *testing.T) {
	taskPlan := agent.Plan{
		Hypotheses: []core.Hypothesis{{ID: "h1", RunID: "run_inv", Statement: "x"}},
		Tasks:      []core.Task{{ID: "t1", RunID: "run_inv", ToolName: "fake.list_pods"}},
	}

	t.Run("pivot to supported", func(t *testing.T) {
		planner := &scriptedPlanner{plans: []agent.Plan{taskPlan}}
		verifier := &scriptedVerifier{results: [][]core.Verdict{
			{{HypothesisID: "h1", RunID: "run_inv", Result: core.VerdictRefuted}},
			{{HypothesisID: "h2", RunID: "run_inv", Result: core.VerdictSupported}},
		}}
		orch, _, _ := newInvestigateOrch(t, planner, verifier)
		orch.SetInvestigateMaxRounds(3)
		if _, err := orch.Execute(context.Background(), core.Run{ID: "run_inv", Question: "x"}); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if planner.calls != 2 || verifier.calls != 2 {
			t.Errorf("calls = planner %d / verifier %d, want 2 / 2", planner.calls, verifier.calls)
		}
	})

	t.Run("budget after refuted", func(t *testing.T) {
		planner := &scriptedPlanner{plans: []agent.Plan{taskPlan}}
		verifier := &scriptedVerifier{results: [][]core.Verdict{
			{{HypothesisID: "h1", RunID: "run_inv", Result: core.VerdictRefuted}},
		}}
		orch, _, _ := newInvestigateOrch(t, planner, verifier)
		orch.SetInvestigateMaxRounds(2)
		if _, err := orch.Execute(context.Background(), core.Run{ID: "run_inv", Question: "x"}); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if planner.calls != 2 {
			t.Errorf("planner calls = %d, want 2", planner.calls)
		}
	})
}

// 用脚本驱动的调查阶段测试桩：按序返回计划，用尽后重复最后一个
type scriptedPlanner struct {
	plans []agent.Plan
	calls int
	seen  []int // 每次规划收到的累积证据条数
}

func (p *scriptedPlanner) Plan(ctx context.Context, state agent.PlanState) (agent.Plan, error) {
	if err := ctx.Err(); err != nil {
		return agent.Plan{}, err
	}
	p.seen = append(p.seen, len(state.Evidence))
	p.calls++
	if len(p.plans) == 0 {
		return agent.Plan{}, nil
	}
	idx := p.calls - 1
	if idx >= len(p.plans) {
		idx = len(p.plans) - 1
	}
	return p.plans[idx], nil
}

// 按脚本返回判断，用尽后重复最后一个；记录每轮收到的累积假设
type scriptedVerifier struct {
	results [][]core.Verdict
	calls   int
	hypSeen [][]core.Hypothesis
	evSeen  [][]core.Evidence // 每轮收到的累积证据，用于断言证据链
}

func (v *scriptedVerifier) Verify(ctx context.Context, _ core.Query, hypotheses []core.Hypothesis, tasks []core.Task, evidence []core.Evidence) ([]core.Verdict, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v.hypSeen = append(v.hypSeen, hypotheses)
	v.evSeen = append(v.evSeen, evidence)
	idx := v.calls
	if idx >= len(v.results) {
		idx = len(v.results) - 1
	}
	v.calls++
	return v.results[idx], nil
}

// 不做校验的报告桩，仅记录调用并返回绑定运行的空报告
type scriptedReporter struct{ calls int }

func (r *scriptedReporter) Report(ctx context.Context, run core.Run, verdicts []core.Verdict, evidence []core.Evidence) (core.Report, error) {
	r.calls++
	return core.Report{ID: "r", RunID: run.ID}, nil
}

// 组装一个定位即提交、调查阶段用脚本桩的编排器
func newInvestigateOrch(t *testing.T, planner *scriptedPlanner, verifier *scriptedVerifier) (*agent.Orchestrator, *scriptedReporter, *testFactory) {
	t.Helper()
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	parser := agenttest.NewFakeParser(core.Query{
		ID:    "q_inv",
		RunID: "run_inv",
		Nodes: []core.Node{{ID: "n_inv", Type: "resource", Text: "demo"}},
	})
	resolver := agenttest.NewFakeResolver(nil)
	reporter := &scriptedReporter{}
	factory := &testFactory{now: time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)}
	orch := agent.NewOrchestrator(
		parser, resolver, planner,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		verifier, reporter, factory,
	)
	return orch, reporter, factory
}

// 始终执行失败的探针工具，用于验证编排层对工具失败的容忍
type failingTool struct{}

func (failingTool) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "fake.failing",
		Description: "always fails",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func (failingTool) Execute(context.Context, json.RawMessage) (*core.Evidence, error) {
	return nil, errors.New("simulated tool failure")
}

// 工具失败应被容忍：合成错误证据入链，调查继续并正常出报告
func TestOrchestratorInvestigateToolFailure(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register fake: %v", err)
	}
	if err := registry.Register(failingTool{}); err != nil {
		t.Fatalf("register failing: %v", err)
	}
	// 规划两个任务：一个失败、一个正常，验证失败不拖垮另一个
	planner := &scriptedPlanner{plans: []agent.Plan{{
		Hypotheses: []core.Hypothesis{{ID: "h1", RunID: "run_inv", Statement: "x"}},
		Tasks: []core.Task{
			{ID: "t_fail", RunID: "run_inv", ToolName: "fake.failing"},
			{ID: "t_ok", RunID: "run_inv", ToolName: "fake.list_pods"},
		},
	}}}
	verifier := &scriptedVerifier{results: [][]core.Verdict{
		{{HypothesisID: "h1", RunID: "run_inv", Result: core.VerdictSupported}},
	}}
	parser := agenttest.NewFakeParser(core.Query{ID: "q_inv", RunID: "run_inv", Nodes: []core.Node{{ID: "n_inv"}}})
	orch := agent.NewOrchestrator(
		parser, agenttest.NewFakeResolver(nil), planner,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		verifier, &scriptedReporter{}, &testFactory{now: time.Now().UTC()},
	)

	if _, err := orch.Execute(context.Background(), core.Run{ID: "run_inv", Question: "x"}); err != nil {
		t.Fatalf("tool failure should be tolerated, got: %v", err)
	}
	// 两条证据都入链：一条正常、一条带错误
	if len(verifier.evSeen) == 0 || len(verifier.evSeen[0]) != 2 {
		t.Fatalf("evidence seen = %v, want 2 items", verifier.evSeen)
	}
	var hasErrorEv bool
	for _, e := range verifier.evSeen[0] {
		if e.Error != "" {
			hasErrorEv = true
		}
	}
	if !hasErrorEv {
		t.Errorf("want an error evidence in chain, got: %#v", verifier.evSeen[0])
	}
}

// 定位阶段已取的证据应作为首轮上下文喂给调查阶段的规划器
// 脚本化解析驱动第一轮调一次伪装列举容器组产生证据，第二轮提交目标
// 调查首轮规划器调用时状态中的证据应含该证据（而非空）
func TestOrchestratorReuseResolveEvidence(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	nodeID := "n_reuse"
	driver := &scriptedResolveDriver{steps: []agent.ResolveAction{
		{
			Action: agent.ResolveActionCallTool,
			Reason: "list pods to confirm workload",
			ToolCalls: []agent.ProposedToolCall{{
				ToolName:  "fake.list_pods",
				Arguments: json.RawMessage(`{"namespace":"default"}`),
				Purpose:   "确认目标",
				Refs:      []string{nodeID},
			}},
		},
	}}
	planner := &scriptedPlanner{plans: []agent.Plan{{
		Hypotheses: []core.Hypothesis{{ID: "h1", RunID: "run_reuse", Statement: "x"}},
		Tasks:      []core.Task{{ID: "t1", RunID: "run_reuse", Refs: []string{"h1"}, ToolName: "fake.list_pods"}},
	}}}
	verifier := &scriptedVerifier{results: [][]core.Verdict{
		{{HypothesisID: "h1", RunID: "run_reuse", Result: core.VerdictSupported}},
	}}
	factory := &testFactory{
		ids: []string{"t_resolve", "e_resolve", "target_1"},
		now: time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC),
	}
	orch := agent.NewOrchestrator(
		agenttest.NewFakeParser(core.Query{ID: "q_reuse", RunID: "run_reuse", Nodes: []core.Node{{ID: nodeID, Type: "resource", Text: "demo"}}}),
		driver, planner,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		verifier, &scriptedReporter{}, factory,
	)

	if _, err := orch.Execute(context.Background(), core.Run{ID: "run_reuse", Question: "demo?"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// 脚本化解析驱动第一轮调用工具产生 1 条证据；调查首轮规划器应看到它
	if len(planner.seen) == 0 || planner.seen[0] != 1 {
		t.Errorf("首轮回喂 evidence = %v, want 1 (定位证据复用)", planner.seen)
	}
}

// 模拟集群工具：按配置返回资源清单标准输出或失败，用于侦察路径黑盒测试
type fakeK8sAPIResourcesTool struct {
	stdout string
	fail   bool
}

func (t *fakeK8sAPIResourcesTool) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "k8s",
		Description: "fake k8s for recon test",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func (t *fakeK8sAPIResourcesTool) Execute(ctx context.Context, args json.RawMessage) (*core.Evidence, error) {
	if t.fail {
		return nil, errors.New("simulated api-resources failure")
	}
	raw, _ := json.Marshal(map[string]any{
		"argv":     []string{"api-resources"},
		"exitCode": 0,
		"stdout":   t.stdout,
	})
	return &core.Evidence{
		Source:      "kubernetes",
		ToolName:    "k8s",
		CommandView: "kubectl api-resources",
		Summary:     "kubectl 执行完成，exitCode=0",
		Raw:         raw,
	}, nil
}

// 侦察证据进返回链（透明），但不在验证器输入（侦察是上下文不是判决依据）
func TestOrchestratorReconEvidenceScope(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register fake.list_pods: %v", err)
	}
	if err := registry.Register(&fakeK8sAPIResourcesTool{
		stdout: "NAME           SHORTNAMES   NAMESPACED   KIND\n" +
			"ingressroutes  ico          true         IngressRoute\n",
	}); err != nil {
		t.Fatalf("register fake k8s: %v", err)
	}
	planner := &scriptedPlanner{plans: []agent.Plan{{
		Hypotheses: []core.Hypothesis{{ID: "h1", RunID: "run_recon", Statement: "x"}},
		Tasks:      []core.Task{{ID: "t1", RunID: "run_recon", Refs: []string{"h1"}, ToolName: "fake.list_pods"}},
	}}}
	verifier := &scriptedVerifier{results: [][]core.Verdict{
		{{HypothesisID: "h1", RunID: "run_recon", Result: core.VerdictSupported}},
	}}
	parser := agenttest.NewFakeParser(core.Query{ID: "q_recon", RunID: "run_recon", Nodes: []core.Node{{ID: "n_recon"}}})
	orch := agent.NewOrchestrator(
		parser, agenttest.NewFakeResolver(nil), planner,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		verifier, &scriptedReporter{}, &testFactory{now: time.Now().UTC()},
	)
	orch.SetReconEnabled(true)

	outcome, err := orch.Execute(context.Background(), core.Run{ID: "run_recon", Question: "x"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if outcome.Report == nil {
		t.Fatalf("expected report, got suspension: %+v", outcome.Suspension)
	}
	evidence := outcome.Evidence

	var reconInChain *core.Evidence
	for i := range evidence {
		if evidence[i].ToolName == "k8s" {
			reconInChain = &evidence[i]
			break
		}
	}
	if reconInChain == nil {
		t.Fatalf("returned chain missing recon evidence: %+v", evidence)
	} else if !strings.Contains(reconInChain.Summary, "侦察") {
		t.Errorf("recon evidence summary = %q, want recon note", reconInChain.Summary)
	}

	if len(verifier.evSeen) == 0 {
		t.Fatal("verifier not called")
	}
	for _, ev := range verifier.evSeen[0] {
		if ev.ToolName == "k8s" {
			t.Errorf("recon evidence leaked into verifier input: %+v", ev)
		}
	}
}
