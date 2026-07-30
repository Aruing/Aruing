package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"aruing/internal/session"
)

func TestBuildTowerUserPayloadIncludesAllHistoryWhenShort(t *testing.T) {
	// 远超原 last-20 的条数，但内容很短 → 应全量进入 payload
	history := make([]session.Message, 0, 30)
	for i := 0; i < 30; i++ {
		history = append(history, session.Message{
			Role:    session.RoleUser,
			Content: "msg",
		})
	}
	raw, err := buildTowerUserPayload(session.RespondInput{
		UserText: "hi",
		History:  history,
	}, nil, nil)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	var p struct {
		History []towerHistMsg `json:"history"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.History) != 30 {
		t.Fatalf("history len: got %d want 30 (no last-N)", len(p.History))
	}
}

func TestBuildTowerUserPayloadPriorDiagnostics(t *testing.T) {
	history := []session.Message{
		{Role: session.RoleUser, Content: "查 demo-api"},
		{
			Role:    session.RoleAssistant,
			Content: "根因：镜像拉取失败",
			Mode:    session.ModeDiagnostic,
			RunID:   "run_1",
		},
		{Role: session.RoleUser, Content: "谢谢"},
		{
			Role:    session.RoleAssistant,
			Content: "不客气",
			Mode:    session.ModeBaseline,
		},
	}
	raw, err := buildTowerUserPayload(session.RespondInput{
		UserText: "为什么上次那么判断",
		History:  history,
	}, nil, nil)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	var p struct {
		Prior []towerPriorDiagnostic `json:"prior_diagnostics"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.Prior) != 1 {
		t.Fatalf("prior: got %d want 1", len(p.Prior))
	}
	if p.Prior[0].RunID != "run_1" || !strings.Contains(p.Prior[0].Summary, "镜像") {
		t.Fatalf("prior: %+v", p.Prior[0])
	}
}

func TestCompactL0TruncatesLongMessage(t *testing.T) {
	long := strings.Repeat("字", 5000)
	hist := []towerHistMsg{{
		Role:    session.RoleAssistant,
		Content: long,
		Mode:    session.ModeDiagnostic,
		RunID:   "run_x",
	}}
	out := compactL0(hist, 100, 40)
	if estimateTokens(out[0].Content) >= estimateTokens(long) {
		t.Fatal("expected truncation")
	}
	if !strings.Contains(out[0].Content, "truncated") {
		t.Fatalf("want truncated marker, got %q", out[0].Content[:min(80, len(out[0].Content))])
	}
}

func TestCompactL1FoldsNonDiagnosticFirst(t *testing.T) {
	hist := []towerHistMsg{
		{Role: session.RoleUser, Content: "闲聊很长" + strings.Repeat("x", 200)},
		{Role: session.RoleAssistant, Content: "闲聊答", Mode: session.ModeBaseline},
		{
			Role:    session.RoleAssistant,
			Content: "诊断结论要点",
			Mode:    session.ModeDiagnostic,
			RunID:   "run_keep",
		},
	}
	// 中等偏紧预算：应先折非诊断，诊断尽量不 fold
	out := compactL1(hist, 80, nil)
	folded := 0
	for _, m := range out {
		if strings.HasPrefix(m.Content, "[folded]") {
			folded++
		}
	}
	if folded == 0 {
		t.Fatalf("expected non-diagnostic fold, got %+v", out)
	}
	var diag *towerHistMsg
	for i := range out {
		if out[i].RunID == "run_keep" {
			diag = &out[i]
			break
		}
	}
	if diag == nil {
		t.Fatal("diagnostic message missing")
	}
	if strings.HasPrefix(diag.Content, "[folded]") {
		t.Fatal("diagnostic should not be folded as non-diagnostic")
	}
}

func TestBuildTowerContextViewOverBudgetAppliesCompact(t *testing.T) {
	history := make([]session.Message, 0, 40)
	for i := 0; i < 40; i++ {
		history = append(history, session.Message{
			Role:    session.RoleUser,
			Content: strings.Repeat("内容块", 50),
		})
	}
	history = append(history, session.Message{
		Role:    session.RoleAssistant,
		Content: "重要诊断摘要应可引用",
		Mode:    session.ModeDiagnostic,
		RunID:   "run_z",
	})

	hist, priors := buildTowerContextView(history, 200, 80, 40)
	if len(hist) == 0 {
		t.Fatal("empty hist")
	}
	// 超预算后应有折叠或截断痕迹，且 prior 仍指向诊断
	hasCompactMark := false
	for _, m := range hist {
		if strings.HasPrefix(m.Content, "[folded]") || strings.Contains(m.Content, "truncated") {
			hasCompactMark = true
			break
		}
	}
	if !hasCompactMark {
		// 若仍未超（估算宽松）则至少 prior 存在
		if estimateHistTokens(hist)+estimatePriorTokens(priors) > 200 {
			t.Fatal("over budget without compact marks")
		}
	}
	found := false
	for _, p := range priors {
		if p.RunID == "run_z" {
			found = true
			break
		}
	}
	if !found {
		// prior 从 hist 重生；诊断若未 fold 应在
		for _, m := range hist {
			if m.RunID == "run_z" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("diagnostic run_z lost; priors=%+v hist_tail=%+v", priors, hist[len(hist)-1])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
