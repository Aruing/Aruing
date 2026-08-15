package agent_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"aruing/internal/agent"
	"aruing/internal/agent/agenttest"
	"aruing/internal/core"
	"aruing/internal/tools"
	"aruing/internal/tools/toolstest"
)

// 组装带完整假角色与 k8s 假工具的编排器（investigate 挂起测试共用）
// parserCalls 记录 Parse 次数：续跑不得重跑解析
func newSuspendOrch(t *testing.T, planner *agenttest.FakePlanner) (*agent.Orchestrator, *agenttest.CallCountParser) {
	t.Helper()
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	parser := &agenttest.CallCountParser{Query: core.Query{
		ID: "q_inv",
		Nodes: []core.Node{{
			ID:   "n_demo",
			Type: "resource",
			Text: "demo",
		}},
	}}
	if planner == nil {
		planner = agenttest.NewFakePlanner(agent.Plan{
			Hypotheses: []core.Hypothesis{{ID: "h1", Statement: "猜想一"}},
			Tasks:      []core.Task{{ID: "t1", Refs: []string{"h1"}, ToolName: "fake.list_pods"}},
		})
	}
	verifier := agenttest.NewFakeVerifier([]core.Verdict{{
		ID: "v1", HypothesisID: "h1", Result: core.VerdictSupported, Reason: "ok",
	}})
	reporter := agenttest.NewFakeReporter(core.Report{ID: "r1", Summary: "ok"})
	orch := agent.NewOrchestrator(
		parser,
		agenttest.NewFakeResolver(nil),
		planner,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		verifier, reporter, &testFactory{now: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)},
	)
	return orch, parser
}

// 调查轮规划器提议澄清 → 挂起（stage=investigate）；Resume 注入答复后完成
// 关键断言：续跑不重跑 Parse、不重跑定位；答复出现在 PlanState.Clarifications
func TestOrchestratorInvestigateSuspendAndResume(t *testing.T) {
	planner := agenttest.NewFakePlanner(agent.Plan{
		Hypotheses: []core.Hypothesis{{ID: "h1", Statement: "猜想一"}},
		Tasks:      []core.Task{{ID: "t1", Refs: []string{"h1"}, ToolName: "fake.list_pods"}},
	})
	// 首轮正常规划（取证一轮 insufficient 后会再入规划——这里首轮后即挂起）：
	// 用脚本让第一次 Plan 返回 clarify，之后回落正常模板完成
	planner.WithClarify("故障大概从什么时候开始？", "今天早上", "上周")
	orch, parser := newSuspendOrch(t, planner)

	run := core.Run{ID: "run_inv", SessionID: "sess_i", Question: "demo 为什么慢"}
	out1, err := orch.Execute(context.Background(), run)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out1.Report != nil || out1.Suspension == nil {
		t.Fatalf("want suspension, got %+v", out1)
	}
	if out1.Suspension.Stage != core.StageInvestigate || out1.Suspension.RunID != "run_inv" {
		t.Fatalf("suspension: %+v", out1.Suspension)
	}
	if !strings.Contains(out1.Suspension.Question, "什么时候") {
		t.Fatalf("question: %q", out1.Suspension.Question)
	}
	if got := orch.FindSuspended("sess_i"); got != "run_inv" {
		t.Fatalf("FindSuspended = %q", got)
	}

	parsesBefore := parser.Calls

	out2, err := orch.Resume(context.Background(), "run_inv", "今天早上九点开始")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if out2.Report == nil {
		t.Fatalf("want report after resume, got %+v", out2)
	}
	// 续跑不得重跑解析；调查进度保留（完成路径的证据链含 fake 工具观察）
	if parser.Calls != parsesBefore {
		t.Fatalf("parse re-run on resume: %d -> %d", parsesBefore, parser.Calls)
	}
	if orch.FindSuspended("sess_i") != "" {
		t.Fatal("expected no suspended run after complete resume")
	}
	// 答复注入：planner 收到的最后一份状态含用户答复
	got := planner.GotClarifications
	if len(got) == 0 || !strings.Contains(got[len(got)-1][0], "九点") {
		t.Fatalf("clarifications seen: %+v", got)
	}
	// 规划输入不得丢已确认目标（含挂起与续跑的每次调用）
	for i, n := range planner.GotTargetCounts {
		if n == 0 {
			t.Fatalf("plan call %d saw zero targets (targets dropped from planner input)", i)
		}
	}
}

