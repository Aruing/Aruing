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

	"github.com/ergochat/readline"

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
func inlineRun(ctx context.Context, svc *session.Service, sessionID, format, tuiTheme string, out io.Writer) error {
	st := loadStyles(tuiTheme)
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
		// readline 提交后输入行（prompt + 内容）天然留在终端，不重复打印用户消息
		waitTurn(ctx, out, st, svc, sessionID, text)
	}
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
// 流式留位（#20）：流式落地时本函数扩展为逐 chunk print 留痕，循环结构不变
func waitTurn(ctx context.Context, out io.Writer, st styles, svc *session.Service, sessionID, text string) {
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
	renderSpinner(out, st, frame)
	for {
		select {
		case msg := <-turnCh:
			clearLine(out)
			if msg.err != nil {
				fmt.Fprintln(out, st.err.Render("错误 ")+msg.err.Error())
			} else {
				for _, mv := range renderAssistant(nil, msg.result) {
					fmt.Fprintln(out, st.assistant.Render("aruing ")+mv.text)
				}
			}
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

// 递归把输入中的全部换行序列替换成普通回车，返回替换后的数据与替换次数
func translateNewlineSeqs(in []byte) ([]byte, int) {
	for _, seq := range newlineSeqs {
		if i := bytes.Index(in, seq); i >= 0 {
			out := make([]byte, 0, len(in))
			out = append(out, in[:i]...)
			out = append(out, '\r')
			rest, soft := translateNewlineSeqs(in[i+len(seq):])
			out = append(out, rest...)
			return out, soft + 1
		}
	}
	return in, 0
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
