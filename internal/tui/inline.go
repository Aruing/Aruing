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
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/ergochat/readline"
	"golang.org/x/term"

	"aruing/internal/session"
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
func inlineRun(ctx context.Context, svc *session.Service, sessionID, format, tuiTheme, themeFile string, out io.Writer) error {
	st, err := loadStyles(tuiTheme, themeFile)
	if err != nil {
		return err
	}
	// 行内引擎逐键读取依赖 raw mode：非 tty（管道 / 脚本）明确报错不降级；
	// 无人值守场景应用单句模式（chat "问题"，一次 Turn 后退出）
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("inline chat requires a terminal (for non-interactive use: aruing chat \"question\")")
	}
	// 启用 modifyOtherKeys：让支持的终端对 shift+enter 上报独立序列（不支持的终端忽略此序列）
	fmt.Fprint(out, modifyOtherKeysOn)
	defer fmt.Fprint(out, modifyOtherKeysOff)

	keys := &newlineKeyReader{r: os.Stdin}

	rl, err := readline.NewFromConfig(&readline.Config{
		Prompt: st.prompt.Render("❯ "),
		Stdin:  keys,
		Stdout: out,
		Stderr: os.Stderr,
	})
	if err != nil {
		return fmt.Errorf("init readline: %w", err)
	}
	defer rl.Close()

	// 终端宽度与 markdown 渲染器：每轮提交时重取宽度（ioctl 开销可忽略），
	// 宽度变了才重建 renderer（重建需解析 style 配置，毫秒级，仅在变化时付）。
	// 这样会话中途调整终端宽，markdown 换行宽与 divider 宽都能自适应
	width := terminalWidth()
	md := buildRenderer(tuiTheme, width)

	// 一次性交互提示（系统样式，留痕）
	fmt.Fprintln(out, st.system.Render("多行输入：shift+enter / option+enter（终端支持时）/ 行尾 \\ + 回车；exit / Ctrl+D 退出"))

	for {
		text, err := readMultiline(rl, keys)
		if err != nil {
			// io.EOF（Ctrl+D）或 ErrInterrupt（Ctrl+C）：正常退出
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
		// 每轮重取终端宽：变了则重建 renderer，后续 divider 与 markdown 都用新宽
		if w := terminalWidth(); w != width {
			width = w
			md = buildRenderer(tuiTheme, width)
		}
		// 提交后把 readline 裸回显的输入行替换为主题渲染的留痕（user 样式项与
		// 称呼开关在此消费，守 #20：与 app 模式 viewport 历史同规则）
		echoUserMessage(out, st, text)
		waitTurn(ctx, out, st, md, svc, sessionID, text)
		// 轮间分割：一轮（用户输入 + 助手输出/错误）结束后与下一轮之间加分割线
		renderMessageDivider(out, st, width)
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

// 等待一轮 Turn 完成：期间单行 spinner 动画；完成后 print 助手 / 错误（留痕）
// 容错：Turn 失败 print 错误后返回，外层循环继续读输入（不杀会话）
// spinner 与回复同属助手块：块首空行读 spacing.assistantTop，spinner 行完成后被内容原位替换
// 流式留位（#20）：流式落地时本函数扩展为逐 chunk print 留痕，循环结构不变
func waitTurn(ctx context.Context, out io.Writer, st styles, md *glamour.TermRenderer, svc *session.Service, sessionID, text string) {
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	turnCh := make(chan turnMsg, 1)
	go func() {
		result, err := svc.Turn(turnCtx, sessionID, text)
		turnCh <- turnMsg{result: result, err: err}
	}()

	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()
	frame := 0
	// 助手块首：空行 + （称呼开启时）称呼行 + spinner 首帧
	printGap(out, st.spacing.assistantTop)
	if st.labels.enabled {
		fmt.Fprint(out, st.assistant.Render(st.labels.assistant), "\n")
	}
	renderSpinner(out, st, frame)
	for {
		select {
		case msg := <-turnCh:
			// 只清 spinner 行（上方空行与称呼行保留），内容从原位接排
			clearLine(out)
			// ctx 已取消时 Turn 多半返回 context canceled：静默退出，不打印伪错误
			// （select 在 turnCh 与 ctx.Done 同时就绪时随机选中，需显式优先取消）
			if turnCtx.Err() != nil {
				return
			}
			if msg.err != nil {
				fmt.Fprintln(out, st.err.Render("错误 ")+msg.err.Error())
			} else {
				for _, mv := range renderAssistant(md, msg.result) {
					// 正文整行经 assistant 样式项渲染，无称呼前缀（称呼行由 labels 开启时单独输出）
					fmt.Fprint(out, st.assistant.Render(mv.text), "\n")
				}
			}
			// 助手块收尾空行（默认 0；主题可配）
			printGap(out, st.spacing.assistantBottom)
			return
		case <-ticker.C:
			frame = (frame + 1) % len(spinnerFrames)
			renderSpinner(out, st, frame)
		case <-ctx.Done():
			clearLine(out)
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
func buildRenderer(tuiTheme string, width int) *glamour.TermRenderer {
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

// newlineKeyReader 包在 stdin 外，把 shift+enter / option+enter 序列翻译成普通回车
// 并计数（软换行）：readline 收到回车提交当前行，readMultiline 据计数判定是换行
// 还是提交，反斜杠不进缓冲区、屏幕无残留。普通字节（含方向键等其它 ESC 序列）原样透传。
// 限制：终端通常一次下发完整序列；若序列被拆到两次 Read，会退化为普通回车提交（可接受）。
type newlineKeyReader struct {
	r   io.Reader
	buf []byte
	// 未消费的软换行计数；readline 在同一线程内同步读 stdin，无并发竞争
	soft int
}

func (n *newlineKeyReader) Read(p []byte) (int, error) {
	for len(n.buf) == 0 {
		var tmp [256]byte
		rn, err := n.r.Read(tmp[:])
		if rn > 0 {
			out, soft := translateNewlineSeqs(tmp[:rn])
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
