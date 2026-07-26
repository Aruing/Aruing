package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"aruing/internal/core"
)

func testVerifyHypotheses() []core.Hypothesis {
	return []core.Hypothesis{{
		ID:        "h_1",
		RunID:     "run_1",
		Statement: "后端 Pod 未就绪",
		Reason:    "服务不可访问时优先检查后端",
	}}
}

func testVerifyTasks() []core.Task {
	return []core.Task{{
		ID:       "t_1",
		RunID:    "run_1",
		Refs:     []string{"h_1"},
		ToolName: "fake.list_pods",
		Purpose:  "检查 Pod",
	}}
}

func testVerifyEvidence() []core.Evidence {
	return []core.Evidence{{
		ID:       "e_1",
		RunID:    "run_1",
		TaskID:   "t_1",
		ToolName: "fake.list_pods",
		Summary:  "Pod 处于 CrashLoopBackOff",
	}}
}

func testVerifyQuery() core.Query {
	return core.Query{ID: "query_1", RunID: "run_1", Goal: "定位 demo-api 无法访问的原因"}
}

// 标准路径：为每条猜想回填系统编号的判断，并保留证据引用
func TestLLMVerifierVerify(t *testing.T) {
	body := `{
		"verdicts":[{
			"hypothesis_id":"h_1",
			"result":"supported",
			"reason":"证据显示 Pod CrashLoopBackOff",
			"evidence_ids":["e_1"]
		}]
	}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	verifier, err := NewLLMVerifier(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	got, err := verifier.Verify(context.Background(), testVerifyQuery(), testVerifyHypotheses(), testVerifyTasks(), testVerifyEvidence())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	v := got[0]
	if !strings.HasPrefix(v.ID, "v_") {
		t.Errorf("verdict ID = %q", v.ID)
	}
	if v.RunID != "run_1" || v.HypothesisID != "h_1" {
		t.Errorf("binding = %+v", v)
	}
	if v.Result != core.VerdictSupported {
		t.Errorf("result = %q", v.Result)
	}
	if v.Reason != "证据显示 Pod CrashLoopBackOff" {
		t.Errorf("reason = %q", v.Reason)
	}
	if len(v.EvidenceIDs) != 1 || v.EvidenceIDs[0] != "e_1" {
		t.Errorf("evidence = %v", v.EvidenceIDs)
	}
	if v.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
}

// 构造期依赖缺失应失败
func TestLLMVerifierNewRequiresDeps(t *testing.T) {
	if _, err := NewLLMVerifier(nil, newTestFactory(t)); err == nil {
		t.Fatal("nil client should fail")
	}
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {})
	if _, err := NewLLMVerifier(client, nil); err == nil {
		t.Fatal("nil factory should fail")
	}
}

// 证据编号必须存在于输入；未知证据触发业务重试后失败
func TestLLMVerifierUnknownEvidence(t *testing.T) {
	body := `{
		"verdicts":[{
			"hypothesis_id":"h_1",
			"result":"supported",
			"reason":"x",
			"evidence_ids":["e_missing"]
		}]
	}`
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeChatCompletion(w, body)
	})
	verifier, err := NewLLMVerifier(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = verifier.Verify(context.Background(), testVerifyQuery(), testVerifyHypotheses(), testVerifyTasks(), testVerifyEvidence())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Errorf("err = %v", err)
	}
	if calls.Load() != maxVerifyAttempts {
		t.Errorf("calls = %d, want %d", calls.Load(), maxVerifyAttempts)
	}
}

// 必须覆盖每条猜想；漏判触发业务重试
func TestLLMVerifierMissingHypothesis(t *testing.T) {
	body := `{"verdicts":[]}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	verifier, err := NewLLMVerifier(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = verifier.Verify(context.Background(), testVerifyQuery(), testVerifyHypotheses(), testVerifyTasks(), testVerifyEvidence())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Errorf("err = %v", err)
	}
}

// result 枚举非法应拒绝
func TestLLMVerifierInvalidResult(t *testing.T) {
	body := `{
		"verdicts":[{
			"hypothesis_id":"h_1",
			"result":"maybe",
			"reason":"x",
			"evidence_ids":["e_1"]
		}]
	}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	verifier, err := NewLLMVerifier(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = verifier.Verify(context.Background(), testVerifyQuery(), testVerifyHypotheses(), testVerifyTasks(), testVerifyEvidence())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Errorf("err = %v", err)
	}
}

// 空 evidence_ids 不合法
func TestLLMVerifierEmptyEvidenceIDs(t *testing.T) {
	body := `{
		"verdicts":[{
			"hypothesis_id":"h_1",
			"result":"insufficient",
			"reason":"no data",
			"evidence_ids":[]
		}]
	}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	verifier, err := NewLLMVerifier(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = verifier.Verify(context.Background(), testVerifyQuery(), testVerifyHypotheses(), testVerifyTasks(), testVerifyEvidence())
	if err == nil {
		t.Fatal("expected error")
	}
}

// 语义违规后干净输出可在业务重试内恢复
func TestLLMVerifierRetryThenSuccess(t *testing.T) {
	dirty := `{
		"verdicts":[{
			"hypothesis_id":"h_1",
			"result":"supported",
			"reason":"x",
			"evidence_ids":["e_missing"]
		}]
	}`
	clean := `{
		"verdicts":[{
			"hypothesis_id":"h_1",
			"result":"refuted",
			"reason":"Pod 正常 Running",
			"evidence_ids":["e_1"]
		}]
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
	verifier, err := NewLLMVerifier(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	got, err := verifier.Verify(context.Background(), testVerifyQuery(), testVerifyHypotheses(), testVerifyTasks(), testVerifyEvidence())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(got) != 1 || got[0].Result != core.VerdictRefuted {
		t.Errorf("got = %+v", got)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d", calls.Load())
	}
}

// 多猜想须各一条 verdict
func TestLLMVerifierMultiHypothesis(t *testing.T) {
	hyps := []core.Hypothesis{
		{ID: "h_1", RunID: "run_1", Statement: "A"},
		{ID: "h_2", RunID: "run_1", Statement: "B"},
	}
	body := `{
		"verdicts":[
			{"hypothesis_id":"h_1","result":"supported","reason":"a","evidence_ids":["e_1"]},
			{"hypothesis_id":"h_2","result":"insufficient","reason":"b","evidence_ids":["e_1"]}
		]
	}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	verifier, err := NewLLMVerifier(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	got, err := verifier.Verify(context.Background(), testVerifyQuery(), hyps, testVerifyTasks(), testVerifyEvidence())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].HypothesisID != "h_1" || got[1].HypothesisID != "h_2" {
		t.Errorf("order/ids = %+v", got)
	}
}

// Verify 的 user payload 必须含 query 字段（goal），让 LLM 看到用户原始问题
func TestLLMVerifierPayloadHasQuery(t *testing.T) {
	var captured string
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		captured = string(raw)
		writeChatCompletion(w, `{"verdicts":[{"hypothesis_id":"h_1","result":"supported","reason":"ok","evidence_ids":["e_1"]}]}`)
	})
	v, err := NewLLMVerifier(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := v.Verify(context.Background(), testVerifyQuery(), testVerifyHypotheses(), testVerifyTasks(), testVerifyEvidence()); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// goal 含中文内容，确保验证 path 传到了 json 的 user 负载中
	if !strings.Contains(captured, "query") || !strings.Contains(captured, "定位 demo-api") {
		t.Errorf("payload missing query/goal: %s", captured)
	}
}
