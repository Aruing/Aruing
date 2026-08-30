package eval

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 假栈 e2e（装置验收）：函数注入依赖驱动 mini 会话（8 轮 2 诊断 3 探针），
// 证明「脚本 → 轮次 → 记录 → 展开 → 观测量」全链成立
// 假 Turn 按用户文本分派：诊断请求写假账本并返回诊断轮结果，探针按预设答案回复
func TestRunProbeSessionFakeStack(t *testing.T) {
	const (
		qaAnswer   = "好的，从上下文看一切正常。"
		podRun1    = "demo-api-111-aaa"
		podLast    = "demo-api-333-ccc"
		cmdRunLast = "kubectl get events -n demo"
	)
	spec := ProbeSpec{
		Name:            "scn-fake",
		DiagnoseRequest: "请正式诊断 demo 的 demo-api",
		QAPool:          []string{"看看集群状态", "有哪些工作负载"},
		Probes: []ProbeQuestion{
			{ID: "pod1", Class: ProbeClassEvidence, Question: "第 1 次诊断查出的异常 pod 叫什么？",
				Expect: []ExpectGroup{{FromLedger: &LedgerRule{Rule: LedgerRulePods, K: 1}}}},
			{ID: "cmdLast", Class: ProbeClassChain, Question: "最后一次诊断跑了什么命令？",
				Expect: []ExpectGroup{{FromLedger: &LedgerRule{Rule: LedgerRuleCommands, K: -1}}}},
			{ID: "both", Class: ProbeClassSynthesis, Question: "对比第一次和最后一次诊断的结论。",
				Expect: []ExpectGroup{
					{FromLedger: &LedgerRule{Rule: LedgerRulePods, K: 1}},
					{FromLedger: &LedgerRule{Rule: LedgerRulePods, K: -1}},
					{Literal: "demo-api"},
				}},
		},
	}
	// seed=3 / 8 轮恰 2 次诊断（含末轮，覆盖 k=1 与 -1 两种账本定位）
	script, err := GenerateProbeScript(spec, 8, 3)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	ledger := store.NewMemoryRunLedger()
	factory := core.NewFactory()
	diagCount := 0
	deps := ProbeDeps{
		Turn: func(ctx context.Context, sessionID, userText string) (session.TurnResult, error) {
			switch {
			case userText == spec.DiagnoseRequest:
				diagCount++
				runID, rerr := factory.NewID("run")
				if rerr != nil {
					return session.TurnResult{}, rerr
				}
				pod := podRun1
				if diagCount == 2 {
					pod = podLast
				}
				report := core.Report{ID: "rep_" + runID, RunID: runID, Summary: "根因是 pod " + pod,
					Conclusions: []core.Conclusion{{Result: "supported", Reason: "根因是 pod " + pod}}}
				putErr := ledger.Put(ctx, session.DiagnosticRecord{
					RunID: runID, SessionID: sessionID, Question: userText,
					Report: report,
					Evidence: []core.Evidence{{
						ID: "e_" + runID, RunID: runID, ToolName: "k8s",
						CommandView: cmdRunLast, Summary: "pod " + pod + " CrashLoopBackOff",
					}},
				})
				if putErr != nil {
					return session.TurnResult{}, putErr
				}
				return session.TurnResult{RunID: runID, Report: &report}, nil
			case strings.HasPrefix(userText, "第 1 次诊断"):
				return turnAnswer("是 " + podRun1), nil
			case strings.HasPrefix(userText, "最后一次诊断"):
				return turnAnswer(cmdRunLast), nil
			case strings.HasPrefix(userText, "对比"):
				return turnAnswer(fmt.Sprintf("第一次是 %s，最后一次是 %s，都是 demo-api 的问题", podRun1, podLast)), nil
			default:
				return turnAnswer(qaAnswer), nil
			}
		},
		MemStats: func() MemoryStats {
			return MemoryStats{Method: "ours", LocateLayer: "lambda1", RehydratedEvidence: 1, HistTurns: 9}
		},
		DiagnoseStats: func() DiagnoseStats { return DiagnoseStats{Rounds: 2, Exit: "supported"} },
		Ledger:        ledger,
	}

	rec, err := RunProbeSession(context.Background(), deps, ProbeRunOptions{
		SessionID: "sess_fake", Model: "fake-model", MemoryMethod: "ours",
		ResourceName: "demo-api",
	}, spec, script)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// 记录全落：完成、2 诊断、3 探针、轮数与脚本一致
	if !rec.Completed || rec.TurnsExecuted != len(script.Turns) {
		t.Fatalf("session incomplete: completed=%v turns=%d", rec.Completed, rec.TurnsExecuted)
	}
	if len(rec.Diagnoses) != 2 {
		t.Fatalf("want 2 embedded diagnoses, got %d", len(rec.Diagnoses))
	}
	for _, d := range rec.Diagnoses {
		if d.Status != "completed" || !d.Record.Completed {
			t.Fatalf("diagnose %s not recorded completed: %+v", d.RunID, d)
		}
		// 内嵌 RunRecord 从账本取证据与结论，出口透传编排统计
		if len(d.Record.RootCauses) != 1 || d.Record.AcquireExit != "supported" {
			t.Fatalf("embedded record fields wrong: %+v", d.Record)
		}
	}
	if len(rec.Probes) != 3 {
		t.Fatalf("want 3 probe entries, got %d", len(rec.Probes))
	}

	// 判分：三条探针全 hit（展开正确 + 假答案命中）
	res := JudgeProbeSession(rec, GroundTruth{ResourceName: "demo-api"})
	if res.ProbeScored != 3 || res.ProbeHits != 3 {
		t.Fatalf("probes not all hit: %+v", res.Probes)
	}
	// 观测量随探针轮落盘
	for _, p := range rec.Probes {
		if p.Memory.LocateLayer != "lambda1" || p.Memory.Method != "ours" {
			t.Fatalf("probe %s memory stats missing: %+v", p.ProbeID, p.Memory)
		}
	}
}