// Resume 后再次提议澄清：快照更新、可多次挂起（#18 澄清不限次）
func TestOrchestratorInvestigateDoubleClarify(t *testing.T) {
	planner := agenttest.NewFakePlanner(agent.Plan{
		Hypotheses: []core.Hypothesis{{ID: "h1", Statement: "猜想一"}},
		Tasks:      []core.Task{{ID: "t1", Refs: []string{"h1"}, ToolName: "fake.list_pods"}},
	})
	planner.WithClarify("第一次问：故障从何时开始？")
	orch, _ := newSuspendOrch(t, planner)
	run := core.Run{ID: "run_inv2", SessionID: "sess_i2", Question: "demo 为什么慢"}

	out1, err := orch.Execute(context.Background(), run)
	if err != nil || out1.Suspension == nil {
		t.Fatalf("first execute: %+v err=%v", out1, err)
	}
	// 再次脚本化澄清：Resume 内第一次 Plan 又提议澄清
	planner.WithClarify("第二次问：近期有变更吗？")
	out2, err := orch.Resume(context.Background(), "run_inv2", "今天早上")
	if err != nil || out2.Suspension == nil {
		t.Fatalf("first resume: %+v err=%v", out2, err)
	}
	if !strings.Contains(out2.Suspension.Question, "第二次问") {
		t.Fatalf("second question: %q", out2.Suspension.Question)
	}
	out3, err := orch.Resume(context.Background(), "run_inv2", "没有变更")
	if err != nil || out3.Report == nil {
		t.Fatalf("second resume: %+v err=%v", out3, err)
	}
	// 两次答复都在累积
	last := planner.GotClarifications[len(planner.GotClarifications)-1]
	if len(last) != 2 {
		t.Fatalf("accumulated clarifications: %+v", planner.GotClarifications)
	}
}

// clarify 与任务/猜想同给 → 编排明确报错（不静默取舍）
func TestOrchestratorInvestigateClarifyConflict(t *testing.T) {
	planner := agenttest.NewFakePlanner(agent.Plan{
		Hypotheses: []core.Hypothesis{{ID: "h1", Statement: "猜想一"}},
		Tasks:      []core.Task{{ID: "t1", Refs: []string{"h1"}, ToolName: "fake.list_pods"}},
	})
	// 构造冲突：脚本 clarify 与模板任务同时给出
	planner.WithClarify("问个问题").KeepPlanOnClarify()
	orch, _ := newSuspendOrch(t, planner)
	_, err := orch.Execute(context.Background(), core.Run{ID: "run_conflict", Question: "demo"})
	if err == nil || !strings.Contains(err.Error(), "clarify") {
		t.Fatalf("want clarify conflict error, got: %v", err)
	}
}

