package agent_test

// 挂起快照的导出/导入与跨实例恢复：进程重启的等价性在此钉住——
// 导出 → 新编排器导入 → Resume 与进程内 Resume 行为一致（不重解析、不重规划、
// 信念连续）。坏载荷校验与导出隔离同文件覆盖。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Aruing/Aruing/internal/agent"
	"github.com/Aruing/Aruing/internal/agent/agenttest"
	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/tools"
	"github.com/Aruing/Aruing/internal/tools/toolstest"
)

// 组装定位阶段可澄清的完整假栈编排器（跨实例恢复测试共用）
func newResolveSuspendOrch(t *testing.T, driver *clarifyThenSubmitDriver) (*agent.Orchestrator, *agenttest.CallCountParser) {
	t.Helper()
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	parser := &agenttest.CallCountParser{Query: core.Query{
		ID:    "q_snap",
		Nodes: []core.Node{{ID: "n_demo", Type: "resource", Text: "demo"}},
	}}
	planner := agenttest.NewFakePlanner(agent.Plan{
		Hypotheses: []core.Hypothesis{{ID: "h1", Statement: "猜想一"}},
		Tasks:      []core.Task{{ID: "t1", Refs: []string{"h1"}, ToolName: "fake.list_pods"}},
	})
	verifier := agenttest.NewFakeVerifier([]core.Verdict{{
		ID: "v1", HypothesisID: "h1", Result: core.VerdictSupported, Reason: "ok",
	}})
	reporter := agenttest.NewFakeReporter(core.Report{ID: "r1", Summary: "ok"})
	orch := agent.NewOrchestrator(
		parser,
		driver,
		planner,
		tools.NewDispatcher(registry, tools.NewReadonlyPolicy()),
		verifier, reporter, &testFactory{now: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)},
	)
	return orch, parser
}

// 定位挂起 → 导出 → 新编排器（新进程等价）导入 → Resume 完成；
// 关键断言：新实例不重跑解析（快照复用 query），完成后挂起索引清空
func TestOrchestratorSnapshotResolveResumeAcrossInstances(t *testing.T) {
	orch1, _ := newResolveSuspendOrch(t, &clarifyThenSubmitDriver{})
	run := core.Run{ID: "run_snap", SessionID: "sess_snap", Question: "demo 在哪个命名空间"}

	out, err := orch1.Execute(context.Background(), run)
	if err != nil || out.Suspension == nil {
		t.Fatalf("execute: %+v err=%v", out, err)
	}
	if out.Suspension.Stage != core.StageResolve {
		t.Fatalf("stage = %q, want resolve", out.Suspension.Stage)
	}

	payload, err := orch1.ExportSuspended("run_snap")
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	// 新编排器 = 重启后的新进程：同套假角色、独立挂起索引
	orch2, parser2 := newResolveSuspendOrch(t, &clarifyThenSubmitDriver{})
	if got := orch2.FindSuspended("sess_snap"); got != "" {
		t.Fatalf("fresh orchestrator should have no suspension, got %q", got)
	}
	err = orch2.ImportSuspended(payload)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if got := orch2.FindSuspended("sess_snap"); got != "run_snap" {
		t.Fatalf("imported suspension = %q", got)
	}

	resumed, err := orch2.Resume(context.Background(), "run_snap", "ns-b")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Report == nil {
		t.Fatalf("want report after cross-instance resume, got %+v", resumed)
	}
	if parser2.Calls != 0 {
		t.Fatalf("parse re-run after import: %d (snapshot should carry query)", parser2.Calls)
	}
	if orch2.FindSuspended("sess_snap") != "" {
		t.Fatal("expected no suspended run after complete resume")
	}
}

// 取证决策状态（信念/动作池）跨实例连续：镜像 TestAcquireLoopAskSuspendResume 的
// 收敛性检验——恢复时若信念被重置为先验，一次观测只到 0.895 不会收敛
func TestOrchestratorSnapshotAcquireResumeAcrossInstances(t *testing.T) {
	decision := agent.PlanDecision{
		Hypotheses: []core.Hypothesis{
			{ID: "h_1", Statement: "近期变更引入", Confidence: 0.5},
			{ID: "h_2", Statement: "环境漂移", Confidence: 0.5},
		},
		Actions: []agent.ActionProposal{
			{
				Name:     "ask-change",
				Ask:      "问题是最近变更后出现的吗？",
				Purpose:  "区分变更引入",
				Cost:     10,
				Outcomes: []string{"yes", "no"},
				Matrix:   [][]float64{{0.7, 0.3}, {0.2, 0.8}},
			},
			{
				Name:     "check-pods",
				Argv:     []string{"get", "pods"},
				Purpose:  "看 Pod 状态",
				Cost:     1,
				Outcomes: []string{"crash", "running"},
				Matrix:   [][]float64{{0.85, 0.15}, {0.1, 0.9}},
			},
		},
	}
	fakeDecision := agenttest.NewFakeDecisionPlanner(decision)
	verifier := &recordingVerifier{FakeVerifier: agenttest.NewFakeVerifier([]core.Verdict{{
		ID: "v_1", HypothesisID: "h_1", Result: core.VerdictSupported, Reason: "证据支持",
	}})}
	executor := &scriptedExecutor{script: map[string]string{
		"pods": "pod CrashLoopBackOff",
	}}
	orch1 := newAcquireOrchestrator(t, fakeDecision, verifier, executor)

	out, err := orch1.Execute(context.Background(), core.Run{ID: "run_a", SessionID: "sess_a", Question: "q"})
	if err != nil || out.Suspension == nil {
		t.Fatalf("execute: %+v err=%v", out, err)
	}
	payload, err := orch1.ExportSuspended("run_a")
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	// 新实例导入恢复：信念经 JSON 往返后仍连续（不重规划、答复归类更新生效）
	orch2 := newAcquireOrchestrator(t, fakeDecision, verifier, executor)
	callsBefore := fakeDecision.Calls
	err = orch2.ImportSuspended(payload)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	resumed, err := orch2.Resume(context.Background(), "run_a", "yes，变更后出现")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Report == nil {
		t.Fatalf("expected report after cross-instance resume, got %+v", resumed)
	}
	if got := orch2.LastRunStats().AcquireExit; got != "supported" {
		t.Errorf("exit = %q, want supported (belief continuity broken?)", got)
	}
	if fakeDecision.Calls != callsBefore {
		t.Errorf("plan calls = %d, want %d (no replan after import-resume)", fakeDecision.Calls, callsBefore)
	}
}

