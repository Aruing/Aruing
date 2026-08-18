package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 检查点或折叠/截断标记存在即说明视图丢了旧文
func TestViewCompactedAwayDetail(t *testing.T) {
	if !viewCompactedAwayDetail(towerContextView{CheckpointContent: "[checkpoint] x"}) {
		t.Fatal("checkpoint should trigger")
	}
	if !viewCompactedAwayDetail(towerContextView{Hist: []towerHistMsg{{Content: "[folded] user | hi"}}}) {
		t.Fatal("folded marker should trigger")
	}
	if !viewCompactedAwayDetail(towerContextView{Hist: []towerHistMsg{{Content: "foo\n…[truncated, retained]"}}}) {
		t.Fatal("truncated marker should trigger")
	}
	if viewCompactedAwayDetail(towerContextView{Hist: []towerHistMsg{{Content: "完整原文"}}}) {
		t.Fatal("clean view must not trigger")
	}
}

// 运行编号锚定：客户端为空也命中，窗覆盖诊断与邻接用户句
func TestLocateRangeRunIDAnchor(t *testing.T) {
	history := []session.Message{
		{Role: session.RoleUser, Content: "查 demo-api"},
		{Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_abc123", Content: "根因：镜像拉取失败"},
		{Role: session.RoleUser, Content: "谢谢"},
	}
	lo, hi, ok := locateRange(context.Background(), nil, "run_abc123 之前那一步为什么", history)
	if !ok {
		t.Fatal("expected locate hit")
	}
	if lo != 0 || hi != 2 {
		t.Fatalf("range got [%d,%d] want [0,2]", lo, hi)
	}
}

// 关键词分支：无运行编号但有追问措辞与诊断标题重叠，客户端为空也命中
func TestLocateRangeKeywordBranch(t *testing.T) {
	history := []session.Message{
		{Role: session.RoleUser, Content: "查 redis"},
		{Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_x", Content: "根因：镜像拉取失败"},
	}
	// 含「镜像」与诊断重叠
	lo, hi, ok := locateRange(context.Background(), nil, "之前那一步为什么排除镜像问题", history)
	if !ok {
		t.Fatal("keyword branch should hit")
	}
	if lo != 0 || hi != 1 {
		t.Fatalf("range got [%d,%d] want [0,1]", lo, hi)
	}
}

// 规则未中且客户端为空 → 不命中（无大模型路径的优雅降级）
func TestLocateRangeRuleMissNoClient(t *testing.T) {
	history := []session.Message{
		{Role: session.RoleUser, Content: "查 redis"},
		{Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_x", Content: "根因：DNS"},
	}
	// 无运行编号、无追问措辞 → 规则不中
	if _, _, ok := locateRange(context.Background(), nil, "现在 redis 状态", history); ok {
		t.Fatal("current-state question should not locate without client")
	}
}

// 规则未中加大模型兜底命中；越界由调用方钳制
func TestLocateRangeLLMFallback(t *testing.T) {
	history := []session.Message{
		{Role: session.RoleUser, Content: "查网络"},
		{Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_a", Content: "根因：Service"},
		{Role: session.RoleUser, Content: "后来呢"},
		{Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_b", Content: "根因：DNS"},
	}
	t.Run("hit", func(t *testing.T) {
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeChatCompletion(w, `{"found":true,"lo":2,"hi":3}`)
		})
		// 无运行编号、无追问措辞 → 规则不中 → 大模型
		lo, hi, ok := locateRange(context.Background(), client, "那个网络的事", history)
		if !ok || lo != 2 || hi != 3 {
			t.Fatalf("llm fallback got [%d,%d] ok=%v", lo, hi, ok)
		}
	})
	t.Run("clamp out-of-range", func(t *testing.T) {
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeChatCompletion(w, `{"found":true,"lo":0,"hi":999}`)
		})
		_, hi, ok := locateRange(context.Background(), client, "那个事", history)
		if !ok || hi != len(history)-1 {
			t.Fatalf("clamp got hi=%d ok=%v", hi, ok)
		}
	})
	t.Run("found false", func(t *testing.T) {
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeChatCompletion(w, `{"found":false}`)
		})
		if _, _, ok := locateRange(context.Background(), client, "现在状态", history); ok {
			t.Fatal("found=false should not locate")
		}
	})
}

// 回灌取原文与序号；越界自动收敛
func TestRehydrateRange(t *testing.T) {
	history := []session.Message{
		{Role: session.RoleUser, Content: "u0"},
		{Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "r1", Content: "a1"},
		{Role: session.RoleUser, Content: "u2"},
	}
	win := rehydrateRange(history, 0, 1)
	if len(win) != 2 || win[0].Idx != 0 || win[1].Idx != 1 || win[1].RunID != "r1" {
		t.Fatalf("window: %+v", win)
	}
	all := rehydrateRange(history, -1, 99)
	if len(all) != 3 {
		t.Fatalf("clamp want 3 got %d", len(all))
	}
}

