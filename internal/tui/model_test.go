package tui

import (
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
