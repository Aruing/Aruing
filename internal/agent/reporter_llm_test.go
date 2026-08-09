package agent

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"aruing/internal/core"
)

func testReportRun() core.Run {
	return core.Run{
		ID:       "run_1",
		Question: "为什么 demo-api 无法访问？",
	}
}

func testReportVerdicts() []core.Verdict {
	return []core.Verdict{{
		ID:           "v_1",
		RunID:        "run_1",
		HypothesisID: "h_1",
		Result:       core.VerdictSupported,
		Reason:       "Pod 处于 CrashLoopBackOff",
		EvidenceIDs:  []string{"e_1", "e_2"},
	}}
}

func testReportEvidence() []core.Evidence {
	return []core.Evidence{
		{
			ID:          "e_1",
			RunID:       "run_1",
			TaskID:      "t_1",
			ToolName:    "fake.list_pods",
			CommandView: "kubectl get pods -l app=demo-api",
			Summary:     "Pod 处于 CrashLoopBackOff",
		},
		{
			ID:       "e_2",
			RunID:    "run_1",
			TaskID:   "t_2",
			ToolName: "k8s",
			Summary:  "日志出现 missing DATABASE_URL",
		},
	}
}

// 标准路径：回填编号、绑定运行，并保留与判决对齐的结论
func TestLLMReporterReport(t *testing.T) {
	body := `{
		"title":"demo-api 诊断报告",
		"summary":"后端 Pod 反复重启导致服务不可用",
		"conclusions":[{
			"hypothesis_id":"h_1",
			"result":"supported",
			"reason":"关键证据显示 Pod CrashLoopBackOff，日志缺少 DATABASE_URL",
			"evidence_ids":["e_1"]
		}],
		"suggestions":["检查 Deployment 环境变量与 Secret 引用"]
	}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	reporter, err := NewLLMReporter(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	got, err := reporter.Report(context.Background(), testReportRun(), testReportVerdicts(), testReportEvidence())
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !strings.HasPrefix(got.ID, "rep_") || got.RunID != "run_1" {
		t.Errorf("binding ID=%q RunID=%q", got.ID, got.RunID)
	}
	if got.Title != "demo-api 诊断报告" || len(got.Conclusions) != 1 {
		t.Errorf("title/conclusions not preserved: title=%q conclusions=%d", got.Title, len(got.Conclusions))
	}
	if got.Conclusions[0].HypothesisID != "h_1" || got.Conclusions[0].Result != core.VerdictSupported {
		t.Errorf("conclusion = %+v", got.Conclusions[0])
	}
}

// 构造期依赖缺失应失败
func TestLLMReporterNewRequiresDeps(t *testing.T) {
	if _, err := NewLLMReporter(nil, newTestFactory(t)); err == nil {
		t.Fatal("nil client should fail")
	}
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {})
	if _, err := NewLLMReporter(client, nil); err == nil {
		t.Fatal("nil factory should fail")
	}
}

// 非法模型输出触发业务重试并最终报输出不一致
func TestLLMReporterInvalidOutput(t *testing.T) {
	// 代表几类校验：改写结果、未知证据、漏结论、空标题
	tests := []struct {
		name string
		body string
	}{
		{
			name: "result mismatch",
			body: `{"title":"t","summary":"s","conclusions":[{"hypothesis_id":"h_1","result":"refuted","reason":"x","evidence_ids":["e_1"]}],"suggestions":[]}`,
		},
		{
			name: "evidence outside verdict",
			body: `{"title":"t","summary":"s","conclusions":[{"hypothesis_id":"h_1","result":"supported","reason":"x","evidence_ids":["e_unknown"]}],"suggestions":[]}`,
		},
		{
			name: "missing conclusion",
			body: `{"title":"t","summary":"s","conclusions":[],"suggestions":[]}`,
		},
		{
			name: "empty title",
			body: `{"title":"","summary":"s","conclusions":[{"hypothesis_id":"h_1","result":"supported","reason":"x","evidence_ids":["e_1"]}],"suggestions":[]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := test.body
			client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeChatCompletion(w, body)
			})
			reporter, err := NewLLMReporter(client, newTestFactory(t))
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			_, err = reporter.Report(context.Background(), testReportRun(), testReportVerdicts(), testReportEvidence())
			if !errors.Is(err, ErrLLMOutputInconsistent) {
				t.Fatalf("err = %v, want ErrLLMOutputInconsistent", err)
			}
		})
	}
}

// 语义违规后干净输出可在业务重试内恢复；持续违规会耗尽重试
func TestLLMReporterRetry(t *testing.T) {
	t.Run("then success", func(t *testing.T) {
		dirty := `{"title":"t","summary":"s","conclusions":[{"hypothesis_id":"h_1","result":"refuted","reason":"x","evidence_ids":["e_1"]}],"suggestions":[]}`
		clean := `{"title":"ok","summary":"summary ok","conclusions":[{"hypothesis_id":"h_1","result":"supported","reason":"ok","evidence_ids":["e_1","e_2"]}],"suggestions":["restart carefully after fix"]}`
		var calls atomic.Int32
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				writeChatCompletion(w, dirty)
				return
			}
			writeChatCompletion(w, clean)
		})
		reporter, err := NewLLMReporter(client, newTestFactory(t))
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		got, err := reporter.Report(context.Background(), testReportRun(), testReportVerdicts(), testReportEvidence())
		if err != nil {
			t.Fatalf("report: %v", err)
		}
		if got.Title != "ok" || calls.Load() != 2 {
			t.Errorf("title=%q calls=%d", got.Title, calls.Load())
		}
	})

	t.Run("exhausted", func(t *testing.T) {
		body := `{"title":"t","summary":"s","conclusions":[{"hypothesis_id":"h_1","result":"refuted","reason":"x","evidence_ids":["e_1"]}],"suggestions":[]}`
		var calls atomic.Int32
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			writeChatCompletion(w, body)
		})
		reporter, err := NewLLMReporter(client, newTestFactory(t))
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		_, err = reporter.Report(context.Background(), testReportRun(), testReportVerdicts(), testReportEvidence())
		if !errors.Is(err, ErrLLMOutputInconsistent) {
			t.Fatalf("err = %v", err)
		}
		if calls.Load() != maxReportAttempts {
			t.Errorf("calls = %d, want %d", calls.Load(), maxReportAttempts)
		}
	})
}

// 判决无证据时在入口拒绝，不进入大模型
func TestLLMReporterVerdictWithoutEvidence(t *testing.T) {
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeChatCompletion(w, `{}`)
	})
	reporter, err := NewLLMReporter(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	verdicts := []core.Verdict{{
		ID:           "v_empty",
		RunID:        "run_1",
		HypothesisID: "h_1",
		Result:       core.VerdictInsufficient,
		Reason:       "no data",
		EvidenceIDs:  nil,
	}}
	_, err = reporter.Report(context.Background(), testReportRun(), verdicts, testReportEvidence())
	if err == nil || errors.Is(err, ErrLLMOutputInconsistent) {
		t.Fatalf("want pre-LLM rejection, got %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("LLM calls = %d, want 0", calls.Load())
	}
}
