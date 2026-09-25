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
	"github.com/charmbracelet/glamour"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
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
	tuiTheme  string // 配置主题（dark | light | auto）；空同 auto

	styles styles
	md     *glamour.TermRenderer // markdown 渲染器；WindowSizeMsg 到达后按宽度建

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
		// 终端尺寸已知后按主题 + 宽度建 markdown 渲染器
		if r, err := newMarkdownRenderer(m.tuiTheme, msg.Width); err == nil {
			m.md = r
		}
		return m, nil

	case turnMsg:
		m.busy = false
		if msg.err != nil {
			m.messages = append(m.messages, msgView{kind: "error", text: msg.err.Error()})
		} else {
			m.messages = append(m.messages, renderAssistant(m.md, msg.result)...)
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
	// 其它消息（如 textarea 内部的 BlinkMsg）转发输入组件，维持光标闪烁等内部循环
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
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
		b.WriteString("\n" + m.styles.assistant.Render(m.streaming.view()))
	}
	w := m.width
	if w < 1 {
		w = 1
	}
	b.WriteString("\n" + m.styles.divider.Render(strings.Repeat("─", w)) + "\n")
	b.WriteString(m.styles.prompt.Render("❯ ") + m.input.View())
	return b.String()
}

// 把 messages 渲染成历史文本，塞进 viewport 并滚到底
func syncViewport(m *Model) {
	m.viewport.SetContent(renderHistory(m))
	m.viewport.GotoBottom()
}

// 渲染消息历史：块间距与称呼开关与 inline 模式同规则（user 上方空行、
// 助手块上下空行、称呼独立行）；连续 assistant/error 视为同一助手块，
// 空行与称呼只加在块首，块内（正文 + 报告）不隔开
func renderHistory(m *Model) string {
	var b strings.Builder
	sp, lab := m.styles.spacing, m.styles.labels
	for i, mv := range m.messages {
		next := ""
		if i+1 < len(m.messages) {
			next = m.messages[i+1].kind
		}
		switch mv.kind {
		case "user":
			printGap(&b, sp.userTop)
			if lab.enabled {
				b.WriteString(m.styles.user.Render(lab.user) + "\n")
			}
			b.WriteString(m.styles.user.Render(mv.text) + "\n")
		case "assistant", "error":
			// 块首：空行 + （assistant 开头时）称呼行；error 开头不加称呼（「错误 」是语义标记）
			if i == 0 || (m.messages[i-1].kind != "assistant" && m.messages[i-1].kind != "error") {
				printGap(&b, sp.assistantTop)
				if lab.enabled && mv.kind == "assistant" {
					b.WriteString(m.styles.assistant.Render(lab.assistant) + "\n")
				}
			}
			if mv.kind == "assistant" {
				b.WriteString(m.styles.assistant.Render(mv.text) + "\n")
			} else {
				b.WriteString(m.styles.err.Render("错误 ") + mv.text + "\n")
			}
			// 块尾：助手块收尾空行
			if next != "assistant" && next != "error" {
				printGap(&b, sp.assistantBottom)
			}
		case "system":
			b.WriteString(m.styles.system.Render(mv.text) + "\n")
		}
	}
	return b.String()
}

// 用 markdown 渲染器把一轮 Turn 结果渲染为消息视图（正文 + 诊断报告）
func renderAssistant(r *glamour.TermRenderer, res session.TurnResult) []msgView {
	var views []msgView
	if c := strings.TrimSpace(res.AssistantMessage.Content); c != "" {
		views = append(views, msgView{kind: "assistant", text: renderMarkdown(r, c)})
	}
	if res.Report != nil && res.AssistantMessage.Mode == session.ModeDiagnostic {
		views = append(views, msgView{kind: "assistant", text: renderMarkdown(r, reportMarkdown(res.Report))})
	}
	return views
}

// 把诊断报告组织成 markdown 文本，交 glamour 渲染
func reportMarkdown(rep *core.Report) string {
	var b strings.Builder
	if rep.Title != "" {
		b.WriteString("## " + rep.Title + "\n\n")
	}
	if rep.Summary != "" {
		b.WriteString(rep.Summary + "\n\n")
	}
	for _, c := range rep.Conclusions {
		fmt.Fprintf(&b, "- **[%s]** %s\n", c.Result, c.Reason)
	}
	return strings.TrimRight(b.String(), "\n")
}
