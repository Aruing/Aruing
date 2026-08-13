package tui

import (
	"errors"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"aruing/internal/core"
	"aruing/internal/session"
)

var errBoom = errors.New("boom")

// 测试用最小 Model：只装交互所需的组件，svc 留空（单测不执行 Turn cmd）
func newTestModel() Model {
	ta := textarea.New()
	ta.Focus()
	return Model{
		input:    ta,
		viewport: viewport.New(80, 10),
		width:    80,
		height:   24,
	}
}

// turnMsg 成功：助手视图入历史，busy 清除
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
	if len(um.messages) != 1 || um.messages[0].kind != "assistant" || um.messages[0].text != "hello" {
		t.Fatalf("messages = %+v", um.messages)
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

// 诊断结果渲染为助手正文 + 报告两条视图
func TestRenderAssistantDiagnostic(t *testing.T) {
	views := renderAssistant(session.TurnResult{
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
	if views[0].text != "结论" {
		t.Fatalf("first view = %q", views[0].text)
	}
}
