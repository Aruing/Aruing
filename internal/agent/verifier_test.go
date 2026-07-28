package agent

import (
	"context"
	"testing"

	"aruing/internal/core"
)

// 判断结果必须绑定猜想所在运行并保留证据引用，才能形成可回溯结论
func TestFakeVerifierVerify(t *testing.T) {
	verifier := NewFakeVerifier([]core.Verdict{{
		ID:           "verdict_demo",
		RunID:        "stale_run",
		HypothesisID: "h_demo",
		Result:       core.VerdictSupported,
		Reason:       "证据显示后端未就绪",
	}})
	hypotheses := []core.Hypothesis{{ID: "h_demo", RunID: "run_test"}}
	tasks := []core.Task{{ID: "task_pods", RunID: "run_test", Refs: []string{"target_demo", "h_demo"}}}
	evidence := []core.Evidence{{ID: "e_runtime", RunID: "run_test", TaskID: "task_pods"}}

	got, err := verifier.Verify(context.Background(), core.Query{}, hypotheses, tasks, evidence)
	if err != nil {
		t.Fatalf("verify evidence: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("verdicts length = %d, want 1", len(got))
	}
	if got[0].RunID != "run_test" || got[0].EvidenceIDs[0] != "e_runtime" {
		t.Errorf("verdict relation was not preserved: %#v", got[0])
	}

	// 多次验证不能共享证据编号列表，否则一次结果可能污染后续运行
	got[0].EvidenceIDs[0] = "changed"
	again, err := verifier.Verify(context.Background(), core.Query{}, hypotheses, tasks, evidence)
	if err != nil {
		t.Fatalf("verify evidence again: %v", err)
	}
	if again[0].EvidenceIDs[0] != "e_runtime" {
		t.Errorf("verdict template was mutated: %#v", again[0])
	}
}

// 判断只能引用同一运行中存在的猜想和证据，避免跨运行或悬空结论
func TestFakeVerifierValidate(t *testing.T) {
	// 代表校验：未知猜想、任务与证据链断裂、跨运行证据
	tests := []struct {
		name       string
		verdicts   []core.Verdict
		hypotheses []core.Hypothesis
		tasks      []core.Task
		evidence   []core.Evidence
	}{
		{
			name:     "unknown hypothesis",
			verdicts: []core.Verdict{{ID: "v_test", HypothesisID: "h_unknown"}},
		},
		{
			name:       "missing task evidence",
			verdicts:   []core.Verdict{{ID: "v_test", HypothesisID: "h_test"}},
			hypotheses: []core.Hypothesis{{ID: "h_test", RunID: "run_test"}},
			tasks:      []core.Task{{ID: "task_test", RunID: "run_test", Refs: []string{"h_test"}}},
		},
		{
			name:       "foreign evidence",
			verdicts:   []core.Verdict{{ID: "v_test", HypothesisID: "h_test"}},
			hypotheses: []core.Hypothesis{{ID: "h_test", RunID: "run_test"}},
			tasks:      []core.Task{{ID: "task_test", RunID: "run_test", Refs: []string{"h_test"}}},
			evidence:   []core.Evidence{{ID: "e_test", RunID: "run_other", TaskID: "task_test"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewFakeVerifier(test.verdicts).Verify(context.Background(), core.Query{}, test.hypotheses, test.tasks, test.evidence)
			if err == nil {
				t.Fatal("verify evidence: error = nil")
			}
		})
	}
}
