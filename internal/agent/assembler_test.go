package agent

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
)

// 构造 n 轮（用户+助手）长历史，指定轮的诊断消息带证据编号
func longHistory(rounds int, diagRounds map[int]string) []session.Message {
	history := make([]session.Message, 0, rounds*2)
	for i := 0; i < rounds; i++ {
		history = append(history, session.Message{
			Role:    session.RoleUser,
			Content: strings.Repeat("用户内容", 30) + " 日常问答",
		})
		if ev, ok := diagRounds[i]; ok {
			history = append(history, session.Message{
				Role:    session.RoleAssistant,
				Mode:    session.ModeDiagnostic,
				RunID:   "run_d" + string(rune('0'+i)),
				Content: "诊断结论，证据 " + ev,
			})
			continue
		}
		history = append(history, session.Message{
			Role:    session.RoleAssistant,
			Mode:    session.ModeBaseline,
			Content: strings.Repeat("助手回答", 30),
		})
	}
	return history
}

// 短历史预算内全量注入，不产 checkpoint（与既有行为等价）；卡片原样返回
func TestAssembleTieredViewShortHistory(t *testing.T) {
	history := longHistory(3, map[int]string{1: "e_11"})
	records := []session.DiagnosticRecord{{
		RunID:  "run_d1",
		Report: core.Report{Title: "t", Summary: "s"},
	}}
	view, cards, err := assembleTieredView(context.Background(), nil, history, records, memoryOptions{})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(view.Hist) != len(history) {
		t.Fatalf("short history must stay full: %d vs %d", len(view.Hist), len(history))
	}
	if view.CheckpointContent != "" {
		t.Fatalf("short history must not produce checkpoint: %q", view.CheckpointContent)
	}
	if len(cards) != 1 || cards[0].RunID != "run_d1" {
		t.Fatalf("cards: %+v", cards)
	}
}

// 长历史触发分层：R 卡片锁定返回 + W 窗口原文 + C 中段摘要（C1 兜地址）+ 总预算受控
func TestAssembleTieredViewTiered(t *testing.T) {
	history := longHistory(30, map[int]string{2: "e_2", 20: "e_20"})
	records := []session.DiagnosticRecord{{
		RunID:  "run_d2",
		Report: core.Report{Title: "旧诊断", Summary: "结论"},
	}}
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		// 模型摘要不带任何编号：C 层只能靠 C1 机械补附
		writeChatCompletion(w, `{"summary":"早期都是日常问答","run_ids":[],"open_questions":[]}`)
	})

	opts := memoryOptions{budgetTokens: 2_000, windowRounds: 3}
	view, cards, err := assembleTieredView(context.Background(), client, history, records, opts)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}

	// R 层：卡片锁定返回（不因历史超预算而丢弃）
	if len(cards) != 1 || cards[0].RunID != "run_d2" {
		t.Fatalf("cards must survive: %+v", cards)
	}

	// 结构：[checkpoint(C)] + W（最近 3 轮 ≈ 6 条）
	if len(view.Hist) < 2 || view.Hist[0].Mode != session.ModeCheckpoint {
		t.Fatalf("expected checkpoint first: %+v", view.Hist)
	}
	if len(view.Hist)-1 > 8 {
		t.Fatalf("window too large: %d msgs", len(view.Hist)-1)
	}
	// W 含最近一轮原文
	if !containsContent(view.Hist[1:], "日常问答") {
		t.Fatalf("recent rounds must be verbatim: %+v", view.Hist[1:])
	}

	// C 层：中段被摘要，checkpoint 落库正文含中段全部地址（模型没带 → C1 补附）
	for _, want := range []string{"run_d2", "e_2"} {
		if !strings.Contains(view.CheckpointContent, want) {
			t.Fatalf("checkpoint lost %s: %q", want, view.CheckpointContent)
		}
	}
	// e_20 在第 20 轮，也在中段（W 只保最近 3 轮）
	if !strings.Contains(view.CheckpointContent, "e_20") {
		t.Fatalf("checkpoint lost e_20: %q", view.CheckpointContent)
	}

	// 总预算受控：卡片 + 注入历史 ≤ B
	total := estimatePriorRunTokens(cards) + estimateHistTokens(view.Hist) + estimatePriorTokens(view.Priors)
	if total > opts.budgetTokens+200 {
		t.Fatalf("over budget: %d > %d", total, opts.budgetTokens)
	}
}

