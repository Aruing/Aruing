// 行内 TUI：readline 库读输入（输入行提交后天然留痕）+ 助手消息 print 往下滚 + spinner + 容错。
// 与 app（bubbletea 全屏）模式并列；默认走此模式（Step 3 加 config.TUI.Mode 选）。
// 纯展示层——事实来自 svc.Turn，不持有业务事实（守 #20）。
// 多行输入：shift+enter / option+enter（终端支持时，经序列翻译成带标记的回车）
// 或行尾 \
// + enter 手动续行兑底；readline 库本身不解析这些序列。
package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/ergochat/readline"
	"golang.org/x/term"

	"github.com/Aruing/Aruing/internal/session"
)

// spinner 帧间隔与帧表
const spinnerInterval = 100 * time.Millisecond

// xterm modifyOtherKeys 开关：启用后支持的终端（iTerm2/WezTerm/foot/VSCode 等）
// 对 shift+enter 等修饰键上报独立序列，应用才能与普通 enter 区分
const (
	modifyOtherKeysOn  = "\x1b[>4;1m"
	modifyOtherKeysOff = "\x1b[>4m"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// 行内模式入口：readline 循环 + Turn 等待（spinner）+ 助手留痕 + 容错
func inlineRun(ctx context.Context, svc *session.Service, sessionID, format, tuiTheme, themeFile string, lister session.SessionLister, out io.Writer, prog *TurnProgress) error {
	st, err := loadStyles(tuiTheme, themeFile)
	if err != nil {
		return err
	}
	// 进度协调器接管终端：此后编排/Tower 的进度行与 spinner 同屏重绘
	prog.bind(out, st)
	// 行内引擎逐键读取依赖 raw mode：非 tty（管道 / 脚本）明确报错不降级；
	// 无人值守场景应用单句模式（chat "问题"，一次 Turn 后退出）
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("inline chat requires a terminal (for non-interactive use: aruing chat \"question\")")
	}
	// 启用 modifyOtherKeys：让支持的终端对 shift+enter 上报独立序列（不支持的终端忽略此序列）
	fmt.Fprint(out, modifyOtherKeysOn)
	defer fmt.Fprint(out, modifyOtherKeysOff)

	// 命令面板会话状态：包装层（菜单键翻译，ioloop goroutine）与 Listener
	// （重算与绘制，主 goroutine）共用；绘制只经 Listener 与提交/退出路径
	ctl := &paletteCtl{}
	keys := &newlineKeyReader{r: os.Stdin, ctl: ctl}

	// 终端宽度、主题与 markdown 渲染器的可写状态：每轮提交时重取宽度（ioctl
	// 开销可忽略），宽度或主题变了才重建 renderer（重建需解析 style 配置，
	// 毫秒级，仅在变化时付）。这样会话中途调整终端宽 / 主题后，markdown
	// 换行宽、divider 宽与配色都能自适应。
	// 先于 readline 构造：Listener 持有它的指针，/theme 切换后菜单立刻用新色表
	rstate := inlineRenderState{st: st, theme: tuiTheme, width: terminalWidth()}
	rstate.md = buildRenderer(rstate.theme, rstate.width)

	rl, err := readline.NewFromConfig(&readline.Config{
		Prompt:   rstate.st.prompt.Render("❯ "),
		Stdin:    keys,
		Stdout:   out,
		Stderr:   os.Stderr,
		Listener: paletteListener(ctl, out, &rstate),
	})
	if err != nil {
		return fmt.Errorf("init readline: %w", err)
	}
	defer rl.Close()

	// 命令执行环境：闭包引用 sessionID 变量，/session、/new 切换后即时生效；
	// lister 缺能力时会话类命令报不可用（步骤设计决策 7）
	cmdEnv := commandEnv{
		lister:           lister,
		newSession:       svc.NewSession,
		currentSessionID: func() string { return sessionID },
		currentTheme:     func() string { return rstate.theme },
	}

	// 一次性交互提示（系统样式，留痕）
	fmt.Fprintln(out, st.system.Render("多行输入：shift+enter / option+enter（终端支持时）/ 行尾 \\ + 回车；exit / Ctrl+D 退出"))

	for {
		if ctx.Err() != nil {
			return nil
		}
		text, err := readMultiline(rl, keys)
		if err != nil {
			// io.EOF（Ctrl+D）或 ErrInterrupt（Ctrl+C）：正常退出；先清菜单残迹
			ctl.clearTail(out)
			fmt.Fprintln(out)
			return nil
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if text == "exit" || text == "quit" {
			return nil
		}
		// 提交前菜单收尾：可见时裸命令片段替换为选中规范名（含参原样，多行
		// 草稿末行非 / 开头视图不可见不替换）；随后擦净菜单再渲染——Enter 后
		// 光标停在输入末行下一行（恰为菜单首行位置），clearBelow 从当前行清起，
		// 无半截消息、无菜单残骸进渲染流（与流式共存，完成标志 3）
		text = submitWithPalette(ctl, text)
		ctl.clearBelow(out)
		// 每轮重取终端宽：变了则重建 renderer，后续 divider 与 markdown 都用新宽
		if w := terminalWidth(); w != rstate.width {
			rstate.width = w
			rstate.md = buildRenderer(rstate.theme, rstate.width)
		}
		// 运行时命令分支：命中命令语法只解析执行与渲染，不 echo 用户行、不进 Turn；
		// 未知命令报错同样只渲染不发送（完成标志 1），命令轮零业务写入（完成标志 2）。
		// 效果应用次序固定：清屏 → 换主题 → 渲染输出行 —— 渲染必须排在两者之后，
		// 否则确认行会被随后的擦屏抹掉、换成新主题的确认行也用旧色表
		if res, handled := execInlineCommand(ctx, cmdEnv, text); handled {
			var themed bool
			res, themed = rstate.applyCommandEffects(res, out, ctl, themeFile)
			if themed {
				// 样式换了：所有启动时定格过色表的消费点都要跟新——进度协调器
				// （spinner 与进度行）与 readline 提示符（它把 prompt 字符串存下来）
				prog.bind(out, rstate.st)
				rl.SetPrompt(rstate.st.prompt.Render("❯ "))
			}
			renderCommandLines(out, rstate.st, res.lines)
			if res.quit {
				return nil
			}
			if res.switchTo != "" {
				sessionID = res.switchTo
			}
			// 清屏即新起点：不在空屏上先画一条分割线
			if !res.clearDisplay {
				renderMessageDivider(out, rstate.st, rstate.width)
			}
			continue
		}
		// 提交后把 readline 裸回显的输入行替换为主题渲染的留痕（user 样式项与
		// 称呼开关在此消费，守 #20：与 app 模式 viewport 历史同规则）
		rstate.echoUser(out, text)
		waitTurn(ctx, out, rstate.st, rstate.md, svc, sessionID, text, prog)
		// 轮间分割：一轮（用户输入 + 助手输出/错误）结束后与下一轮之间加分割线
		renderMessageDivider(out, rstate.st, rstate.width)
	}
}

// inline 模式的命令执行：命中命令语法时只解析与执行，返回效果供主循环应用
// （渲染与效果次序归主循环：清屏 / 换主题必须早于输出行）。普通输入
// handled=false 交回主循环走 Turn。命令与错误都不发送、不写 Store、不进 Turn
// （#20，完成标志 1/2）
func execInlineCommand(ctx context.Context, env commandEnv, text string) (commandResult, bool) {
	name, args, ok, err := parseCommand(text)
	if !ok {
		return commandResult{}, false
	}
	if err != nil {
		// 解析错误（未知命令 / 参数非法）与执行错误同一路径：错误样式留痕
		return errResult(err.Error()), true
	}
	return runCommand(ctx, env, name, args), true
}

// inlineRenderState inline 循环里会被运行时命令改写的渲染状态：样式表、
// markdown 渲染器、生效主题与终端宽。集中在一处，避免换主题后各处局部变量
// 出现新旧不一致（风格 token、渲染宽、主题查询三者必须同源）
type inlineRenderState struct {
	// 当前生效样式表；/theme 成功后整体替换
	st styles
	// markdown 渲染器；随生效主题与终端宽重建
	md *markdownRenderer
	// 当前生效主题名（dark / light / auto 或配置原值）
	theme string
	// 终端显示宽；每轮提交时重取，宽变则重建渲染器与分割线
	width int
}

// applyCommandEffects 按固定次序应用命令效果：先清屏（若有）再换主题，返回
// 调整后的结果与「样式是否已更换」（调用方据此重绑进度协调器的色表）。
// 次序不能颠倒：主题切换失败时要保留原样式，而清屏与主题无关。
// 主题加载失败时把命令自带的「已切换」行换成错误行——切换没成时的确认行是误导
func (s *inlineRenderState) applyCommandEffects(res commandResult, out io.Writer, ctl *paletteCtl, themeFile string) (commandResult, bool) {
	if res.clearDisplay {
		ctl.clearAll(out)
	}
	if res.theme == "" {
		return res, false
	}
	newSt, newMd, err := switchTheme(res.theme, themeFile, s.width)
	if err != nil {
		res.theme = ""
		res.lines = []renderLine{{text: "切换主题失败：" + err.Error(), isError: true}}
		return res, false
	}
	s.st, s.md, s.theme = newSt, newMd, res.theme
	return res, true
}

// echoUser 用当前样式表重印用户消息留痕。主题切换后必须走这里：直接拿启动时
// 的样式快照会让 /theme 之后的第一条用户消息仍是旧色（启动快照族）
func (s *inlineRenderState) echoUser(out io.Writer, text string) {
	echoUserMessage(out, s.st, text)
}

// renderCommandLines 渲染命令输出：错误行走错误样式，其余走系统样式
func renderCommandLines(out io.Writer, st styles, lines []renderLine) {
	for _, ln := range lines {
		if ln.isError {
			fmt.Fprintln(out, st.err.Render("错误 "+ln.text))
		} else {
			fmt.Fprintln(out, st.system.Render(ln.text))
		}
	}
}

// 提交后重印用户消息留痕：清除 readline 已回显的原始输入行（可能多行、长行在终端折行），
// 用 user 样式项逐行渲染内容；称呼开关开启时先单独一行渲染称呼。
// 回车后光标停在回显区下一行：须上移完整回显行数（echoRows）回到首行再逐行清印，
// 少移一行会把留痕打在原始回显下方、造成每轮输入重复显示。
// 行数按回显真实占用估算：每逻辑行 ceil(显示宽/终端宽) 折行，「❯ 」前缀约 2 列，CJK 按 2 列。
func echoUserMessage(out io.Writer, st styles, text string) {
	width := terminalWidth()
	echoRows := 0
	logical := strings.Split(text, "\n")
	for _, line := range logical {
		// 「❯ 」前缀约 2 列；保守按显示宽度折行计数（CJK 粗略按 2 列估，宁可多清一行也不残留）
		cols := 2 + displayCols(line)
		echoRows += rowsFor(cols, width)
	}
	// 回车后光标在回显区下一行行首：上移完整回显行数覆盖首行（差一行会留下原始回显致重复）
	if echoRows > 0 {
		fmt.Fprintf(out, "\x1b[%dA", echoRows)
	}
	// 输入上方空行读 spacing.userTop（主题可配，显式 0 合法）；覆盖旧回显行须逐行清印
	for i := 0; i < st.spacing.userTop; i++ {
		clearLine(out)
		fmt.Fprintln(out)
	}
	if st.labels.enabled {
		clearLine(out)
		fmt.Fprint(out, st.user.Render(st.labels.user), "\n")
	}
	for _, line := range logical {
		clearLine(out)
		fmt.Fprint(out, st.user.Render(line), "\n")
	}
}

// 折行行数：列数除以终端宽向上取整，至少 1
func rowsFor(cols, width int) int {
	if width <= 0 {
		width = 80
	}
	rows := (cols + width - 1) / width
	if rows < 1 {
		rows = 1
	}
	return rows
}

// 粗估显示列数：CJK 与全角按 2 列，其余 1 列（用于折行行数估算，宁多勿少）
func displayCols(s string) int {
	cols := 0
	for _, r := range s {
		if r > 0x2E80 { // CJK 区块及以右粗略按宽字符
			cols += 2
		} else {
			cols++
		}
	}
	return cols
}

// 读多行输入：shift/option+enter（软换行，由 keys 标记）直接换行拼接下一行；
// 行尾单个反斜杠 + 回车是终端不支持软换行序列时的手动续行兑底
func readMultiline(rl *readline.Instance, keys *newlineKeyReader) (string, error) {
	var b strings.Builder
	for {
		line, err := rl.ReadLine()
		if err != nil {
			return "", err
		}
		// 本行的回车若来自软换行翻译：直接拼接换行继续读，不连 \ 也不提交
		if keys.consumeSoft() {
			b.WriteString(line)
			b.WriteString("\n")
			continue
		}
		content, more := continuation(line)
		b.WriteString(content)
		if !more {
			return b.String(), nil
		}
	}
}

// 判断一行是否以单个反斜杠续行：返回写入缓冲的内容与是否继续读
// 双反斜杠 \\ 结尾表示字面反斜杠（不续行）
func continuation(line string) (content string, more bool) {
	trimmed := strings.TrimRight(line, " \t")
	if strings.HasSuffix(trimmed, "\\\\") {
		// 字面反斜杠：去掉转义保留一个 \，行完成
		return strings.TrimSuffix(trimmed, "\\") + "\n", false
	}
	if strings.HasSuffix(trimmed, "\\") {
		// 单反斜杠：去掉续行符换行，继续读
		return strings.TrimSuffix(trimmed, "\\") + "\n", true
	}
	return line + "\n", false
}

// 等待并展示一轮流式应答；增量写入失败反馈给会话层，禁止提交未成功消费的回复
// 读取输入结束后终端已恢复正常模式，生成期间的中断信号只取消本轮
// 已显示片段失败时明确标记为未完成；完整诊断与澄清继续走原有渲染路径
func waitTurn(ctx context.Context, out io.Writer, st styles, md *markdownRenderer, svc *session.Service, sessionID, text string, prog *TurnProgress) {
	turnCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()
	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()
	printGap(out, st.spacing.assistantTop)
	if st.labels.enabled {
		fmt.Fprint(out, st.assistant.Render(st.labels.assistant), "\n")
	}
	prog.spinnerStart(st)
	events := startStream(ctx, turnCtx, svc, sessionID, text)
	hasDelta := false
	_, height, _ := term.GetSize(int(os.Stdout.Fd()))
	if height < 2 {
		height = 24
	}
	view := inlineStreamView{width: terminalWidth(), height: height}
	for {
		select {
		case msg, ok := <-events:
			if !ok {
				prog.spinnerStop()
				fmt.Fprintln(out, st.err.Render("\n已中断：本轮未完成"))
				return
			}
			if msg.ack != nil {
				var writeErr error
				if turnCtx.Err() != nil {
					writeErr = turnCtx.Err()
				} else if msg.delta != "" {
					prog.spinnerStop()
					hasDelta = true
					writeErr = view.append(out, st, md, msg.delta)
				}
				msg.ack <- writeErr
				continue
			}
			prog.spinnerStop()
			if hasDelta {
				if msg.final.err == nil {
					if err := view.render(out, st, md, msg.final.result.AssistantMessage.Content); err != nil {
						fmt.Fprintln(out, st.err.Render("排版失败：")+err.Error())
					}
				}
				fmt.Fprintln(out)
			}
			if msg.final.err != nil {
				if hasDelta {
					fmt.Fprintln(out, st.err.Render("未完成：")+msg.final.err.Error())
				} else {
					if st.labels.enabled && prog.progressLines() == 0 {
						erasePrevLine(out)
					}
					fmt.Fprintln(out, st.err.Render("错误 ")+msg.final.err.Error())
				}
			} else if !hasDelta {
				for _, mv := range renderAssistant(md, msg.final.result) {
					fmt.Fprint(out, st.assistant.Render(mv.text), "\n")
				}
			}
			printGap(out, st.spacing.assistantBottom)
			return
		case <-ticker.C:
			prog.spinnerTick()
		case <-ctx.Done():
			prog.spinnerStop()
			return
		}
	}
}

// 取当前终端宽；取不到（非 tty 等）回退 80
func terminalWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 80
}

