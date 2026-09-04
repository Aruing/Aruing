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

// 词典机械抽取：DNS-1123 资源名进词典；flag 名、选择器赋值、镜像标签、
// kubectl 常用词与大写词（表头/状态）不进
func TestBuildEntityDict(t *testing.T) {
	records := []session.DiagnosticRecord{{
		RunID: "run_d1",
		Evidence: []core.Evidence{
			{
				ID:          "e_1",
				CommandView: "kubectl get pods -n demo --field-selector spec.nodeName=demo-node-1",
				Summary:     "demo-web-7d9f6c-xk2p9 Running 3 重启",
			},
			{
				ID:          "e_2",
				CommandView: "kubectl describe pod demo-web-7d9f6c-xk2p9 -n demo --tail=100",
				Summary:     "镜像 nginx:1.25 拉取失败",
			},
		},
	}}
	dict := buildEntityDict(records)
	want := []string{"demo", "demo-web-7d9f6c-xk2p9"}
	if len(dict) != len(want) {
		t.Fatalf("dict = %v, want %v", dict, want)
	}
	for i, w := range want {
		if dict[i] != w {
			t.Fatalf("dict[%d] = %q, want %q (full: %v)", i, dict[i], w, dict)
		}
	}
}

// λ₁ 锚点类硬保证（式 6/7）：问题提到的编号/资源名在依据单元地址中时必命中
func TestLocateByAddress(t *testing.T) {
	history := []session.Message{
		{Role: session.RoleUser, Content: "查一下 demo-web-7d9f6c-xk2p9 的日志"},
		{Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_diag1", Content: "根因：镜像拉取失败，假设 h_hyp1 成立，证据 e_ev100"},
		{Role: session.RoleUser, Content: "谢谢"},
		{Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_diag2", Content: "另一次诊断"},
	}
	records := []session.DiagnosticRecord{{
		RunID: "run_diag1",
		Evidence: []core.Evidence{
			{ID: "e_ev100", ToolName: "k8s", CommandView: "kubectl logs pod/demo-web-7d9f6c-xk2p9 -n demo", Summary: "pod demo-web-7d9f6c-xk2p9 CrashLoopBackOff"},
			{ID: "e_ev200", ToolName: "k8s", CommandView: "kubectl get events -n other", Summary: "无异常事件"},
		},
	}}
	dict := buildEntityDict(records)

	cases := []struct {
		name    string
		q       string
		wantIdx []int
		wantEv  []string
	}{
		{"run 编号命中消息（RunID 字段）", "run_diag1 之前那一步为什么", []int{1}, nil},
		{"h 编号命中消息（正文 ID 族）", "h_hyp1 当时怎么说的", []int{1}, nil},
		{"e 编号直接命中证据卡", "e_ev200 说了什么", nil, []string{"e_ev200"}},
		{"资源名命中消息与证据卡", "demo-web-7d9f6c-xk2p9 现在还崩吗", []int{0}, []string{"e_ev100"}},
		{"无锚点不命中", "现在集群状态怎么样", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgIdx, evIDs := locateByAddress(tc.q, history, records, dict)
			if len(msgIdx) != len(tc.wantIdx) || len(evIDs) != len(tc.wantEv) {
				t.Fatalf("locate = idx %v ev %v, want idx %v ev %v", msgIdx, evIDs, tc.wantIdx, tc.wantEv)
			}
			for i, w := range tc.wantIdx {
				if msgIdx[i] != w {
					t.Fatalf("msgIdx[%d] = %d, want %d", i, msgIdx[i], w)
				}
			}
			for i, w := range tc.wantEv {
				if evIDs[i] != w {
					t.Fatalf("evIDs[%d] = %q, want %q", i, evIDs[i], w)
				}
			}
		})
	}
}

// λ₁ 命中下标取原文：邻接扩一圈、并集升序去重、保留下标
func TestRehydrateIndices(t *testing.T) {
	history := make([]session.Message, 14)
	for i := range history {
		history[i] = session.Message{Role: session.RoleUser, Content: "m" + string(rune('a'+i))}
	}
	// 单锚点扩一圈；多锚点区间并集且重叠去重（[1..3] 与 [2..4] 合并）
	win := rehydrateIndices(history, []int{5})
	if len(win) != 3 || win[0].Idx != 4 || win[1].Idx != 5 || win[2].Idx != 6 {
		t.Fatalf("single anchor window: %+v", win)
	}
	win = rehydrateIndices(history, []int{2, 3})
	want := []int{1, 2, 3, 4}
	if len(win) != len(want) {
		t.Fatalf("merged window len = %d, want %d: %+v", len(win), len(want), win)
	}
	for i, w := range want {
		if win[i].Idx != w {
			t.Fatalf("merged window[%d].Idx = %d, want %d", i, win[i].Idx, w)
		}
	}
}

// 组合：视图压缩丢细节 + 编号锚定 → λ₁ 单步回灌原文（非折叠骨架）
func TestRehydrateLayeredRestoresOriginal(t *testing.T) {
	history := []session.Message{
		{Role: session.RoleUser, Content: "查 demo-api"},
		{Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_x", Content: "根因：ORIGINAL_MARKER_777 Service 没选 Pod"},
	}
	view := towerContextView{Hist: []towerHistMsg{{
		Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_x",
		Content: "[folded] assistant mode=diagnostic runId=run_x | 根因…",
	}}}
	if !viewCompactedAwayDetail(view) {
		t.Fatal("folded view should trigger rehydrate")
	}
	win, _ := rehydrateLayered(context.Background(), nil, "run_x 之前那一步为什么", history, nil, view)
	if len(win) == 0 {
		t.Fatal("λ₁ should hit run_x")
	}
	var diag *rehydratedMsg
	for i := range win {
		if win[i].RunID == "run_x" {
			diag = &win[i]
			break
		}
	}
	if diag == nil {
		t.Fatalf("diagnostic missing from window: %+v", win)
	}
	if !strings.Contains(diag.Content, "ORIGINAL_MARKER_777") {
		t.Fatalf("rehydrate should restore original, got %q", diag.Content)
	}
	if strings.HasPrefix(diag.Content, "[folded]") {
		t.Fatalf("rehydrate must not return the folded skeleton: %q", diag.Content)
	}
}

// λ₁ 消息命中但视图未压缩 → 不回灌（原文已在视图，省预算）；
// 证据命中例外：raw 从不在默认视图，必回灌
func TestRehydrateLayeredSkipsMessagesWhenViewClean(t *testing.T) {
	history := []session.Message{
		{Role: session.RoleUser, Content: "查 demo-api"},
		{Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_x", Content: "根因：Service 没选 Pod"},
	}
	records := []session.DiagnosticRecord{{
		RunID: "run_x",
		Evidence: []core.Evidence{{
			ID: "e_ev1", RunID: "run_x", ToolName: "k8s",
			CommandView: "kubectl get endpoints demo-api -n demo",
			Summary:     "无端点", Raw: json.RawMessage(`{"stdout":"<none>"}`),
		}},
	}}
	cleanView := towerContextView{Hist: []towerHistMsg{{Role: session.RoleUser, Content: "完整原文"}}}

	if win, _ := rehydrateLayered(context.Background(), nil, "run_x 那一步说了什么", history, records, cleanView); win != nil {
		t.Fatalf("clean view must skip message rehydrate: %+v", win)
	}
	win, _ := rehydrateLayered(context.Background(), nil, "e_ev1 当时的输出是什么", history, records, cleanView)
	if len(win) != 1 || win[0].Mode != rehydratedModeEvidence || win[0].RunID != "run_x" {
		t.Fatalf("evidence hit must rehydrate raw preview: %+v", win)
	}
	if !strings.Contains(win[0].Content, "<none>") {
		t.Fatalf("preview should contain raw body: %q", win[0].Content)
	}
}

// 证据 raw 回灌：编号命中合成条目（mode=evidence、下标 -1、带运行编号）；
// 超长 raw 截断后 C1 补附深层地址；编号不存在不合成
func TestRehydrateLayeredEvidencePreview(t *testing.T) {
	longRaw := `{"stdout":"` + strings.Repeat("y", 25_000) + ` 尾部锚点 run_deep999"}`
	records := []session.DiagnosticRecord{{
		RunID: "run_d",
		Evidence: []core.Evidence{{
			ID: "e_evdeep", RunID: "run_d", ToolName: "k8s",
			CommandView: "kubectl logs pod/p-1 -n demo", Summary: "长输出",
			Raw: json.RawMessage(longRaw),
		}},
	}}
	cleanView := towerContextView{}

	win, _ := rehydrateLayered(context.Background(), nil, "e_evdeep 的完整输出", nil, records, cleanView)
	if len(win) != 1 {
		t.Fatalf("want single evidence entry: %+v", win)
	}
	e := win[0]
	if e.Mode != rehydratedModeEvidence || e.Idx != -1 || e.RunID != "run_d" {
		t.Fatalf("entry shape: %+v", e)
	}
	if !strings.Contains(e.Content, "e_evdeep") {
		t.Fatalf("preview must keep evidence id: %q", e.Content)
	}
	// 截断丢掉的尾部编号由 C1 补附，跨轮仍可寻址
	if !strings.Contains(e.Content, "run_deep999") || !strings.Contains(e.Content, "[addr_refs]") {
		t.Fatalf("truncated preview must keep deep address via C1 footer: %q", tail(e.Content, 200))
	}

	if win, _ := rehydrateLayered(context.Background(), nil, "e_ghost999 在哪", nil, records, cleanView); win != nil {
		t.Fatalf("unknown evidence id must not synthesize entry: %+v", win)
	}
}

// λ₂ 兜底门：λ₁ 空 + 视图压缩丢细节 + 语义指涉时大模型定位一次；
// 三条件缺一不调；模型 found=false 时优雅降级为不注入
func TestRehydrateLayeredLLMFallback(t *testing.T) {
	history := []session.Message{
		{Role: session.RoleUser, Content: "查网络"},
		{Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: "run_a", Content: "根因：Service"},
	}
	records := []session.DiagnosticRecord{{
		RunID: "run_a",
		Evidence: []core.Evidence{{
			ID: "e_ev1", ToolName: "k8s", CommandView: "kubectl get svc -n demo", Summary: "选择器不符",
		}},
	}}
	compacted := towerContextView{CheckpointContent: "[checkpoint] handoff"}

	t.Run("gate pass and llm hit", func(t *testing.T) {
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeChatCompletion(w, `{"found":true,"lo":0,"hi":1}`)
		})
		win, _ := rehydrateLayered(context.Background(), client, "之前那一步为什么排除镜像问题", history, records, compacted)
		if len(win) != 2 || win[0].Idx != 0 || win[1].Idx != 1 {
			t.Fatalf("llm fallback window: %+v", win)
		}
	})
	t.Run("no semantic reference skips llm", func(t *testing.T) {
		called := false
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			called = true
			writeChatCompletion(w, `{"found":true,"lo":0,"hi":1}`)
		})
		if win, _ := rehydrateLayered(context.Background(), client, "现在集群状态", history, records, compacted); win != nil {
			t.Fatalf("no anchor no semantic must not rehydrate: %+v", win)
		}
		if called {
			t.Fatal("llm must not be called without semantic reference")
		}
	})
	t.Run("clean view skips llm", func(t *testing.T) {
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeChatCompletion(w, `{"found":true,"lo":0,"hi":1}`)
		})
		clean := towerContextView{Hist: []towerHistMsg{{Role: session.RoleUser, Content: "完整原文"}}}
		if win, _ := rehydrateLayered(context.Background(), client, "之前那一步为什么", history, records, clean); win != nil {
			t.Fatalf("clean view must not rehydrate: %+v", win)
		}
	})
	t.Run("llm found false degrades", func(t *testing.T) {
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			writeChatCompletion(w, `{"found":false}`)
		})
		if win, _ := rehydrateLayered(context.Background(), client, "之前那一步为什么", history, records, compacted); win != nil {
			t.Fatalf("found=false must not rehydrate: %+v", win)
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

// 应答接线（beta7 回归）：压缩触发（超长消息零级截断）加编号锚定 → λ₁ 注入回灌原文，回复不跑执行
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

	// 超长用户消息迫使零级截断，视图丢细节；编号锚定走 λ₁
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

// 白话走查（完成标志 6）：60 轮 3 诊断脚本长会话——
// 浅问题（结论级）索引卡常驻零回灌即答；深问题（证据细节级）λ₁ 一步回灌找回 raw
func TestTowerLayeredWalkthrough(t *testing.T) {
	const (
		walkSession = "sess_walk"
		walkRounds  = 60
		podName     = "web-7d9f6c-xk2p9"
		deepRaw     = `{"stdout":"RAW_DEEP_MARKER_300 line1\nline2"}`
	)
	diagRounds := map[int]string{8: "run_walk1", 28: "run_walk2", 50: "run_walk3"}

	history := make([]session.Message, 0, walkRounds*2)
	for r := 1; r <= walkRounds; r++ {
		history = append(history, session.Message{
			Role:    session.RoleUser,
			Content: "第 " + itoa(r) + " 轮：帮我看看集群，顺便聊点别的。",
		})
		if runID, ok := diagRounds[r]; ok {
			history = append(history, session.Message{
				Role: session.RoleAssistant, Mode: session.ModeDiagnostic, RunID: runID,
				Content: "诊断完成：" + filler(2400) + "结论详见报告。",
			})
			continue
		}
		history = append(history, session.Message{
			Role:    session.RoleAssistant,
			Content: "好的，从上下文看一切正常：" + filler(2400),
		})
	}

	records := []session.DiagnosticRecord{
		{
			RunID: "run_walk1", SessionID: walkSession, Question: "web 服务为什么不通",
			Report: core.Report{ID: "rep_w1", RunID: "run_walk1", Title: "镜像拉取失败",
				Summary: "根因是 pod " + podName + " 镜像拉取失败"},
			Evidence: []core.Evidence{{
				ID: "e_evw1", RunID: "run_walk1", ToolName: "k8s",
				CommandView: "kubectl get pods -n demo",
				Summary:     "pod " + podName + " CrashLoopBackOff 3 重启",
			}},
		},
		{
			RunID: "run_walk2", SessionID: walkSession, Question: "网络为什么抖动",
			Report: core.Report{ID: "rep_w2", RunID: "run_walk2", Title: "节点压力", Summary: "节点负载过高"},
			Evidence: []core.Evidence{{
				ID: "e_evw2", RunID: "run_walk2", ToolName: "k8s",
				CommandView: "kubectl top nodes -n demo", Summary: "节点 demo-node-1 高负载",
			}},
		},
		{
			RunID: "run_walk3", SessionID: walkSession, Question: "日志里报什么",
			Report: core.Report{ID: "rep_w3", RunID: "run_walk3", Title: "重启循环日志", Summary: "持续崩溃日志"},
			Evidence: []core.Evidence{{
				ID: "e_evw3", RunID: "run_walk3", ToolName: "k8s",
				CommandView: "kubectl logs pod/" + podName + " -n demo",
				Summary:     "崩溃堆栈", Raw: json.RawMessage(deepRaw),
			}},
		},
	}

	// 每轮重新装配：走查两类探针各一次完整 Respond
	// 返回决策载荷与是否发生深层压缩（证明长会话压缩前提成立）
	runProbe := func(t *testing.T, q string) (payload string, compacted bool) {
		t.Helper()
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
				// 深层压缩请求的 user 消息不含 user_text，只记录决策载荷
				if m.Role == "user" && strings.Contains(m.Content, `"user_text"`) {
					userPayload = m.Content
				}
				// 压缩请求载荷的顶层字段是 messages（决策载荷无此字段名）
				if m.Role == "user" && strings.Contains(m.Content, `{"messages":`) {
					compacted = true
				}
			}
			writeChatCompletion(w, `{"action":"reply","content":"作答","question":""}`)
		})
		ledger := store.NewMemoryRunLedger()
		for _, rec := range records {
			if err := ledger.Put(context.Background(), rec); err != nil {
				t.Fatalf("ledger put: %v", err)
			}
		}
		tower, err := NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, ledger, nil, nil, nil)
		if err != nil {
			t.Fatalf("new tower: %v", err)
		}
		if _, err := tower.Respond(context.Background(), session.RespondInput{
			SessionID: walkSession, UserText: q, History: history,
		}); err != nil {
			t.Fatalf("respond: %v", err)
		}
		if userPayload == "" {
			t.Fatal("decision payload not captured")
		}
		return userPayload, compacted
	}

	// 走查前提：填充量足以迫使 tier 视图走深层压缩（模拟长会话物理条件）
	if _, compacted := runProbe(t, "现在集群状态怎么样"); !compacted {
		t.Fatal("walkthrough session must force deep compaction")
	}

	t.Run("shallow probe answered by cards without rehydration", func(t *testing.T) {
		payload, _ := runProbe(t, "第一次诊断找到的 pod 叫什么名字？")
		if !strings.Contains(payload, `"prior_run_details"`) {
			t.Fatal("payload must carry R-layer cards")
		}
		if !strings.Contains(payload, podName) {
			t.Fatalf("cards must contain the pod name of the first diagnosis: %s", tail(payload, 400))
		}
		if strings.Contains(payload, `"rehydrated_messages"`) {
			t.Fatal("shallow probe must be answered with zero rehydration")
		}
	})

	t.Run("deep probe recovers evidence raw in one λ1 step", func(t *testing.T) {
		payload, _ := runProbe(t, "证据 e_evw3 当时的完整输出是什么？")
		if !strings.Contains(payload, `"rehydrated_messages"`) {
			t.Fatalf("deep probe must rehydrate: %s", tail(payload, 400))
		}
		if !strings.Contains(payload, "RAW_DEEP_MARKER_300") || !strings.Contains(payload, `"mode":"evidence"`) {
			t.Fatalf("rehydrated window must carry evidence raw preview: %s", tail(payload, 400))
		}
	})
}

// 长会话填充正文：迫使 tier 视图走深层压缩，不含地址与实体
func filler(n int) string { return strings.Repeat("日常会话填充。", n/6+1) }

// 小工具：取字符串尾部若干字符，便于失败信息定位
func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// 小工具：整数转字符串（避免测试文件引 strconv 只为一处）
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
