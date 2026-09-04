package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 台账有先前运行时用户载荷含 R 层索引卡：编号与摘要在场、raw 不在场；
// 解释追问直接回复且不调执行
func TestTowerPriorRunCardsFromLedger(t *testing.T) {
	ctx := context.Background()
	ledger := store.NewMemoryRunLedger()
	rec := session.DiagnosticRecord{
		RunID:     "run_prior",
		SessionID: "sess_deep",
		Question:  "demo-api 挂了",
		Report: core.Report{
			Title:   "镜像问题",
			Summary: "ImagePullBackOff",
			Conclusions: []core.Conclusion{{
				Result:      core.VerdictSupported,
				Reason:      "pull 失败",
				EvidenceIDs: []string{"e_pull"},
			}},
		},
		Evidence: []core.Evidence{{
			ID:       "e_pull",
			ToolName: "k8s",
			Summary:  "events: ImagePullBackOff",
			Raw:      json.RawMessage(`{"stdout":"Failed to pull image quay.io/demo"}`),
		}},
	}
	if err := ledger.Put(ctx, rec); err != nil {
		t.Fatalf("put: %v", err)
	}

	var sawUser string
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		var reqBody struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		for _, m := range reqBody.Messages {
			if m.Role == "user" {
				sawUser = m.Content
			}
		}
		writeChatCompletion(w, `{"action":"reply","content":"依据 e_pull 的事件摘要，上次判定镜像问题","question":""}`)
	})
	exec := &fakeRunExecutor{}
	tower, err := NewTowerResponder(client, newTestFactory(t), exec, ledger, nil, nil, nil)
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}

	out, err := tower.Respond(ctx, session.RespondInput{
		SessionID: "sess_deep",
		UserText:  "为什么上次那么判断，依据是什么",
		History: []session.Message{
			{Role: session.RoleUser, Content: "demo-api 挂了"},
			{
				Role:    session.RoleAssistant,
				Content: "根因：ImagePullBackOff",
				Mode:    session.ModeDiagnostic,
				RunID:   "run_prior",
			},
		},
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Mode != session.ModeBaseline {
		t.Fatalf("mode: %q", out.Mode)
	}
	if exec.lastRun.ID != "" {
		t.Fatal("executor must not run for explain reply")
	}
	if !strings.Contains(sawUser, "prior_run_details") {
		t.Fatalf("payload missing prior_run_details: %s", sawUser)
	}
	// 卡面语义：编号与摘要在场；raw（深细节）不在场，归回灌与 evidence.read
	if !strings.Contains(sawUser, "e_pull") || !strings.Contains(sawUser, "ImagePullBackOff") {
		t.Fatalf("payload should include card fields: %s", sawUser)
	}
	if strings.Contains(sawUser, "Failed to pull image quay.io/demo") {
		t.Fatalf("cards must not carry evidence raw: %s", sawUser)
	}
	if !strings.Contains(out.Content, "e_pull") {
		t.Fatalf("reply should cite evidence: %q", out.Content)
	}
}

// 空账本：既往运行明细为空；解释追问不调执行，不注入伪证据
func TestTowerEmptyLedgerNoPriorRuns(t *testing.T) {
	ctx := context.Background()
	ledger := store.NewMemoryRunLedger()

	var sawUser string
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		var reqBody struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		for _, m := range reqBody.Messages {
			if m.Role == "user" {
				sawUser = m.Content
			}
		}
		writeChatCompletion(w, `{"action":"reply","content":"本会话尚无正式诊断记录，可 escalate 后再追问依据","question":""}`)
	})
	exec := &fakeRunExecutor{}
	tower, err := NewTowerResponder(client, newTestFactory(t), exec, ledger, nil, nil, nil)
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}

	out, err := tower.Respond(ctx, session.RespondInput{
		SessionID: "sess_empty",
		UserText:  "上次结论依据是什么",
		History:   nil,
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Mode != session.ModeBaseline {
		t.Fatalf("mode: %q", out.Mode)
	}
	if exec.lastRun.ID != "" {
		t.Fatal("executor must not run when ledger empty")
	}
	var payload struct {
		PriorRunDetails []struct {
			RunID string `json:"run_id"`
		} `json:"prior_run_details"`
	}
	if err := json.Unmarshal([]byte(sawUser), &payload); err != nil {
		t.Fatalf("user payload not JSON: %v body=%s", err, sawUser)
	}
	if len(payload.PriorRunDetails) != 0 {
		t.Fatalf("want empty prior_run_details, got %d: %s", len(payload.PriorRunDetails), sawUser)
	}
}