// 按主题与宽建 markdown 渲染器；建不起来（理论不可达）降级 nil，renderMarkdown 返回原文
func buildRenderer(tuiTheme string, width int) *markdownRenderer {
	md, err := newMarkdownRenderer(tuiTheme, width)
	if err != nil {
		return nil //nolint:staticcheck // 降级是有意的
	}
	return md
}

// 需要翻译成换行的按键序列：终端启用扩展键盘协议后才可区分
var newlineSeqs = [][]byte{
	[]byte("\x1b[27;2;13~"), // shift+enter（xterm modifyOtherKeys）
	[]byte("\x1b[13;2u"),    // shift+enter（kitty keyboard protocol）
	[]byte("\x1b\r"),        // option/alt + enter（多数 mac 终端原生，无需协议）
}

// newlineKeyReader 包在 stdin 外的两段字节翻译（ioloop goroutine 上被读）：
// 先菜单翻译（菜单可见时把 ↑↓/孤立 Esc/Tab 换成 MetaShiftTab 注入序列并把
// 意图入队，见 translateMenuKeys），再把 shift+enter / option+enter 序列翻译成
// 普通回车并计数（软换行）：readline 收到回车提交当前行，readMultiline 据计数
// 判定是换行还是提交。其余字节原样透传。
// 限制：终端通常一次下发完整序列；若序列被拆到两次 Read，换行序列退化为普通
// 回车提交、孤立 \x1b 被当 Esc 键处理（均文档化容忍）。
type newlineKeyReader struct {
	r io.Reader
	// 尚未交付给 readline 的翻译产物，Read 按块消费
	buf []byte
	// 未消费的软换行计数。并发事实：本包装层在 readline 的 ioloop goroutine
	// 上被读（经 bufio），计数消费在主 goroutine；Enter 字节的 channel 传递
	// 建立先于关系、类型上无同步保护——存量债务，本步不扩大（菜单状态的跨
	// goroutine 访问走 paletteCtl 内锁）
	soft int
	// 菜单会话状态；nil = 不做菜单翻译（未装配路径与既有测试）。ioloop 侧
	// 只经锁访问其意图队列与可见性快照，不触碰绘制
	ctl *paletteCtl
}

