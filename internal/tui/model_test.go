package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
)

var errBoom = errors.New("boom")

// 测试用最小 Model：只装交互所需的组件，svc 留空（单测不执行 Turn cmd）；主题 dark
func newTestModel() Model {
	ta := textarea.New()
	ta.Focus()
	m := Model{
		ctx:      context.Background(),
		input:    ta,
		viewport: viewport.New(80, 10),
		width:    80,
		height:   24,
		tuiTheme: "dark",
	}
	m.styles = mustLoadStyles("dark")
	return m
}

// turnMsg 成功：助手视图入历史，busy 清除（md 为 nil 时降级返回原文）
func TestUpdateTurnSuccess(t *testing.T) {
	m := newTestModel()
	m.busy = true
	updated, _ := m.Update(turnMsg{
		result: session.TurnResult{
			AssistantMessage: session.Message{Content: "hello", Mode: session.ModeBaseline},
		},
	})
	um := updated.(Model)
	if um.busy {
		t.Fatal("busy should clear after turn")
	}
	if len(um.messages) != 1 || um.messages[0].kind != "assistant" {
		t.Fatalf("messages = %+v", um.messages)
	}
	if !strings.Contains(um.messages[0].text, "hello") {
		t.Fatalf("assistant text = %q", um.messages[0].text)
	}
}

// turnMsg 失败：错误入历史、不退出、busy 清除（#3 容错）
func TestUpdateTurnFailure(t *testing.T) {
	m := newTestModel()
	m.busy = true
	updated, _ := m.Update(turnMsg{err: errBoom})
	um := updated.(Model)
	if um.busy {
		t.Fatal("busy should clear on error")
	}
	if um.quit {
		t.Fatal("must not quit on turn error")
	}
	if len(um.messages) != 1 || um.messages[0].kind != "error" {
		t.Fatalf("want one error message, got %+v", um.messages)
	}
}

// Enter 提交：busy 置 true、用户消息入历史、返回 Turn cmd
func TestUpdateEnterSubmits(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("hello")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(Model)
	if !um.busy {
		t.Fatal("busy should set on submit")
	}
	if len(um.messages) != 1 || um.messages[0].kind != "user" || um.messages[0].text != "hello" {
		t.Fatalf("messages = %+v", um.messages)
	}
	if cmd == nil {
		t.Fatal("submit should return Turn cmd")
	}
}

// busy 时 Enter 忽略（避免并发提交）
func TestUpdateEnterBusyIgnored(t *testing.T) {
	m := newTestModel()
	m.busy = true
	m.input.SetValue("hello")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(Model)
	if !um.busy {
		t.Fatal("busy should remain")
	}
	if len(um.messages) != 0 {
		t.Fatalf("busy enter should not add message, got %+v", um.messages)
	}
}

// Ctrl+C 退出
func TestUpdateCtrlCQuit(t *testing.T) {
	m := newTestModel()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	um := updated.(Model)
	if !um.quit {
		t.Fatal("Ctrl+C should set quit")
	}
	if cmd == nil {
		t.Fatal("Ctrl+C should return Quit cmd")
	}
}

// 诊断结果经 glamour 渲染为助手正文 + 报告两条视图
func TestRenderAssistantDiagnostic(t *testing.T) {
	r, err := newMarkdownRenderer("dark", 80)
	if err != nil {
		t.Fatalf("newMarkdownRenderer: %v", err)
	}
	views := renderAssistant(r, session.TurnResult{
		AssistantMessage: session.Message{Content: "结论", Mode: session.ModeDiagnostic},
		Report: &core.Report{
			Title:       "T",
			Summary:     "S",
			Conclusions: []core.Conclusion{{Result: "supported", Reason: "r"}},
		},
	})
	if len(views) != 2 {
		t.Fatalf("want 2 views (content + report), got %d", len(views))
	}
	if !strings.Contains(views[0].text, "结论") {
		t.Fatalf("first view = %q", views[0].text)
	}
	if !strings.Contains(views[1].text, "T") || !strings.Contains(views[1].text, "supported") {
		t.Fatalf("report view = %q", views[1].text)
	}
}

