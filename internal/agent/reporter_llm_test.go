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

// 标准路径：回填 rep_ 编号，结论与 Verdict 对齐，证据可取子集
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
	if !strings.HasPrefix(got.ID, "rep_") {
		t.Errorf("report ID = %q", got.ID)
	}
	if got.RunID != "run_1" {
		t.Errorf("runID = %q", got.RunID)
	}
	if got.Title != "demo-api 诊断报告" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Summary == "" {
		t.Error("summary should not be empty")
	}
	if len(got.Conclusions) != 1 {
		t.Fatalf("conclusions len = %d", len(got.Conclusions))
	}
	c := got.Conclusions[0]
	if c.HypothesisID != "h_1" || c.Result != core.VerdictSupported {
		t.Errorf("conclusion = %+v", c)
	}
	if len(c.EvidenceIDs) != 1 || c.EvidenceIDs[0] != "e_1" {
		t.Errorf("evidence = %v", c.EvidenceIDs)
	}
	if len(got.Suggestions) != 1 {
		t.Errorf("suggestions = %v", got.Suggestions)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
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

// 改写 result 触发业务重试后失败
func TestLLMReporterResultMismatch(t *testing.T) {
	body := `{
		"title":"t",
		"summary":"s",
		"conclusions":[{
			"hypothesis_id":"h_1",
			"result":"refuted",
			"reason":"x",
			"evidence_ids":["e_1"]
		}],
		"suggestions":[]
	}`
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
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Errorf("err = %v", err)
	}
	if calls.Load() != maxReportAttempts {
		t.Errorf("calls = %d, want %d", calls.Load(), maxReportAttempts)
	}
}

// 引用不在 Verdict 证据集中的 evidence 应拒绝
func TestLLMReporterEvidenceOutsideVerdict(t *testing.T) {
	body := `{
		"title":"t",
		"summary":"s",
		"conclusions":[{
			"hypothesis_id":"h_1",
			"result":"supported",
			"reason":"x",
			"evidence_ids":["e_unknown"]
		}],
		"suggestions":[]
	}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	reporter, err := NewLLMReporter(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = reporter.Report(context.Background(), testReportRun(), testReportVerdicts(), testReportEvidence())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Errorf("err = %v", err)
	}
}

// 漏写结论触发业务重试
func TestLLMReporterMissingConclusion(t *testing.T) {
	body := `{"title":"t","summary":"s","conclusions":[],"suggestions":[]}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	reporter, err := NewLLMReporter(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = reporter.Report(context.Background(), testReportRun(), testReportVerdicts(), testReportEvidence())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Errorf("err = %v", err)
	}
}

// 空 title 不合法
func TestLLMReporterEmptyTitle(t *testing.T) {
	body := `{
		"title":"",
		"summary":"s",
		"conclusions":[{
			"hypothesis_id":"h_1",
			"result":"supported",
			"reason":"x",
			"evidence_ids":["e_1"]
		}],
		"suggestions":[]
	}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	reporter, err := NewLLMReporter(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = reporter.Report(context.Background(), testReportRun(), testReportVerdicts(), testReportEvidence())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Errorf("err = %v", err)
	}
}

// 语义违规后干净输出可在业务重试内恢复
func TestLLMReporterRetryThenSuccess(t *testing.T) {
	dirty := `{
		"title":"t",
		"summary":"s",
		"conclusions":[{
			"hypothesis_id":"h_1",
			"result":"refuted",
			"reason":"x",
			"evidence_ids":["e_1"]
		}],
		"suggestions":[]
	}`
	clean := `{
		"title":"ok",
		"summary":"summary ok",
		"conclusions":[{
			"hypothesis_id":"h_1",
			"result":"supported",
			"reason":"ok",
			"evidence_ids":["e_1","e_2"]
		}],
		"suggestions":["restart carefully after fix"]
	}`
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
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
	if got.Title != "ok" {
		t.Errorf("title = %q", got.Title)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

// 取消上下文应立即失败
func TestLLMReporterCanceledContext(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{}`)
	})
	reporter, err := NewLLMReporter(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = reporter.Report(ctx, testReportRun(), testReportVerdicts(), testReportEvidence())
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrLLMOutputInconsistent) {
		t.Errorf("should not be inconsistent: %v", err)
	}
}

// Verdict 无证据时在入口拒绝，不进入 LLM 重试
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
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "requires evidence") {
		t.Errorf("err = %v", err)
	}
	if errors.Is(err, ErrLLMOutputInconsistent) {
		t.Errorf("should fail before LLM retries: %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("LLM calls = %d, want 0", calls.Load())
	}
}
