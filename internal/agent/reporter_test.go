package agent_test

import (
	"context"
	"testing"

	"aruing/internal/agent/agenttest"
	"aruing/internal/core"
)

// 报告必须绑定当前运行并保留有效结论引用，才能向用户展示可核查结果
func TestFakeReporterReport(t *testing.T) {
	reporter := agenttest.NewFakeReporter(core.Report{
		ID:      "report_demo",
		RunID:   "stale_run",
		Title:   "demo 诊断报告",
		Summary: "后端未正常提供服务",
		Conclusions: []core.Conclusion{{
			HypothesisID: "h_demo",
			Result:       core.VerdictSupported,
			Reason:       "后端未就绪",
		}},
		Suggestions: []string{"检查应用启动日志"},
	})
	run := core.Run{ID: "run_test"}
	verdicts := []core.Verdict{{
		ID:           "verdict_demo",
		RunID:        "run_test",
		HypothesisID: "h_demo",
		Result:       core.VerdictSupported,
		EvidenceIDs:  []string{"e_runtime"},
	}}
	evidence := []core.Evidence{{ID: "e_runtime", RunID: "run_test"}}

	got, err := reporter.Report(context.Background(), run, verdicts, evidence)
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	if got.RunID != run.ID || len(got.Conclusions) != 1 {
		t.Errorf("report relation was not preserved: %#v", got)
	}
	if got.Conclusions[0].EvidenceIDs[0] != "e_runtime" || got.Suggestions[0] != "检查应用启动日志" {
		t.Errorf("report content was not preserved: %#v", got)
	}

	// 多次生成报告不能共享可变列表，否则一次展示修改可能污染后续运行
	got.Conclusions[0].EvidenceIDs[0] = "changed"
	got.Suggestions[0] = "changed"
	again, err := reporter.Report(context.Background(), run, verdicts, evidence)
	if err != nil {
		t.Fatalf("build report again: %v", err)
	}
	if again.Conclusions[0].EvidenceIDs[0] != "e_runtime" || again.Suggestions[0] != "检查应用启动日志" {
		t.Errorf("report template was mutated: %#v", again)
	}
}

// 报告只能整理一致且可追溯的判断与证据，不能生成悬空或跨运行结论
func TestFakeReporterValidate(t *testing.T) {
	run := core.Run{ID: "run_test"}
	// 代表校验：未知结论、result 与 verdict 不一致、跨运行证据
	tests := []struct {
		name     string
		report   core.Report
		verdicts []core.Verdict
		evidence []core.Evidence
	}{
		{
			name: "unknown verdict",
			report: core.Report{Conclusions: []core.Conclusion{{
				HypothesisID: "h_unknown",
			}}},
		},
		{
			name: "mismatched result",
			report: core.Report{Conclusions: []core.Conclusion{{
				HypothesisID: "h_test",
				Result:       core.VerdictSupported,
			}}},
			verdicts: []core.Verdict{{
				RunID:        "run_test",
				HypothesisID: "h_test",
				Result:       core.VerdictRefuted,
			}},
		},
		{
			name: "foreign evidence",
			report: core.Report{Conclusions: []core.Conclusion{{
				HypothesisID: "h_test",
			}}},
			verdicts: []core.Verdict{{
				RunID:        "run_test",
				HypothesisID: "h_test",
				EvidenceIDs:  []string{"e_test"},
			}},
			evidence: []core.Evidence{{ID: "e_test", RunID: "run_other"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := agenttest.NewFakeReporter(test.report).Report(context.Background(), run, test.verdicts, test.evidence)
			if err == nil {
				t.Fatal("build report: error = nil")
			}
		})
	}
}
