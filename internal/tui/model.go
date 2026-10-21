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

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
)

// 渲染视图的一条消息（用户 / 助手 / 错误 / 系统），是展示层视图、非业务事实（守 #20）
type msgView struct {
	kind string // "user" | "assistant" | "error" | "system"
	text string
}

// 当前回复的展示副本；使用字符串以适配框架按值复制模型，终态到达后清空
type streamingBuffer struct{ text string }

// 追加展示片段，不参与业务提交
func (s *streamingBuffer) append(chunk string) { s.text += chunk }

// 返回当前展示正文
func (s *streamingBuffer) view() string { return s.text }

// 轮次结束后清除临时正文
func (s *streamingBuffer) reset() { s.text = "" }

// 判断是否已有可见片段
func (s *streamingBuffer) empty() bool { return s.text == "" }

// Turn 完成消息：成功带 result，失败带 err
type turnMsg struct {
	result session.TurnResult
	err    error
}

// TUI 状态
type Model struct {
	svc       *session.Service
	sessionID string
	// 配置主题（dark | light | auto）；空同 auto。运行时 /theme 切换后就地更新
	tuiTheme string
	// 主题覆盖文件路径；运行时切换主题需重载样式，故要留着（启动时已校验）
	themeFile string
	// 会话发现能力（运行时命令 /sessions、/session 用）；可空，缺能力时命令报不可用
	lister session.SessionLister

	styles styles
	md     *markdownRenderer // markdown 渲染器；WindowSizeMsg 到达后按宽度建

	messages  []msgView
	input     textarea.Model
	viewport  viewport.Model
	spinner   spinner.Model
	streaming streamingBuffer

	// 命令面板（/ 菜单）：palette 为按当前输入推导的视图；paletteOff 为
	// Esc 抑制（菜单关闭，任意编辑后重开）
	palette    paletteView
	paletteOff bool

	busy   bool
	quit   bool
	width  int
	height int

	// 在途 Turn 的取消句柄；program 退出时由 Run 的 defer cancel 触发。
	// bubbletea Model 受框架约束需持有 ctx（Update 签名无法传参），此处偏离通用「不存 ctx」约定
	ctx context.Context
	// 当前轮次的取消句柄；取消后等待终态再允许下一轮
	cancelTurn context.CancelFunc
	// 当前轮次按序消费的展示事件
	events <-chan streamMsg
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
		syncViewport(&m)
		return m, nil

	case streamStartedMsg:
		m.events = msg.events
		return m, nextStream(m.events)

	case streamMsg:
		if msg.ack == nil {
			return m.Update(msg.final)
		}
		m.streaming.append(msg.delta)
		msg.ack <- nil
		syncViewport(&m)
		return m, nextStream(m.events)

	case turnMsg:
		if m.cancelTurn != nil {
			m.cancelTurn()
			m.cancelTurn = nil
		}
		m.streaming.reset()
		m.events = nil
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
		// 菜单激活时的专属键位：↑↓/Tab/Esc 不进 textarea（消费）；
		// busy 中仍可浏览导航，Enter 不执行由下方既有 busy 拦截承接（设计钉死）
		if m.updatePaletteKeys(msg) {
			return m, nil
		}
		switch msg.Type {
		case tea.KeyCtrlC:
			if m.busy && m.cancelTurn != nil {
				m.cancelTurn()
				return m, nil
			}
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
			// 菜单激活时回车：裸命令（无空白）替换为选中规范名再走既有命令路径；
			// 已在输参数（含空白）原样提交（设计钉死的 Enter 语义）
			if m.menuVisible() {
				if execLine, handled := paletteSubmit(m.input.Value(), m.palette); handled {
					text = strings.TrimSpace(execLine)
				}
			}
			// 运行时命令分支：命令不进 Turn、不产生 user 视图，错误同样只渲染不发送
			// （完成标志 1/2）；busy 已在上方拦截，与流式生成互斥不排队（零新状态机）
			if name, args, ok, perr := parseCommand(text); ok {
				var res commandResult
				if perr != nil {
					res = errResult(perr.Error())
				} else {
					res = runCommand(m.ctx, m.commandEnv(), name, args)
				}
				// 效果应用次序与 inline 一致：清屏 → 换主题 → 追加输出行
				if res.clearDisplay {
					// 只清视图历史：Store 全量保留，切回即恢复语义与 /session 切换同口径
					m.messages = nil
				}
				if res.theme != "" {
					res = m.applyTheme(res)
				}
				if res.quit {
					m.quit = true
					m.input.Reset()
					return m, tea.Quit
				}
				if res.switchTo != "" {
					m.sessionID = res.switchTo
					// 切换后清空视图历史：viewport 只承载本次交互的滚动记录，
					// 避免旧会话消息与新会话轮次混在同一视图（显示与活跃会话不符）。
					// 历史不重放——Store 全量保留，切回即恢复语义
					m.messages = nil
				}
				for _, ln := range res.lines {
					kind := "system"
					if ln.isError {
						kind = "error"
					}
					m.messages = append(m.messages, msgView{kind: kind, text: ln.text})
				}
				m.input.Reset()
				m.paletteOff = false
				m.refreshPalette()
				syncViewport(&m)
				return m, nil
			}
			m.messages = append(m.messages, msgView{kind: "user", text: text})
			m.input.Reset()
			m.paletteOff = false
			m.refreshPalette()
			m.busy = true
			syncViewport(&m)
			turnCtx, cancel := context.WithCancel(m.ctx)
			m.cancelTurn = cancel
			m.streaming.reset()
			// 启动命令执行后才产生事件，更新线程只消费并渲染
			svc, sid, parent := m.svc, m.sessionID, m.ctx
			turn := func() tea.Msg {
				stream := startStream(parent, turnCtx, svc, sid, text)
				// 转交实际事件通道，后续命令逐个读取
				return streamStartedMsg{events: stream}
			}
			return m, tea.Batch(turn, m.spinner.Tick)
		default:
			prev := m.input.Value()
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			// 输入值变化（任意编辑）：清除 Esc 抑制并重算菜单；纯光标移动不重开
			if m.input.Value() != prev {
				m.paletteOff = false
				m.refreshPalette()
			}
			return m, cmd
		}
	}
	// 其它消息（如 textarea 内部的 BlinkMsg）转发输入组件，维持光标闪烁等内部循环
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// 组装命令执行环境：闭包读取当前会话编号，/session、/new 切换后后续轮次即时生效；
// newSession 绑定产品服务既有路径，不另起后门
func (m Model) commandEnv() commandEnv {
	return commandEnv{
		lister:           m.lister,
		newSession:       m.svc.NewSession,
		currentSessionID: func() string { return m.sessionID },
		currentTheme:     func() string { return m.tuiTheme },
	}
}

