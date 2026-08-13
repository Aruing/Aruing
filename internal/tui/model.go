// chat 的 bubbletea 终端交互：Elm 架构（Model/Update/View）。
// 纯展示层——所有事实来自 svc.Turn，messages 是渲染视图非业务事实（守 #20）。
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"aruing/internal/core"
	"aruing/internal/session"
)

// 渲染视图的一条消息（用户 / 助手 / 错误 / 系统），是展示层视图、非业务事实（守 #20）
type msgView struct {
	kind string // "user" | "assistant" | "error" | "system"
	text string
}

// 为流式响应预留（arc《流式响应》）：累积当前正在生成的 chunk，完成后落为正式消息并清空。
// 当前非流式恒空；流式落地时 chunk 经 append 灌入、View 检查 empty() 渲染、完成 reset()
type streamingBuffer struct{ b strings.Builder }

func (s *streamingBuffer) append(chunk string) { s.b.WriteString(chunk) } //nolint:unused // 为 arc《流式响应》预留；Step 2 非流式不调用
func (s *streamingBuffer) view() string        { return s.b.String() }
func (s *streamingBuffer) reset()              { s.b.Reset() } //nolint:unused // 为 arc《流式响应》预留；Turn 完成时清空
func (s *streamingBuffer) empty() bool         { return s.b.Len() == 0 }

// Turn 完成消息：成功带 result，失败带 err
type turnMsg struct {
	result session.TurnResult
	err    error
}

// TUI 状态
type Model struct {
	svc       *session.Service
	sessionID string
	format    string

	messages  []msgView
	input     textarea.Model
	viewport  viewport.Model
	spinner   spinner.Model
	streaming streamingBuffer

	busy   bool
	quit   bool
	width  int
	height int

	// 在途 Turn 的取消句柄；program 退出时由 Run 的 defer cancel 触发。
	// bubbletea Model 受框架约束需持有 ctx（Update 签名无法传参），此处偏离通用「不存 ctx」约定
	ctx context.Context
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spinner.Tick)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		vph := msg.Height - inputHeight - 2 // 历史 + 分隔 + 输入
		if vph < 1 {
			vph = 1
		}
		m.viewport.Width = msg.Width
		m.viewport.Height = vph
		m.input.SetWidth(msg.Width)
		return m, nil

	case turnMsg:
		m.busy = false
		if msg.err != nil {
			m.messages = append(m.messages, msgView{kind: "error", text: msg.err.Error()})
		} else {
			m.messages = append(m.messages, renderAssistant(msg.result)...)
		}
		syncViewport(&m)
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.busy {
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.quit = true
			return m, tea.Quit
		case tea.KeyEnter:
			if m.busy {
				return m, nil
			}
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			if text == "exit" || text == "quit" {
				m.quit = true
				return m, tea.Quit
			}
			m.messages = append(m.messages, msgView{kind: "user", text: text})
			m.input.Reset()
			m.busy = true
			syncViewport(&m)
			svc := m.svc
			sid := m.sessionID
			ctx := m.ctx
			turn := func() tea.Msg {
				result, err := svc.Turn(ctx, sid, text)
				return turnMsg{result: result, err: err}
			}
			// 提交时重启 spinner tick：idle 时 tick 已停，busy 期间需持续动画
			return m, tea.Batch(turn, m.spinner.Tick)
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.quit {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.viewport.View())
	if m.busy {
		b.WriteString("\n" + m.spinner.View() + " 思考中…")
	}
	if !m.streaming.empty() {
		b.WriteString("\n" + assistantStyle.Render(m.streaming.view()))
	}
	w := m.width
	if w < 1 {
		w = 1
	}
	b.WriteString("\n" + dividerStyle.Render(strings.Repeat("─", w)) + "\n")
	b.WriteString(promptStyle.Render("❯ ") + m.input.View())
	return b.String()
}

// 把 messages 渲染成文本塞进 viewport 并滚到底
func syncViewport(m *Model) {
	var b strings.Builder
	for _, mv := range m.messages {
		switch mv.kind {
		case "user":
			b.WriteString(userStyle.Render("你 ") + mv.text + "\n")
		case "assistant":
			b.WriteString(assistantStyle.Render("aruing ") + mv.text + "\n")
		case "error":
			b.WriteString(errorStyle.Render("错误 ") + mv.text + "\n")
		case "system":
			b.WriteString(systemStyle.Render(mv.text) + "\n")
		}
	}
	m.viewport.SetContent(b.String())
	m.viewport.GotoBottom()
}

// 按 Mode 把一轮 Turn 结果渲染为消息视图（纯文本；Step 3 用 glamour 美化）
func renderAssistant(r session.TurnResult) []msgView {
	var views []msgView
	if c := strings.TrimSpace(r.AssistantMessage.Content); c != "" {
		views = append(views, msgView{kind: "assistant", text: c})
	}
	if r.Report != nil && r.AssistantMessage.Mode == session.ModeDiagnostic {
		views = append(views, msgView{kind: "assistant", text: renderReportText(r.Report)})
	}
	return views
}

// 诊断报告纯文本投影（Step 3 改 glamour Markdown）
func renderReportText(rep *core.Report) string {
	var b strings.Builder
	if rep.Title != "" {
		b.WriteString("## " + rep.Title + "\n")
	}
	if rep.Summary != "" {
		b.WriteString(rep.Summary + "\n")
	}
	for _, c := range rep.Conclusions {
		fmt.Fprintf(&b, "- [%s] %s\n", c.Result, c.Reason)
	}
	return strings.TrimRight(b.String(), "\n")
}