func (n *newlineKeyReader) Read(p []byte) (int, error) {
	for len(n.buf) == 0 {
		var tmp [256]byte
		rn, err := n.r.Read(tmp[:])
		if rn > 0 {
			// 两段翻译：先菜单（注入 + 意图），再软换行序列；其余透传
			out, soft := translateNewlineSeqs(n.translateMenuKeys(tmp[:rn]))
			n.buf = out
			n.soft += soft
		}
		if err != nil && len(n.buf) == 0 {
			return 0, err
		}
	}
	copied := copy(p, n.buf)
	n.buf = n.buf[copied:]
	return copied, nil
}

// 菜单拦截键与注入序列：↑↓ 为 CSI A/B；注入 \x1b[Z 是 readline 的
// MetaShiftTab——ioloop 按显式 no-op 处理且必回调 Listener，真实 shift+tab
// 无意图入队时在 Listener 侧被忽略，无副作用
var (
	menuUpSeq     = []byte("\x1b[A")
	menuDownSeq   = []byte("\x1b[B")
	menuInjectSeq = []byte("\x1b[Z")
)

// translateMenuKeys 菜单可见时把 ↑↓/孤立 Esc/Tab 翻译为注入序列并把意图
// 入队，其余字节原样透传（含换行序列，交后续 translateNewlineSeqs）；
// 菜单不可见时整体原样返回，保留 ↑↓ 历史导航、Tab 插字符、Esc 的既有
// readline 行为。注入的序列只追加到输出、不回扫，不会被二次翻译。
// 只在本包装层 Read（ioloop goroutine）内调用，经 ctl 锁与主 goroutine 交互
func (n *newlineKeyReader) translateMenuKeys(in []byte) []byte {
	if n.ctl == nil || !n.ctl.visible() {
		return in
	}
	out := make([]byte, 0, len(in)+len(menuInjectSeq))
	// 块内顺序可见性：Esc 关闭菜单后，同读块内后续键不再拦截（归还历史
	// 导航）；本块内不会重新激活（激活依赖 Listener 重算，块级模拟只需关闭方向）
	vis := true
	for len(in) > 0 {
		switch {
		case vis && bytes.HasPrefix(in, menuUpSeq):
			// ↑ 不进 readline：菜单激活时防历史导航接管方向键
			n.ctl.pushIntent(intentUp)
			out = append(out, menuInjectSeq...)
			in = in[len(menuUpSeq):]
		case vis && bytes.HasPrefix(in, menuDownSeq):
			n.ctl.pushIntent(intentDown)
			out = append(out, menuInjectSeq...)
			in = in[len(menuDownSeq):]
		case vis && in[0] == '\x1b' && len(in) > 1 && in[1] == '\x1b':
			// 同读块内 Esc 后随另一按键（如 Esc+↑ 批量到达，终端把两次按键
			// 合进一次 Read）：首字节按 Esc 键关闭菜单，后续键不再拦截。真
			// alt+方向键（\x1b\x1b[A）同编码命中此分支——alt+方向罕见，让位
			// 保 Esc 语义（pr-agent 批量路径）
			n.ctl.pushIntent(intentEsc)
			out = append(out, menuInjectSeq...)
			in = in[1:]
			vis = false
		case vis && len(in) == 1 && in[0] == '\x1b':
			// 孤立 Esc：剩余恰为单字节 \x1b 视为 Esc 键；与被拆包的序列头
			// 无法区分，退化沿既有拆包容忍（文档化）
			n.ctl.pushIntent(intentEsc)
			out = append(out, menuInjectSeq...)
			in = nil
		case vis && in[0] == '\t':
			// Tab 透传（readline 插入 '\t' 后回调 Listener 补全）+ 注入重画
			// 意图：补全改写缓冲触发的全量重绘会把菜单清掉，由注入键重画
			n.ctl.pushIntent(intentRedraw)
			out = append(out, '\t')
			out = append(out, menuInjectSeq...)
			in = in[1:]
		default:
			out = append(out, in[0])
			in = in[1:]
		}
	}
	return out
}