// 导出隔离：载荷定格导出时点，此后进程内状态变化不影响已导出字节；
// 再次挂起后导出的快照携带累积答复
func TestOrchestratorSnapshotExportIsolation(t *testing.T) {
	planner := agenttest.NewFakePlanner(agent.Plan{
		Hypotheses: []core.Hypothesis{{ID: "h1", Statement: "猜想一"}},
		Tasks:      []core.Task{{ID: "t1", Refs: []string{"h1"}, ToolName: "fake.list_pods"}},
	})
	planner.WithClarify("第一次问：故障从何时开始？")
	orch, _ := newSuspendOrch(t, planner)
	run := core.Run{ID: "run_iso", SessionID: "sess_iso", Question: "demo 为什么慢"}

	if _, err := orch.Execute(context.Background(), run); err != nil {
		t.Fatalf("execute: %v", err)
	}
	first, err := orch.ExportSuspended("run_iso")
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	// 进程内再走一轮：再挂起（快照的澄清答复累积 +1）
	planner.WithClarify("第二次问：近期有变更吗？")
	_, err = orch.Resume(context.Background(), "run_iso", "今天早上")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	second, err := orch.ExportSuspended("run_iso")
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	var snap1, snap2 agent.SuspensionSnapshot
	err = json.Unmarshal(first, &snap1)
	if err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}
	err = json.Unmarshal(second, &snap2)
	if err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}
	if n := len(snap1.Investigate.Clarifications); n != 0 {
		t.Errorf("first export clarifications = %d, want 0 (frozen at export time)", n)
	}
	if n := len(snap2.Investigate.Clarifications); n != 1 {
		t.Errorf("second export clarifications = %d, want 1 (accumulated)", n)
	}
}

// 导入校验：坏 JSON、未知版本、空运行编号、未知阶段逐条拒绝；合法重复导入幂等
func TestOrchestratorSnapshotImportValidation(t *testing.T) {
	orch, _ := newResolveSuspendOrch(t, &clarifyThenSubmitDriver{})
	if _, err := orch.Execute(context.Background(), core.Run{ID: "run_v", SessionID: "sess_v", Question: "q"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	good, err := orch.ExportSuspended("run_v")
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	cases := []struct {
		name    string
		payload func() []byte
		wantErr string
	}{
		{"corrupt json", func() []byte { return []byte("{not json") }, "import suspended"},
		{"unknown version", func() []byte {
			return []byte(strings.Replace(string(good), `"v":1`, `"v":99`, 1))
		}, "unsupported snapshot version"},
		{"empty run id", func() []byte {
			var s agent.SuspensionSnapshot
			_ = json.Unmarshal(good, &s)
			s.Run.ID = ""
			b, _ := json.Marshal(s)
			return b
		}, "run id is required"},
		{"unknown stage", func() []byte {
			var s agent.SuspensionSnapshot
			_ = json.Unmarshal(good, &s)
			s.Stage = "report"
			b, _ := json.Marshal(s)
			return b
		}, "unknown suspension stage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := orch.ImportSuspended(tc.payload())
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}

	// 合法载荷重复导入幂等（覆盖归位，不报错不重复）
	if err := orch.ImportSuspended(good); err != nil {
		t.Fatalf("first import: %v", err)
	}
	if err := orch.ImportSuspended(good); err != nil {
		t.Fatalf("second import: %v", err)
	}
	if got := orch.FindSuspended("sess_v"); got != "run_v" {
		t.Fatalf("FindSuspended after idempotent import = %q", got)
	}
}

// 导出不存在的运行明确报错
func TestOrchestratorSnapshotExportMissing(t *testing.T) {
	orch, _ := newResolveSuspendOrch(t, &clarifyThenSubmitDriver{})
	if _, err := orch.ExportSuspended("run_missing"); err == nil {
		t.Fatal("error = nil, want missing run")
	}
}
