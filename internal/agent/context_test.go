package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"aruing/internal/session"
)

// 短消息远超原 last-20 条数时，预算内应全量进 payload，禁止 last-N
func TestTowerPayloadHistory(t *testing.T) {
	history := make([]session.Message, 0, 30)
	for i := 0; i < 30; i++ {
		history = append(history, session.Message{
			Role:    session.RoleUser,
			Content: "msg",
		})
	}
	view, err := prepareTowerContext(context.Background(), nil, history, defaultTowerContextBudgetTokens, 0, 0)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	raw, err := buildTowerUserPayload(session.RespondInput{
		UserText: "hi",
		History:  history,
	}, view, nil, nil)
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

// prior_diagnostics 只收录诊断助手消息，不含基线闲聊
func TestTowerPayloadPrior(t *testing.T) {
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
	view, err := prepareTowerContext(context.Background(), nil, history, defaultTowerContextBudgetTokens, 0, 0)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	raw, err := buildTowerUserPayload(session.RespondInput{
		UserText: "为什么上次那么判断",
		History:  history,
	}, view, nil, nil)
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

// L0 对超长单条打 truncated，体积须小于原文
func TestCompactL0(t *testing.T) {
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
		n := min(80, len(out[0].Content))
		t.Fatalf("want truncated marker, got %q", out[0].Content[:n])
	}
}

// L1 优先折叠非诊断；带 RunID 的诊断不得当非诊断 fold
func TestCompactL1(t *testing.T) {
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
	// 中等偏紧预算：应先折非诊断
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

// 超预算时 L0/L1 留下 compact 痕迹，且诊断 run 不丢
func TestTowerContextBudget(t *testing.T) {
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
	hasCompactMark := false
	for _, m := range hist {
		if strings.HasPrefix(m.Content, "[folded]") || strings.Contains(m.Content, "truncated") {
			hasCompactMark = true
			break
		}
	}
	if !hasCompactMark {
		// 估算宽松时可能未触发标记，但若仍超预算则失败
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

// L2：紧预算 + mock LLM → handoff checkpoint 与近期原文
func TestPrepareTowerContextL2(t *testing.T) {
	history := make([]session.Message, 0, 30)
	for i := 0; i < 24; i++ {
		history = append(history, session.Message{
			Role:    session.RoleUser,
			Content: strings.Repeat("旧闲聊块", 40),
		})
		history = append(history, session.Message{
			Role:    session.RoleAssistant,
			Content: strings.Repeat("旧基线答", 40),
			Mode:    session.ModeBaseline,
		})
	}
	history = append(history, session.Message{
		Role:    session.RoleUser,
		Content: "最早诊断问题",
	})
	history = append(history, session.Message{
		Role:    session.RoleAssistant,
		Content: "根因：镜像拉取失败，请检查 ImagePullSecrets",
		Mode:    session.ModeDiagnostic,
		RunID:   "run_keep",
	})
	history = append(history, session.Message{Role: session.RoleUser, Content: "最近一句"})
	history = append(history, session.Message{
		Role:    session.RoleAssistant,
		Content: "最近回复",
		Mode:    session.ModeBaseline,
	})

	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"summary":"用户曾查 demo；诊断 run_keep 结论镜像拉取失败","run_ids":["run_keep"],"open_questions":[]}`)
	})

	// 极紧预算：逼出 L0/L1 后仍超，进入 L2
	view, err := prepareTowerContext(context.Background(), client, history, 120, 40, 20)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if view.CheckpointContent == "" {
		t.Fatal("expected CheckpointContent from L2")
	}
	if !strings.Contains(view.CheckpointContent, "[checkpoint]") {
		t.Fatalf("checkpoint marker: %q", view.CheckpointContent)
	}
	if !strings.Contains(view.CheckpointContent, "run_keep") && !strings.Contains(view.CheckpointContent, "镜像") {
		t.Fatalf("checkpoint should retain diagnosis: %q", view.CheckpointContent)
	}

	hasCP := false
	for _, m := range view.Hist {
		if m.Mode == session.ModeCheckpoint {
			hasCP = true
			break
		}
	}
	if !hasCP {
		t.Fatalf("hist missing checkpoint mode: %+v", view.Hist)
	}
}

// nil client 不得触发 L2，CheckpointContent 须为空
func TestPrepareTowerContextNoClient(t *testing.T) {
	history := make([]session.Message, 0, 20)
	for i := 0; i < 20; i++ {
		history = append(history, session.Message{
			Role:    session.RoleUser,
			Content: strings.Repeat("块", 80),
		})
	}
	view, err := prepareTowerContext(context.Background(), nil, history, 50, 30, 15)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if view.CheckpointContent != "" {
		t.Fatalf("nil client should not L2: %q", view.CheckpointContent)
	}
}

// checkpoint 消息不得进入 prior_diagnostics
func TestExtractPriorCheckpoint(t *testing.T) {
	history := []session.Message{
		{
			Role:    session.RoleAssistant,
			Mode:    session.ModeCheckpoint,
			Content: "[checkpoint] old",
		},
		{
			Role:    session.RoleAssistant,
			Mode:    session.ModeDiagnostic,
			RunID:   "run_1",
			Content: "结论",
		},
	}
	priors := extractPriorDiagnostics(history)
	if len(priors) != 1 || priors[0].RunID != "run_1" {
		t.Fatalf("priors: %+v", priors)
	}
}