// 轮次失败：记入 TurnErrors、中止后续轮、返回部分记录与错误（失败也落盘口径）
func TestRunProbeSessionTurnFailure(t *testing.T) {
	spec := ProbeSpec{
		Name: "scn-fail", DiagnoseRequest: "诊断", QAPool: []string{"q1"},
		Probes: []ProbeQuestion{{ID: "p1", Class: ProbeClassEvidence, Question: "探针",
			Expect: []ExpectGroup{{Literal: "x"}}}},
	}
	script, err := GenerateProbeScript(spec, 6, 1)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// 第 3 轮（零起 2）起永久失败
	deps := ProbeDeps{
		Turn: func(ctx context.Context, sessionID, userText string) (session.TurnResult, error) {
			return session.TurnResult{}, fmt.Errorf("llm down")
		},
		Ledger: store.NewMemoryRunLedger(),
	}
	rec, err := RunProbeSession(context.Background(), deps, ProbeRunOptions{SessionID: "s"}, spec, script)
	if err == nil {
		t.Fatal("want turn error propagated")
	}
	if rec.Completed || len(rec.TurnErrors) != 1 || rec.TurnErrors[0].TurnIndex != 0 {
		t.Fatalf("failure record wrong: completed=%v errors=%+v", rec.Completed, rec.TurnErrors)
	}
	if rec.TurnsExecuted != 0 {
		t.Fatalf("turns executed should stay at failure point, got %d", rec.TurnsExecuted)
	}
}

// 依赖最小集：Turn / 账本缺失启动期报错，不到轮次里 panic
func TestRunProbeSessionDepsValidation(t *testing.T) {
	spec := ProbeSpec{Name: "s", DiagnoseRequest: "d", QAPool: []string{"q"},
		Probes: []ProbeQuestion{{ID: "p", Class: ProbeClassEvidence, Question: "?", Expect: []ExpectGroup{{Literal: "x"}}}}}
	script, err := GenerateProbeScript(spec, 4, 1)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := RunProbeSession(context.Background(), ProbeDeps{}, ProbeRunOptions{SessionID: "s"}, spec, script); err == nil {
		t.Fatal("empty deps must error")
	}
	if _, err := RunProbeSession(context.Background(), ProbeDeps{Ledger: store.NewMemoryRunLedger()}, ProbeRunOptions{SessionID: "s"}, spec, script); err == nil {
		t.Fatal("missing Turn must error")
	}
}

// turnAnswer 构造基线回复轮结果（假栈助手消息）
func turnAnswer(content string) session.TurnResult {
	return session.TurnResult{
		AssistantMessage: session.Message{Role: session.RoleAssistant, Content: content},
	}
}
