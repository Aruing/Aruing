package agent

import (
	"context"
	"encoding/json"
	"io"
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

// 记忆观测量 accessor（0.1.3 步骤 4 探针装置）：四条定位路径与实验臂口径
// 只读统计不改变应答行为；轮首重置保证每路径读到的是本轮值
func TestTowerMemoryStats(t *testing.T) {
	ctx := context.Background()
	ledger := store.NewMemoryRunLedger()
	if err := ledger.Put(ctx, session.DiagnosticRecord{
		RunID:     "run_d2",
		SessionID: "s",
		Report:    core.Report{Title: "旧诊断", Summary: "结论"},
		Evidence: []core.Evidence{{
			ID: "e_ev2", ToolName: "k8s", CommandView: "kubectl describe pod web-7d9f6c-xk2p9 -n demo",
			Summary: "pod web-7d9f6c-xk2p9 CrashLoopBackOff",
		}},
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	// 30 轮超预算长历史迫使组装压缩（视图丢细节前提）；第 2 轮带诊断消息
	history := make([]session.Message, 0, 62)
	for i := 0; i < 30; i++ {
		history = append(history, session.Message{
			Role: session.RoleUser, Content: strings.Repeat("用户内容", 400) + " 日常问答",
		})
		if i == 2 {
			history = append(history, session.Message{
				Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_d2",
				Content: "诊断结论，证据 e_ev2：" + strings.Repeat("细节", 400),
			})
			continue
		}
		history = append(history, session.Message{
			Role: session.RoleAssistant, Mode: session.ModeBaseline, Content: strings.Repeat("助手回答", 400),
		})
	}

	newTower := func(t *testing.T, handler http.HandlerFunc) *TowerResponder {
		tower, err := NewTowerResponder(newMockLLMClient(t, handler), newTestFactory(t), &fakeRunExecutor{}, ledger, nil, nil, nil)
		if err != nil {
			t.Fatalf("new tower: %v", err)
		}
		return tower
	}
	replyHandler := func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"action":"reply","content":"好的","question":""}`)
	}
	ask := func(t *testing.T, tower *TowerResponder, q string) MemoryTurnStats {
		t.Helper()
		out, err := tower.Respond(ctx, session.RespondInput{SessionID: "s", UserText: q, History: history})
		if err != nil {
			t.Fatalf("respond: %v", err)
		}
		if out.Mode != session.ModeBaseline {
			t.Fatalf("want baseline reply, got %s", out.Mode)
		}
		return tower.LastMemoryStats()
	}

	t.Run("lambda1 message hit", func(t *testing.T) {
		stats := ask(t, newTower(t, replyHandler), "run_d2 之前那一步说了什么")
		if stats.LocateLayer != "lambda1" || stats.RehydratedMsgs < 1 || stats.Lambda2Called {
			t.Fatalf("msg-hit stats wrong: %+v", stats)
		}
		if stats.Method != "ours" || stats.HistTurns != len(history) {
			t.Fatalf("base fields wrong: %+v", stats)
		}
	})

	t.Run("lambda1 evidence hit", func(t *testing.T) {
		stats := ask(t, newTower(t, replyHandler), "e_ev2 当时的完整输出是什么")
		if stats.LocateLayer != "lambda1" || stats.RehydratedEvidence < 1 {
			t.Fatalf("evidence-hit stats wrong: %+v", stats)
		}
	})

	t.Run("lambda2 fallback", func(t *testing.T) {
		// 同一客户端先答定位请求（含 timeline 载荷）再答动作决策
		tower := newTower(t, func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "timeline") {
				writeChatCompletion(w, `{"found":true,"lo":0,"hi":2}`)
				return
			}
			writeChatCompletion(w, `{"action":"reply","content":"好的","question":""}`)
		})
		stats := ask(t, tower, "之前那一步为什么排除镜像问题")
		if stats.LocateLayer != "lambda2" || !stats.Lambda2Called {
			t.Fatalf("lambda2 stats wrong: %+v", stats)
		}
	})

	t.Run("none", func(t *testing.T) {
		stats := ask(t, newTower(t, replyHandler), "现在集群状态怎么样")
		if stats.LocateLayer != "none" || stats.Lambda2Called || stats.RehydratedMsgs+stats.RehydratedEvidence != 0 {
			t.Fatalf("none stats wrong: %+v", stats)
		}
	})

	t.Run("d1 arm stays none", func(t *testing.T) {
		tower := newTower(t, replyHandler)
		if err := tower.SetMemoryMethod("d1-last-n", 5); err != nil {
			t.Fatalf("set method: %v", err)
		}
		stats := ask(t, tower, "e_ev2 当时的完整输出是什么")
		if stats.Method != "d1-last-n" || stats.LocateLayer != "none" {
			t.Fatalf("d1 stats wrong: %+v", stats)
		}
	})
}