// 主题归一：dark/light 透传；空/auto/未知落到 termenv 检测（dark 或 light）
func TestResolveTheme(t *testing.T) {
	for _, in := range []string{"dark", "light"} {
		if got := resolveTheme(in); got != in {
			t.Fatalf("resolveTheme(%q) = %q", in, got)
		}
	}
	for _, in := range []string{"", "auto", "bogus"} {
		if got := resolveTheme(in); got != "dark" && got != "light" {
			t.Fatalf("resolveTheme(%q) = %q, want dark/light", in, got)
		}
	}
}

// dark 与 light 色表不同（可定制生效）
func TestLoadStylesDiffersByTheme(t *testing.T) {
	d := mustLoadStyles("dark")
	l := mustLoadStyles("light")
	if d.user.GetForeground() == l.user.GetForeground() {
		t.Fatal("dark/light user foreground should differ")
	}
}

// 历史渲染：默认无称呼前缀，内容整行经角色样式
func TestRenderHistoryNoLabels(t *testing.T) {
	m := newTestModel()
	m.messages = []msgView{
		{kind: "user", text: "hi"},
		{kind: "assistant", text: "hello"},
	}
	got := renderHistory(&m)
	if !strings.Contains(got, "hi") || !strings.Contains(got, "hello") {
		t.Fatalf("history = %q", got)
	}
	if strings.Contains(got, "你 ") || strings.Contains(got, "aruing ") {
		t.Fatalf("default history must not carry label prefixes: %q", got)
	}
}

// 称呼开启：称呼独立一行 + 换行 + 内容；连续 assistant 视图（正文 + 报告）同一块只一个称呼
func TestRenderHistoryLabels(t *testing.T) {
	m := newTestModel()
	st, err := loadStyles("dark", writeTheme(t, "labels:\n  enabled: true\n"))
	if err != nil {
		t.Fatalf("loadStyles: %v", err)
	}
	m.styles = st
	m.messages = []msgView{
		{kind: "user", text: "hi"},
		{kind: "assistant", text: "body"},
		{kind: "assistant", text: "report"},
	}
	got := stripANSI(renderHistory(&m))
	if !strings.Contains(got, "你\nhi") {
		t.Fatalf("user label line missing: %q", got)
	}
	// 助手块一个称呼 + 两条内容（块内不重复加称呼）
	if n := strings.Count(got, "aruing\n"); n != 1 {
		t.Fatalf("assistant label count = %d, want 1: %q", n, got)
	}
	if !strings.Contains(got, "body") || !strings.Contains(got, "report") {
		t.Fatalf("contents missing: %q", got)
	}
}

// 多个片段只进入临时视图，完成后缓冲清空且正文只显示一次
func TestUpdateStreaming(t *testing.T) {
	m := newTestModel()
	m.busy = true
	for _, delta := range []string{"hello", " world"} {
		ack := make(chan error, 1)
		updated, _ := m.Update(streamMsg{delta: delta, ack: ack})
		m = updated.(Model)
		if err := <-ack; err != nil {
			t.Fatal(err)
		}
	}
	if len(m.messages) != 0 || !strings.Contains(m.View(), "hello world") || strings.Contains(m.View(), "思考中") {
		t.Fatalf("stream view: %q, messages: %+v", m.View(), m.messages)
	}
	updated, _ := m.Update(streamMsg{final: turnMsg{result: session.TurnResult{
		AssistantMessage: session.Message{Content: "hello world", Mode: session.ModeBaseline},
	}}})
	m = updated.(Model)
	if !m.streaming.empty() || m.busy || strings.Count(m.View(), "hello world") != 1 {
		t.Fatalf("terminal view: %q", m.View())
	}
}

