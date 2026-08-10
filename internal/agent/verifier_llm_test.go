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

// 标准路径：回填编号、绑定猜想，并保留结果与证据
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
	if !strings.HasPrefix(v.ID, "v_") || v.HypothesisID != "h_1" {
		t.Errorf("binding ID=%q hyp=%q", v.ID, v.HypothesisID)
	}
	if v.Result != core.VerdictSupported || len(v.EvidenceIDs) != 1 || v.EvidenceIDs[0] != "e_1" {
		t.Errorf("result/evidence = %+v", v)
	}
}

// 多猜想须各一条判决
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
	if len(got) != 2 || got[0].HypothesisID != "h_1" || got[1].HypothesisID != "h_2" {
		t.Errorf("got = %+v", got)
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

// 非法模型输出触发业务重试并最终报输出不一致
func TestLLMVerifierInvalidOutput(t *testing.T) {
	// 代表几类校验：未知证据、漏判、非法结果、空证据编号
	tests := []struct {
		name string
		body string
	}{
		{
			name: "unknown evidence",
			body: `{"verdicts":[{"hypothesis_id":"h_1","result":"supported","reason":"x","evidence_ids":["e_missing"]}]}`,
		},
		{
			name: "missing hypothesis",
			body: `{"verdicts":[]}`,
		},
		{
			name: "invalid result",
			body: `{"verdicts":[{"hypothesis_id":"h_1","result":"maybe","reason":"x","evidence_ids":["e_1"]}]}`,
		},
		{
			name: "empty evidence ids",
			body: `{"verdicts":[{"hypothesis_id":"h_1","result":"insufficient","reason":"no data","evidence_ids":[]}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := test.body
			client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeChatCompletion(w, body)
			})
			verifier, err := NewLLMVerifier(client, newTestFactory(t))
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			_, err = verifier.Verify(context.Background(), testVerifyQuery(), testVerifyHypotheses(), testVerifyTasks(), testVerifyEvidence())
			if !errors.Is(err, ErrLLMOutputInconsistent) {
				t.Fatalf("err = %v, want ErrLLMOutputInconsistent", err)
			}
		})
	}
}

// 语义违规后干净输出可在业务重试内恢复
func TestLLMVerifierRetryThenSuccess(t *testing.T) {
	dirty := `{"verdicts":[{"hypothesis_id":"h_1","result":"supported","reason":"x","evidence_ids":["e_missing"]}]}`
	clean := `{"verdicts":[{"hypothesis_id":"h_1","result":"refuted","reason":"Pod 正常 Running","evidence_ids":["e_1"]}]}`
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
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
	if len(got) != 1 || got[0].Result != core.VerdictRefuted || calls.Load() != 2 {
		t.Errorf("got=%+v calls=%d", got, calls.Load())
	}
}

// 验证的用户载荷必须含问题与目标，让模型看到用户问题
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
	if !strings.Contains(captured, "query") || !strings.Contains(captured, "定位 demo-api") {
		t.Errorf("payload missing query/goal: %s", captured)
	}
}
