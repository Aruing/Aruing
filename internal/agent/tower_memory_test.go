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

// 记忆方法分派（Respond 级）：D1 纯对照臂——无卡片无回灌只保窗口；ours 默认注入卡片
func TestTowerRespondMemoryDispatch(t *testing.T) {
	ctx := context.Background()
	ledger := store.NewMemoryRunLedger()
	if err := ledger.Put(ctx, session.DiagnosticRecord{
		RunID:     "run_deep",
		SessionID: "s",
		Report:    core.Report{Title: "旧诊断", Summary: "结论 CARD_MARK"},
		Evidence: []core.Evidence{{
			ID: "e_deep", ToolName: "k8s", Summary: "events: CARD_EV",
		}},
	}); err != nil {
		t.Fatalf("put: %v", err)
	}

	// 30 轮长历史 + 追问旧诊断编号：ours 会压缩并触发回灌与卡片
	history := longHistory(30, map[int]string{2: "e_2"})

	newTower := func(t *testing.T, method string, lastN int, capture *string) *TowerResponder {
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
					*capture = m.Content
				}
			}
			writeChatCompletion(w, `{"action":"reply","content":"好的","question":""}`)
		})
		tower, err := NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, ledger, nil, nil, nil)
		if err != nil {
			t.Fatalf("new tower: %v", err)
		}
		if err := tower.SetMemoryMethod(method, lastN); err != nil {
			t.Fatalf("set method: %v", err)
		}
		return tower
	}

	t.Run("d1 baseline no cards no rehydrate", func(t *testing.T) {
		var payload string
		tower := newTower(t, "d1-last-n", 4, &payload)
		if _, err := tower.Respond(ctx, session.RespondInput{
			SessionID: "s", UserText: "run_2 那步怎么回事", History: history,
		}); err != nil {
			t.Fatalf("respond: %v", err)
		}
		var p struct {
			History []json.RawMessage `json:"history"`
			Priors  []json.RawMessage `json:"prior_run_details"`
			Rehy    json.RawMessage   `json:"rehydrated_messages"`
		}
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			t.Fatalf("payload: %v", err)
		}
		if len(p.History) != 4 {
			t.Fatalf("D1 window = last_n msgs, got %d", len(p.History))
		}
		if len(p.Priors) != 0 {
			t.Fatalf("D1 must not inject cards: %s", payload)
		}
		if strings.TrimSpace(string(p.Rehy)) != "" && string(p.Rehy) != "null" {
			t.Fatalf("D1 must not rehydrate: %s", payload)
		}
	})

	t.Run("ours default injects cards", func(t *testing.T) {
		var payload string
		tower := newTower(t, "", 0, &payload)
		if _, err := tower.Respond(ctx, session.RespondInput{
			SessionID: "s", UserText: "上次诊断结论是什么", History: history,
		}); err != nil {
			t.Fatalf("respond: %v", err)
		}
		if !strings.Contains(payload, "CARD_MARK") || !strings.Contains(payload, "e_deep") {
			t.Fatalf("ours must inject memory cards: %s", payload)
		}
	})

	t.Run("unknown method rejected", func(t *testing.T) {
		var payload string
		tower := newTower(t, "", 0, &payload)
		if err := tower.SetMemoryMethod("magic", 0); err == nil {
			t.Fatal("unknown method must error")
		}
	})
}