// 全屏视图也延后表格列宽计算，完成后由正式消息替换临时行
func TestUpdateStreamingTable(t *testing.T) {
	m := newTestModel()
	var err error
	m.md, err = newMarkdownRenderer("dark", 60)
	if err != nil {
		t.Fatal(err)
	}
	m.busy = true
	body := "| Name | Description |\n| --- | --- |\n| alpha | small |"
	ack := make(chan error, 1)
	updated, _ := m.Update(streamMsg{delta: body, ack: ack})
	m = updated.(Model)
	if err := <-ack; err != nil {
		t.Fatal(err)
	}
	live := stripANSI(m.View())
	if !strings.Contains(live, "alpha | small") || strings.Contains(live, "│") {
		t.Fatalf("live table: %q", live)
	}
	updated, _ = m.Update(streamMsg{final: turnMsg{result: session.TurnResult{
		AssistantMessage: session.Message{Content: body, Mode: session.ModeBaseline},
	}}})
	m = updated.(Model)
	if !m.streaming.empty() || !strings.Contains(stripANSI(m.View()), "│") {
		t.Fatalf("final table not formatted: %q", m.View())
	}
}

// 生成中取消不退出，终态前不允许下一轮；失败片段不变成正式助手视图
func TestUpdateStreamingCancel(t *testing.T) {
	m := newTestModel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m.busy, m.cancelTurn = true, cancel
	m.streaming.append("unfinished")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = updated.(Model)
	if ctx.Err() == nil || m.quit || !m.busy {
		t.Fatal("cancel must stop generation and await completion")
	}
	updated, _ = m.Update(turnMsg{err: context.Canceled})
	m = updated.(Model)
	if m.busy || !m.streaming.empty() || strings.Contains(m.View(), "unfinished") {
		t.Fatalf("failed stream retained: %q", m.View())
	}
	m.input.SetValue("retry")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	defer m.cancelTurn()
	if !m.busy || cmd == nil || m.quit {
		t.Fatal("next turn must remain available")
	}
}