// 窗超预算 → 范围折叠截断到不超过预算，保留运行编号与序号，不静默丢段
func TestCompactRange(t *testing.T) {
	window := []rehydratedMsg{
		{Idx: 0, Role: session.RoleUser, Content: strings.Repeat("巨", 5000)},
		{Idx: 1, Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_keep", Content: "结论要点 KEEP_MARKER"},
	}
	out := compactRange(window, 100)
	if len(out) != 2 {
		t.Fatalf("len changed: %d (compact must not drop entries)", len(out))
	}
	if out[1].RunID != "run_keep" || out[1].Idx != 1 || !strings.Contains(out[1].Content, "KEEP_MARKER") {
		t.Fatalf("diagnostic must survive: %+v", out[1])
	}
	if !strings.Contains(out[0].Content, "truncated") && !strings.Contains(out[0].Content, "[folded]") {
		t.Fatalf("oversized entry should carry a compact marker: %q", out[0].Content)
	}
	// 超长条必须被显著缩小，不能原样塞回
	if estimateTokens(out[0].Content) >= estimateTokens(window[0].Content) {
		t.Fatalf("oversized entry was not reduced: %d tokens", estimateTokens(out[0].Content))
	}
}

// 组合：视图里诊断被折叠，回灌取回原文（非折叠骨架）
func TestRehydrateRestoresOriginalNotFolded(t *testing.T) {
	history := []session.Message{
		{Role: session.RoleUser, Content: "查 demo-api"},
		{Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_x", Content: "根因：ORIGINAL_MARKER_777 Service 没选 Pod"},
	}
	// 模拟压缩后的诊断消息只剩折叠骨架
	view := towerContextView{Hist: []towerHistMsg{{
		Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_x",
		Content: "[folded] assistant mode=diagnostic runId=run_x | 根因…",
	}}}
	if !viewCompactedAwayDetail(view) {
		t.Fatal("folded view should trigger rehydrate")
	}
	lo, hi, ok := locateRange(context.Background(), nil, "run_x 之前那一步为什么", history)
	if !ok {
		t.Fatal("locate should hit run_x")
	}
	win := compactRange(rehydrateRange(history, lo, hi), defaultRehydrateBudgetTokens)
	var diag *rehydratedMsg
	for i := range win {
		if win[i].RunID == "run_x" {
			diag = &win[i]
			break
		}
	}
	if diag == nil {
		t.Fatalf("diagnostic missing from window: %+v", win)
	} else if !strings.Contains(diag.Content, "ORIGINAL_MARKER_777") {
		t.Fatalf("rehydrate should restore original, got %q", diag.Content)
	} else if strings.HasPrefix(diag.Content, "[folded]") {
		t.Fatalf("rehydrate must not return the folded skeleton: %q", diag.Content)
	}
}

// 载荷：传回灌窗则含字段；空则省略
func TestTowerPayloadRehydrated(t *testing.T) {
	view := towerContextView{Hist: []towerHistMsg{{Role: session.RoleUser, Content: "hi"}}}
	with, err := buildTowerUserPayload(session.RespondInput{UserText: "q"}, view, nil, nil, nil, nil, []rehydratedMsg{
		{Idx: 1, Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_x", Content: "回灌原文 REHY_MARK"},
	})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if !strings.Contains(with, `"rehydrated_messages"`) || !strings.Contains(with, "REHY_MARK") {
		t.Fatalf("payload should include rehydrated_messages: %s", with)
	}
	without, err := buildTowerUserPayload(session.RespondInput{UserText: "q"}, view, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if strings.Contains(without, `"rehydrated_messages"`) {
		t.Fatalf("nil rehydrated must omit field: %s", without)
	}
}

// 应答接线：压缩触发（超长消息零级截断）加运行编号 → 注入回灌消息，回复不跑执行
func TestTowerRespondInjectsRehydrated(t *testing.T) {
	var userPayload string
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
				userPayload = m.Content
			}
		}
		writeChatCompletion(w, `{"action":"reply","content":"依据回灌原文作答","question":""}`)
	})
	exec := &fakeRunExecutor{}
	tower, err := NewTowerResponder(client, newTestFactory(t), exec, store.NewMemoryRunLedger(), nil, nil, nil)
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}

	// 超长用户消息迫使零级截断，视图丢细节，触发回灌门
	history := []session.Message{
		{Role: session.RoleUser, Content: strings.Repeat("巨", 100000)},
		{Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_loc1", Content: "根因：RESTORE_MARKER_555 Service 没选 Pod"},
	}
	out, err := tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_rehy",
		UserText:  "run_loc1 之前那一步为什么",
		History:   history,
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Mode != session.ModeBaseline {
		t.Fatalf("mode: %q want baseline", out.Mode)
	}
	if exec.lastRun.ID != "" {
		t.Fatal("executor must not run for explain reply")
	}
	if !strings.Contains(userPayload, `"rehydrated_messages"`) {
		t.Fatalf("payload missing rehydrated_messages: %s", userPayload)
	}
	if !strings.Contains(userPayload, "RESTORE_MARKER_555") {
		t.Fatalf("rehydrated window should restore diagnostic original: %s", userPayload)
	}
}