// resolve 与 investigate 挂起并存：FindSuspended 按会话各自命中
func TestOrchestratorSuspendStagesCoexist(t *testing.T) {
	// run A：resolve 挂起（clarifyThenSubmitDriver 首轮问 ns）
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	basePlan := agent.Plan{
		Hypotheses: []core.Hypothesis{{ID: "h1", Statement: "猜想一"}},
		Tasks:      []core.Task{{ID: "t1", Refs: []string{"h1"}, ToolName: "fake.list_pods"}},
	}
	orchA := agent.NewOrchestrator(
		agenttest.NewFakeParser(core.Query{ID: "q_a", Nodes: []core.Node{{ID: "n_a", Type: "resource", Text: "a"}}}),
		&clarifyThenSubmitDriver{},
		agenttest.NewFakePlanner(basePlan),
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		agenttest.NewFakeVerifier(nil),
		agenttest.NewFakeReporter(core.Report{}),
		&testFactory{now: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)},
	)
	_, err := orchA.Execute(context.Background(), core.Run{ID: "run_a", SessionID: "sess_a", Question: "a"})
	if err != nil || orchA.FindSuspended("sess_a") != "run_a" {
		t.Fatalf("orchA suspend: err=%v", err)
	}

	// run B：investigate 挂起
	plannerB := agenttest.NewFakePlanner(basePlan)
	plannerB.WithClarify("何时开始？")
	orchB, _ := newSuspendOrch(t, plannerB)
	_, err = orchB.Execute(context.Background(), core.Run{ID: "run_b", SessionID: "sess_b", Question: "b"})
	if err != nil || orchB.FindSuspended("sess_b") != "run_b" {
		t.Fatalf("orchB suspend: err=%v", err)
	}

	if got := orchB.FindSuspended("sess_a"); got != "" {
		t.Fatalf("cross-session leak: %q", got)
	}
}

// 调查挂起不得丢侦察产物：挂起前已侦察，Resume 续跑后
// ① 完成证据链仍含侦察证据（透明性）；② 续跑轮规划器仍收到 cluster_resources
func TestOrchestratorInvestigateSuspendKeepsRecon(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.Register(&fakeK8sAPIResourcesTool{
		stdout: "NAME           SHORTNAMES   NAMESPACED   KIND\n" +
			"ingressroutes  ico          true         IngressRoute\n",
	}); err != nil {
		t.Fatalf("register fake k8s: %v", err)
	}
	planner := agenttest.NewFakePlanner(agent.Plan{
		Hypotheses: []core.Hypothesis{{ID: "h1", Statement: "猜想一"}},
		Tasks:      []core.Task{{ID: "t1", Refs: []string{"h1"}, ToolName: "fake.list_pods"}},
	})
	planner.WithClarify("何时开始？")
	orch := agent.NewOrchestrator(
		agenttest.NewFakeParser(core.Query{ID: "q_r", Nodes: []core.Node{{ID: "n_r", Type: "resource", Text: "demo"}}}),
		agenttest.NewFakeResolver(nil),
		planner,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		agenttest.NewFakeVerifier([]core.Verdict{{ID: "v1", HypothesisID: "h1", Result: core.VerdictSupported, Reason: "ok"}}),
		agenttest.NewFakeReporter(core.Report{ID: "r1", Summary: "ok"}),
		&testFactory{now: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)},
	)
	orch.SetReconEnabled(true)

	out1, err := orch.Execute(context.Background(), core.Run{ID: "run_r", SessionID: "sess_r", Question: "demo 为什么慢"})
	if err != nil || out1.Suspension == nil {
		t.Fatalf("execute: %+v err=%v", out1, err)
	}

	out2, err := orch.Resume(context.Background(), "run_r", "今天早上")
	if err != nil || out2.Report == nil {
		t.Fatalf("resume: %+v err=%v", out2, err)
	}
	// ① 完成链含侦察证据
	hasRecon := false
	for _, ev := range out2.Evidence {
		if ev.ToolName == "k8s" {
			hasRecon = true
			break
		}
	}
	if !hasRecon {
		t.Fatalf("resumed evidence chain lost recon evidence: %d items", len(out2.Evidence))
	}
	// ② 续跑轮（Resume 后的 Plan 调用）规划器收到 cluster_resources
	if len(planner.GotClusterResources) < 2 || planner.GotClusterResources[len(planner.GotClusterResources)-1] == 0 {
		t.Fatalf("resumed planner lost cluster_resources: %v", planner.GotClusterResources)
	}
}
