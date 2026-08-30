package agent

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/session"
)

// ID 族抽取按出现序去重；嵌入更长标识符内的编号不误抽（词边界）
func TestExtractIDAddrs(t *testing.T) {
	text := "诊断 run_a 依据 e_1 与 e_1；假设 h_2 否定（v_9），报告 rep_3，任务 t_4"
	got := strings.Join(extractIDAddrs(text), ",")
	want := "run_a,e_1,h_2,v_9,rep_3,t_4"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// xxx_run_a 中 run 前无词边界（下划线是词字符），不得抽取
	if got := extractIDAddrs("prefix_run_a and run_a"); strings.Join(got, ",") != "run_a" {
		t.Fatalf("embedded id must not match: %v", got)
	}
}

// 词典实体大小写不敏感整词命中（保留词典原拼写）；nil 词典只走 ID 族
func TestExtractAddrsEntities(t *testing.T) {
	text := "Pod demo-api-7f9d 在 demo namespace 反复重启，证据 e_1"
	got := extractAddrs(text, []string{"demo-api-7f9d", "DEMO", "absent"})
	want := "e_1,demo-api-7f9d,DEMO"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %q want %q", strings.Join(got, ","), want)
	}
	if got := extractAddrs(text, nil); strings.Join(got, ",") != "e_1" {
		t.Fatalf("nil entities should keep id family only: %v", got)
	}
}

// C1 出口校验：缺则补附、已覆盖不动、幂等
func TestEnsureAddrCoverage(t *testing.T) {
	original := "先查 e_101，再看 e_102 日志，结论关联 run_77"
	t.Run("missing appended", func(t *testing.T) {
		compressed := "看了两条证据，结论关联 run_77"
		out := ensureAddrCoverage(original, compressed, nil)
		if !strings.HasPrefix(out, compressed) {
			t.Fatalf("original compressed text must be kept: %q", out)
		}
		if !strings.Contains(out, "[addr_refs] e_101, e_102") {
			t.Fatalf("missing addrs not appended: %q", out)
		}
	})
	t.Run("covered untouched", func(t *testing.T) {
		compressed := "e_101 e_102 run_77 全在"
		if out := ensureAddrCoverage(original, compressed, nil); out != compressed {
			t.Fatalf("covered text must be unchanged: %q", out)
		}
	})
	t.Run("idempotent", func(t *testing.T) {
		once := ensureAddrCoverage(original, "看了两条证据", nil)
		if twice := ensureAddrCoverage(original, once, nil); twice != once {
			t.Fatalf("not idempotent:\nonce  %q\ntwice %q", once, twice)
		}
	})
	t.Run("empty compressed gets refs only", func(t *testing.T) {
		out := ensureAddrCoverage("依据 e_9", "", nil)
		if out != "[addr_refs] e_9" {
			t.Fatalf("empty compressed should be refs only: %q", out)
		}
	})
}

// 截断预览丢尾部编号时，footer 机械补回（L0 路径）
func TestTruncatePreviewAddrFooter(t *testing.T) {
	content := strings.Repeat("背景细节。", 100) + "关键证据 e_900 与 run_901"
	out := truncateContentPreview(content, 40)
	if !strings.Contains(out, "truncated") {
		t.Fatal("expected truncated marker")
	}
	if !strings.Contains(out, "[addr_refs] e_900, run_901") {
		t.Fatalf("tail addrs lost from preview: %q", out)
	}
}

// 折叠骨架保 runId 字段之外，正文 ID 族由 footer 补回（L1 路径）
func TestFoldLineAddrFooter(t *testing.T) {
	m := towerHistMsg{
		Role:    session.RoleAssistant,
		Mode:    session.ModeDiagnostic,
		RunID:   "run_5",
		Content: strings.Repeat("铺垫细节", 60) + " 结论依据 e_50 与 e_51，另见 h_7",
	}
	out := foldLine(m)
	if !strings.HasPrefix(out, "[folded]") || !strings.Contains(out, "runId=run_5") {
		t.Fatalf("skeleton must keep runId: %q", out)
	}
	// 铺垫超过 80 字符，编号全在骨架预览之外，只能靠 footer 保住
	for _, id := range []string{"e_50", "e_51", "h_7"} {
		if !strings.Contains(out, id) {
			t.Fatalf("fold lost %s: %q", id, out)
		}
	}
	// 空正文无地址，不产生空 footer
	if out := foldLine(towerHistMsg{Role: session.RoleUser, Content: ""}); strings.Contains(out, "[addr_refs]") {
		t.Fatalf("empty content must not produce footer: %q", out)
	}
}

// L2 摘要丢编号时，checkpoint 落库正文机械补全——不信任模型 run_ids 输出
func TestCompactL2AddrGuarantee(t *testing.T) {
	hist := make([]towerHistMsg, 0, 10)
	hist = append(hist, towerHistMsg{Role: session.RoleUser, Content: strings.Repeat("旧轮内容", 30)})
	// 诊断消息落在 oldSeg（keepN=6，split=4）
	hist = append(hist, towerHistMsg{
		Role:    session.RoleAssistant,
		Mode:    session.ModeDiagnostic,
		RunID:   "run_old1",
		Content: "结论：镜像问题，关键证据 e_801",
	})
	for i := 0; i < 6; i++ {
		hist = append(hist, towerHistMsg{Role: session.RoleUser, Content: strings.Repeat("中段闲聊", 20)})
	}
	hist = append(hist, towerHistMsg{Role: session.RoleUser, Content: "最近一句"})

	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"summary":"用户排查过镜像问题","run_ids":[],"open_questions":[]}`)
	})
	view, err := compactL2(context.Background(), client, hist, 8_000)
	if err != nil {
		t.Fatalf("compactL2: %v", err)
	}
	// 模型摘要两个编号都没带，落库与注入正文都须机械含全（run_old1 来自 RunID 字段）
	for _, want := range []string{"run_old1", "e_801"} {
		if !strings.Contains(view.CheckpointContent, want) {
			t.Fatalf("checkpoint body lost %s: %q", want, view.CheckpointContent)
		}
	}
	var injected string
	for _, m := range view.Hist {
		if m.Mode == session.ModeCheckpoint {
			injected = m.Content
			break
		}
	}
	if injected == "" {
		t.Fatal("checkpoint missing from injected hist")
	}
	if !strings.Contains(injected, "e_801") {
		t.Fatalf("injected checkpoint lost e_801: %q", injected)
	}
}

// 回灌窗压缩复用 L0/L1，自动获得 C1：折叠后地址仍在（beta7 路径）
func TestCompactRangeAddrFooter(t *testing.T) {
	window := []rehydratedMsg{
		{Idx: 0, Role: session.RoleUser, Content: strings.Repeat("巨", 5000) + " 尾部编号 e_700"},
		{Idx: 1, Role: session.RoleAssistant, Mode: session.ModeBaseline, Content: "短回复"},
	}
	out := compactRange(window, 100)
	if !strings.HasPrefix(out[0].Content, "[folded]") && !strings.Contains(out[0].Content, "truncated") {
		t.Fatalf("expected compact marker: %q", out[0].Content)
	}
	if !strings.Contains(out[0].Content, "[addr_refs] e_700") {
		t.Fatalf("rehydrate window lost addr after compact: %q", out[0].Content)
	}
}