// 应用主题切换效果：重载样式表并重建 markdown 渲染器；失败则保持原主题并把
// 「已切换」行换成错误行（误导性确认不能留）。已渲染的历史不重排：inline 无法
// 回溯旧输出，双模式语义保持一致
func (m *Model) applyTheme(res commandResult) commandResult {
	st, md, err := switchTheme(res.theme, m.themeFile, m.width)
	if err != nil {
		res.theme = ""
		res.lines = []renderLine{{text: "切换主题失败：" + err.Error(), isError: true}}
		return res
	}
	m.styles, m.tuiTheme = st, res.theme
	// spinner 在构造时把样式表拷进 Bubble 组件，不会自己跟主题走，须显式同步
	m.spinner.Style = st.spinner
	if m.width > 0 {
		// 宽度已知才重建：WindowSizeMsg 会按新 tuiTheme 再建一次
		m.md = md
	}
	return res
}

// menuVisible 报告菜单当前是否显示（有过滤结果且未被 Esc 抑制）
func (m *Model) menuVisible() bool {
	return m.palette.active() && !m.paletteOff
}

// refreshPalette 按输入当前值重算菜单视图（输入变化后调用）
func (m *Model) refreshPalette() {
	m.palette = filterPalette(m.input.Value())
}

// updatePaletteKeys 菜单激活时的专属键位：↑↓ 移动选中、Tab 把选中规范名补全进
// 输入框（需参命令带尾空格）、Esc 关闭菜单。消费的键不进 textarea；未激活返回
// false 走原路径。busy（生成中）可浏览导航，Enter 不执行由主分支既有 busy 拦截
// 承接（设计钉死）
func (m *Model) updatePaletteKeys(msg tea.Msg) bool {
	if !m.menuVisible() {
		return false
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return false
	}
	switch km.Type {
	case tea.KeyUp:
		m.palette = m.palette.moveSel(-1)
		return true
	case tea.KeyDown:
		m.palette = m.palette.moveSel(1)
		return true
	case tea.KeyTab:
		if line, _, ok := completePaletteLine(m.input.Value(), m.palette); ok {
			m.input.SetValue(line)
			m.input.CursorEnd()
			m.paletteOff = false
			m.refreshPalette()
		}
		return true
	case tea.KeyEscape:
		m.paletteOff = true
		return true
	}
	return false
}

func (m Model) View() string {
	if m.quit {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.viewport.View())
	if m.busy && m.streaming.empty() {
		b.WriteString("\n" + m.spinner.View() + " 思考中…")
	}
	w := m.width
	if w < 1 {
		w = 1
	}
	b.WriteString("\n" + m.styles.divider.Render(strings.Repeat("─", w)) + "\n")
	// 命令面板：菜单行在分割线与输入框之间，行正文与 /help 同形（#20：样式
	// 只经 menu / menu_selected 主题 token）
	if m.menuVisible() {
		for _, ln := range paletteRows(m.palette) {
			st := m.styles.menu
			if ln.selected {
				st = m.styles.menuSelected
			}
			b.WriteString(st.Render(ln.text) + "\n")
		}
	}
	b.WriteString(m.styles.prompt.Render("❯ ") + m.input.View())
	return b.String()
}

// 把 messages 渲染成历史文本，塞进 viewport 并滚到底
func syncViewport(m *Model) {
	history := renderHistory(m)
	if !m.streaming.empty() {
		var draft strings.Builder
		printGap(&draft, m.styles.spacing.assistantTop)
		if m.styles.labels.enabled {
			draft.WriteString(m.styles.assistant.Render(m.styles.labels.assistant) + "\n")
		}
		draft.WriteString(m.styles.assistant.Render(renderStreamingMarkdown(m.md, m.streaming.view())))
		history += draft.String()
	}
	m.viewport.SetContent(history)
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
func renderAssistant(r *markdownRenderer, res session.TurnResult) []msgView {
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