// consumeSoft 报告并满耗一个软换行计数
func (n *newlineKeyReader) consumeSoft() bool {
	if n.soft > 0 {
		n.soft--
		return true
	}
	return false
}

// 递归把输入中的全部换行序列替换成普通回车，返回替换后的数据与替换次数。
// 必须取所有序列中位置最靠前的那个先处理：若按序列类型固定顺序，先命中的
// 序列前面可能残留更早出现的另一种序列，被 readline 误当作孤立 ESC / 回车。
func translateNewlineSeqs(in []byte) ([]byte, int) {
	var best []byte
	bestAt := -1
	for _, seq := range newlineSeqs {
		if i := bytes.Index(in, seq); i >= 0 && (bestAt < 0 || i < bestAt) {
			best, bestAt = seq, i
		}
	}
	if bestAt < 0 {
		return in, 0
	}
	out := make([]byte, 0, len(in))
	out = append(out, in[:bestAt]...)
	out = append(out, '\r')
	rest, soft := translateNewlineSeqs(in[bestAt+len(best):])
	out = append(out, rest...)
	return out, soft + 1
}

// 渲染轮间分割线：上下空行数读 spacing（主题可配，显式 0 合法；默认上 1 下 0，
// 分割线与下轮输入之间的空行由 userTop 提供避免双空行）；
// 线本身的颜色经 divider 样式项前景色（#20：无硬编码样式）
func renderMessageDivider(out io.Writer, st styles, width int) {
	if width <= 0 {
		width = 80
	}
	printGap(out, st.spacing.dividerTop)
	fmt.Fprint(out, st.divider.Render(strings.Repeat("─", width)), "\n")
	printGap(out, st.spacing.dividerBottom)
}

// 渲染 spinner 行（清行 + 写当前帧）
func renderSpinner(out io.Writer, st styles, frame int) {
	fmt.Fprint(out, "\r\033[2K")
	fmt.Fprint(out, st.spinner.Render(spinnerFrames[frame]), " 思考中…")
}

// 清当前行
func clearLine(out io.Writer) {
	fmt.Fprint(out, "\r\033[2K")
}

// 印 n 行空行（块间距消费点共用；n<=0 不输出）
func printGap(out io.Writer, n int) {
	for i := 0; i < n; i++ {
		fmt.Fprintln(out)
	}
}

// 擦除上一行并把光标移回当前行（上移一行 → 清行 → 下移回来）；
// 用于错误路径撤回已印的称呼行，当前行留给错误消息原位接排
func erasePrevLine(out io.Writer) {
	fmt.Fprint(out, "\x1b[1A")
	clearLine(out)
	fmt.Fprint(out, "\x1b[1B")
}