// 命令面板：'/' 即菜单（含键位自述行）；↑↓ 改选中并钳制；Esc 关闭后编辑重开
func TestUpdatePaletteMenuAndKeys(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/")
	m.refreshPalette()
	view := stripANSI(m.View())
	if !strings.Contains(view, "/sessions") || !strings.Contains(view, "/exit") {
		t.Fatalf("menu missing commands: %q", view)
	}
	if !strings.Contains(view, paletteKeyHint) {
		t.Fatalf("menu missing key hint: %q", view)
	}

	m.input.SetValue("/s")
	m.refreshPalette()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if doc, ok := m.palette.selectedDoc(); !ok || doc.name != "session" {
		t.Fatalf("selected after down = %+v", doc)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp}) // 钳制：仍停在顶部
	m = updated.(Model)
	if doc, _ := m.palette.selectedDoc(); doc.name != "sessions" {
		t.Fatalf("selection should clamp at top, got %+v", doc)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(Model)
	if m.menuVisible() || strings.Contains(stripANSI(m.View()), paletteKeyHint) {
		t.Fatal("esc should hide menu")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = updated.(Model)
	if !m.menuVisible() {
		t.Fatal("edit should reopen menu")
	}
}

// Tab 补全：选中命令名写入输入框，需参命令带尾空格
func TestUpdatePaletteTabComplete(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/se")
	m.refreshPalette()
	m.palette = m.palette.moveSel(1) // → session
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if got := m.input.Value(); got != "/session " {
		t.Fatalf("tab complete = %q", got)
	}
}

// Enter 执行选中项：裸命令替换后走既有命令分支，输出入历史、不进 Turn、菜单随提交消失
func TestUpdatePaletteEnterExecutesSelected(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/hel")
	m.refreshPalette()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(Model)
	if um.busy || cmd != nil {
		t.Fatal("palette enter on command must not start a turn")
	}
	if len(um.messages) != 1 || um.messages[0].kind != "system" {
		t.Fatalf("command output missing: %+v", um.messages)
	}
	if !strings.Contains(um.messages[0].text, "/sessions") {
		t.Fatalf("help output = %q", um.messages[0].text)
	}
	if um.menuVisible() {
		t.Fatal("menu should close after command submit")
	}
}

// 裸 /session 回车：打开会话选择器而不是用法错误；缺会话发现能力时明确报错
func TestUpdatePaletteEnterBareSession(t *testing.T) {
	m := newTestModel()
	m.lister = fakeLister{summaries: []session.SessionSummary{
		{ID: "sess_a", MessageCount: 2, FirstQuestion: "查 redis"},
		{ID: "sess_b", MessageCount: 1, FirstQuestion: "为什么 demo-api 起不来"},
	}}
	m.sessionID = "sess_a"
	m.input.SetValue("/se")
	m.refreshPalette()
	m.palette = m.palette.moveSel(1)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(Model)
	if um.busy {
		t.Fatal("picker must not start turn")
	}
	if !um.pickerOn || len(um.picker.items) != 2 {
		t.Fatalf("picker = on %v items %d, want opened with 2 items", um.pickerOn, len(um.picker.items))
	}
	if len(um.messages) != 0 {
		t.Fatalf("picker opening must not print text lines: %+v", um.messages)
	}
	if !strings.Contains(um.View(), pickerKeyHint) {
		t.Fatalf("view must show picker key hint: %q", um.View())
	}

	m2 := newTestModel()
	m2.input.SetValue("/session")
	updated, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um2 := updated.(Model)
	if um2.pickerOn {
		t.Fatal("picker must not open without the session lister")
	}
	if len(um2.messages) != 1 || um2.messages[0].kind != "error" || !strings.Contains(um2.messages[0].text, "不可用") {
		t.Fatalf("messages = %+v", um2.messages)
	}
}

// 选择器导航与切换：↑↓ 改选中、Enter 切换会话（清视图历史并提示）
func TestPickerNavigateSwitch(t *testing.T) {
	m := newTestModel()
	m.lister = fakeLister{summaries: []session.SessionSummary{
		{ID: "sess_a", MessageCount: 2, FirstQuestion: "查 redis"},
		{ID: "sess_b", MessageCount: 1, FirstQuestion: "为什么 demo-api 起不来"},
	}}
	m.sessionID = "sess_a"
	m.messages = []msgView{{kind: "user", text: "旧会话内容"}}
	m.input.SetValue("/session")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	// ↓ 选中第二条（列表顺序沿用 lister：sess_a 在前）
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.pickerOn {
		t.Fatal("picker must close after switching")
	}
	if m.sessionID != "sess_b" {
		t.Fatalf("sessionID = %q, want sess_b", m.sessionID)
	}
	if len(m.messages) != 1 || m.messages[0].kind != "system" || !strings.Contains(m.messages[0].text, "已切换到会话 sess_b") {
		t.Fatalf("messages = %+v", m.messages)
	}
}

// 过滤：输入行作为过滤词收窄条目；无命中时保留空视图而不崩
func TestPickerFilter(t *testing.T) {
	m := newTestModel()
	m.lister = fakeLister{summaries: []session.SessionSummary{
		{ID: "sess_a", MessageCount: 2, FirstQuestion: "查 redis"},
		{ID: "sess_b", MessageCount: 1, FirstQuestion: "为什么 demo-api 起不来"},
	}}
	m.input.SetValue("/session")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	m.input.SetValue("")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("redis")})
	m = updated.(Model)
	if len(m.picker.items) != 1 || m.picker.items[0].id != "sess_a" {
		t.Fatalf("filtered items = %+v, want only sess_a", m.picker.items)
	}

	m.input.SetValue("")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("zzz")})
	m = updated.(Model)
	if len(m.picker.items) != 0 {
		t.Fatalf("no-match filter should yield empty view: %+v", m.picker.items)
	}
	if !strings.Contains(m.View(), pickerKeyHint) {
		t.Fatal("empty view still shows the picker hint row")
	}
}

// Esc 关闭选择器；生成中 Enter 不切换（busy 拦截同语义）
func TestPickerEscapeAndBusy(t *testing.T) {
	m := newTestModel()
	m.lister = fakeLister{summaries: []session.SessionSummary{{ID: "sess_b"}}}
	m.input.SetValue("/session")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(Model)
	if m.pickerOn {
		t.Fatal("Esc must close the picker")
	}

	m2 := newTestModel()
	m2.lister = fakeLister{summaries: []session.SessionSummary{{ID: "sess_b"}}}
	m2.input.SetValue("/session")
	updated, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 = updated.(Model)
	m2.busy = true
	updated, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 = updated.(Model)
	if !m2.pickerOn || m2.sessionID == "sess_b" {
		t.Fatalf("busy Enter must not switch: pickerOn=%v sessionID=%q", m2.pickerOn, m2.sessionID)
	}
}