// 无客户端路径：中段退化 L0/L1 压缩不报错，不产 checkpoint
func TestAssembleTieredViewNilClient(t *testing.T) {
	history := longHistory(30, nil)
	view, cards, err := assembleTieredView(context.Background(), nil, history, nil, memoryOptions{budgetTokens: 1_500})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if view.CheckpointContent != "" {
		t.Fatalf("nil client must not produce checkpoint: %q", view.CheckpointContent)
	}
	if len(cards) != 0 {
		t.Fatalf("no records no cards: %+v", cards)
	}
	if len(view.Hist) != len(history) {
		t.Fatalf("message count must not change: %d vs %d", len(view.Hist), len(history))
	}
}

// D1：只保留最近 N 条原文，无 checkpoint / 卡片 / 折叠
func TestAssembleLastN(t *testing.T) {
	history := longHistory(30, map[int]string{1: "e_old"})
	view := assembleLastN(history, 6)
	if len(view.Hist) != 6 {
		t.Fatalf("want 6 msgs got %d", len(view.Hist))
	}
	if view.CheckpointContent != "" {
		t.Fatal("D1 must not produce checkpoint")
	}
	// 旧内容不可见：第 1 轮的证据编号不在窗口
	for _, m := range view.Hist {
		if strings.Contains(m.Content, "e_old") || strings.Contains(m.Content, "[folded]") {
			t.Fatalf("D1 window must be verbatim tail only: %+v", m)
		}
	}
	// 少于 N 条时全量
	short := longHistory(2, nil)
	if v := assembleLastN(short, 20); len(v.Hist) != 4 {
		t.Fatalf("short history must stay full: %d", len(v.Hist))
	}
}

// D2：全历史一段摘要常驻（C1 兜地址），无窗口
func TestAssembleFlatSummary(t *testing.T) {
	history := longHistory(10, map[int]string{3: "e_3"})
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"summary":"全程对话概览","run_ids":[],"open_questions":[]}`)
	})
	view, err := assembleFlatSummary(context.Background(), client, history, 1_000)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(view.Hist) != 1 || view.Hist[0].Mode != session.ModeCheckpoint {
		t.Fatalf("D2 view must be single checkpoint: %+v", view.Hist)
	}
	if !strings.Contains(view.CheckpointContent, "e_3") {
		t.Fatalf("flat summary lost addr: %q", view.CheckpointContent)
	}
	// 无客户端退化路径可用
	if _, err := assembleFlatSummary(context.Background(), nil, history, 500); err != nil {
		t.Fatalf("nil client: %v", err)
	}
}

// 方法解析：空 = ours；未知报错
func TestParseMemoryMethod(t *testing.T) {
	for in, want := range map[string]MemoryMethod{
		"":                MemoryMethodOurs,
		"ours":            MemoryMethodOurs,
		"d1-last-n":       MemoryMethodD1LastN,
		"d2-flat-summary": MemoryMethodD2FlatSummary,
	} {
		got, err := ParseMemoryMethod(in)
		if err != nil || got != want {
			t.Fatalf("parse %q: got %v err %v want %v", in, got, err, want)
		}
	}
	if _, err := ParseMemoryMethod("last-n"); err == nil {
		t.Fatal("unknown method must error")
	}
}

// W 窗口取轮：预算不足从旧侧整轮丢弃，保最后一轮
func TestTakeWindowRounds(t *testing.T) {
	hist := messagesToHist(longHistory(10, nil))
	win := takeWindowRounds(hist, 3, 1_000_000)
	if len(win) != 6 {
		t.Fatalf("3 rounds = 6 msgs, got %d", len(win))
	}
	// 极小预算：逐轮丢弃到只剩最后一轮（2 条），不丢段
	tight := takeWindowRounds(hist, 3, 10)
	if len(tight) < 2 {
		t.Fatalf("must keep at least last round: %d", len(tight))
	}
	if tight[len(tight)-2].Role != session.RoleUser {
		t.Fatalf("window must align on round boundary: %+v", tight[:2])
	}
}

func containsContent(msgs []towerHistMsg, sub string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, sub) {
			return true
		}
	}
	return false
}
