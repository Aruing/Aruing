package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"aruing/internal/core"
	"aruing/internal/session"
	"aruing/internal/store"
)

// 账本记录映射为既往运行详情，含结论与证据编号与摘要
func TestBuildPriorRunDetails(t *testing.T) {
	records := []session.DiagnosticRecord{{
		RunID:     "run_1",
		SessionID: "sess_1",
		Question:  "demo-api 挂了",
		Report: core.Report{
			Title:   "镜像拉取失败",
			Summary: "ImagePullBackOff",
			Conclusions: []core.Conclusion{{
				Result:      core.VerdictSupported,
				Reason:      "事件含 Failed to pull image",
				EvidenceIDs: []string{"e_1"},
			}},
			Suggestions: []string{"检查镜像仓库凭证"},
		},
		Evidence: []core.Evidence{{
			ID:       "e_1",
			ToolName: "k8s",
			Summary:  "events: ImagePullBackOff",
			Raw:      json.RawMessage(`{"stdout":"Failed to pull image"}`),
		}},
	}}

	details := buildPriorRunDetails(records, 8_000)
	if len(details) != 1 {
		t.Fatalf("len: %d", len(details))
	}
	d := details[0]
	if d.RunID != "run_1" || d.Title != "镜像拉取失败" {
		t.Fatalf("detail: %+v", d)
	}
	if len(d.Conclusions) != 1 || d.Conclusions[0].EvidenceIDs[0] != "e_1" {
		t.Fatalf("conclusions: %+v", d.Conclusions)
	}
	if len(d.Evidence) != 1 || d.Evidence[0].ID != "e_1" {
		t.Fatalf("evidence: %+v", d.Evidence)
	}
	if d.Evidence[0].RawTruncated || !strings.Contains(string(d.Evidence[0].Raw), "Failed to pull") {
		t.Fatalf("raw should stay full: trunc=%v raw=%s", d.Evidence[0].RawTruncated, d.Evidence[0].Raw)
	}
}

// 多证据共享预算时优先保新；旧条截断/占位
func TestBuildPriorRunDetailsRawBudget(t *testing.T) {
	oldMark := "OLD_PRIOR_RAW"
	newMark := "NEW_PRIOR_RAW"
	oldRaw := json.RawMessage(`{"stdout":"` + oldMark + strings.Repeat("o", 200) + `"}`)
	newRaw := json.RawMessage(`{"stdout":"` + newMark + strings.Repeat("n", 200) + `"}`)
	records := []session.DiagnosticRecord{{
		RunID:  "run_old",
		Report: core.Report{Summary: "old"},
		Evidence: []core.Evidence{{
			ID: "e_old", Summary: "old", Raw: append(json.RawMessage(nil), oldRaw...),
		}},
	}, {
		RunID:  "run_new",
		Report: core.Report{Summary: "new"},
		Evidence: []core.Evidence{{
			ID: "e_new", Summary: "new", Raw: append(json.RawMessage(nil), newRaw...),
		}},
	}}

	details := buildPriorRunDetails(records, 60)
	if details[1].Evidence[0].RawTruncated || string(details[1].Evidence[0].Raw) != string(newRaw) {
		t.Fatalf("newest run evidence must stay full: trunc=%v", details[1].Evidence[0].RawTruncated)
	}
	if !details[0].Evidence[0].RawTruncated {
		t.Fatal("oldest run evidence must yield under tight budget")
	}
}

// 台账有先前运行时用户载荷含先前详情；解释追问直接回复且不调执行
func TestTowerPriorRunDetailsFromLedger(t *testing.T) {
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
		writeChatCompletion(w, `{"action":"reply","content":"依据 e_pull：Failed to pull image，上次判定镜像问题","question":""}`)
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
	if !strings.Contains(sawUser, "e_pull") || !strings.Contains(sawUser, "Failed to pull") {
		t.Fatalf("payload should include evidence points: %s", sawUser)
	}
	if !strings.Contains(sawUser, "ImagePullBackOff") {
		t.Fatalf("payload should include report summary: %s", sawUser)
	}
	if !strings.Contains(out.Content, "e_pull") && !strings.Contains(out.Content, "pull") {
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