// 已输参数时 Enter 原样提交（不替换为选中项）；busy 时 Enter 不执行
func TestUpdatePaletteEnterWithArgsAndBusy(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/session abc")
	m.refreshPalette()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(Model)
	// lister 未注入：走既有 switchSession 路径报不可用（证明未被替换成裸命令）
	if len(um.messages) != 1 || !strings.Contains(um.messages[0].text, "不可用") {
		t.Fatalf("messages = %+v", um.messages)
	}

	m2 := newTestModel()
	m2.busy = true
	m2.input.SetValue("/hel")
	m2.refreshPalette()
	updated, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um = updated.(Model)
	if len(um.messages) != 0 {
		t.Fatalf("busy enter must not execute: %+v", um.messages)
	}
}

// app 模式 /clear：只清视图历史，不退出、不进 Turn
func TestUpdateEnterClear(t *testing.T) {
	m := newTestModel()
	m.messages = []msgView{{kind: "user", text: "hi"}, {kind: "assistant", text: "yo"}}
	m.input.SetValue("/clear")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(Model)
	if cmd != nil || um.busy || um.quit {
		t.Fatalf("clear must stay in session: cmd=%v busy=%v quit=%v", cmd, um.busy, um.quit)
	}
	if len(um.messages) != 1 || um.messages[0].kind != "system" {
		t.Fatalf("messages = %+v (history cleared, confirmation kept)", um.messages)
	}
}

// app 模式 /theme：切换后样式与主题字段生效，历史不重排（双模式一致语义）
func TestUpdateEnterTheme(t *testing.T) {
	m := newTestModel()
	m.messages = []msgView{{kind: "user", text: "hi"}}
	m.input.SetValue("/theme light")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(Model)
	if um.tuiTheme != "light" {
		t.Fatalf("tuiTheme = %q, want light", um.tuiTheme)
	}
	if got, want := um.styles.prompt.Render("x"), mustLoadStyles("light").prompt.Render("x"); got != want {
		t.Fatalf("prompt style not switched: got %q want %q", got, want)
	}
	if len(um.messages) != 2 || um.messages[0].kind != "user" {
		t.Fatalf("history must be kept: %+v", um.messages)
	}
	// spinner 样式在构造时被拷贝进组件，切换后必须同步（否则下一轮 spinner 仍旧色）
	if got, want := um.spinner.Style.Render("x"), mustLoadStyles("light").spinner.Render("x"); got != want {
		t.Fatalf("spinner style not switched: got %q want %q", got, want)
	}
}

// app 模式 /theme 非法值：样式不变，只多一条错误行
func TestUpdateEnterThemeInvalid(t *testing.T) {
	m := newTestModel()
	m.input.SetValue("/theme blue")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(Model)
	if um.tuiTheme != "dark" {
		t.Fatalf("tuiTheme = %q, want unchanged dark", um.tuiTheme)
	}
	if len(um.messages) != 1 || um.messages[0].kind != "error" {
		t.Fatalf("messages = %+v", um.messages)
	}
}

// app 模式 /theme 切换失败（主题文件已坏）：保持原主题，确认行换成错误行
func TestUpdateEnterThemeLoadFailure(t *testing.T) {
	m := newTestModel()
	m.themeFile = writeTheme(t, "styles:\n  user:\n    foreground: \"#zzzzzz\"\n")
	m.input.SetValue("/theme light")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(Model)
	if um.tuiTheme != "dark" {
		t.Fatalf("tuiTheme = %q, want unchanged dark", um.tuiTheme)
	}
	if len(um.messages) != 1 || um.messages[0].kind != "error" || !strings.Contains(um.messages[0].text, "切换主题失败") {
		t.Fatalf("messages = %+v", um.messages)
	}
}
