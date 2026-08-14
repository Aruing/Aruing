// 行内 TUI：readline 库读输入（输入行提交后天然留痕）+ 助手消息 print 往下滚 + spinner + 容错。
// 与 app（bubbletea 全屏）模式并列；默认走此模式（Step 3 加 config.TUI.Mode 选）。
// 纯展示层——事实来自 svc.Turn，不持有业务事实（守 #20）。
// 多行输入用续行符约定（行尾 \ + enter 续行）：readline 库不支持 shift+enter 序列捕获，
// 终端对 shift+enter 的编码也不统一（xterm/kitty 各异），续行符是跨终端稳定的功能替代。
package tui

import (
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

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// 行内模式入口：readline 循环 + Turn 等待（spinner）+ 助手留痕 + 容错
func inlineRun(ctx context.Context, svc *session.Service, sessionID, format, tuiTheme string, out io.Writer) error {
	st := loadStyles(tuiTheme)
	rl, err := readline.NewFromConfig(&readline.Config{
		Prompt: st.prompt.Render("❯ "),
		Stdin:  os.Stdin,
		Stdout: out,
		Stderr: os.Stderr,
	})
	if err != nil {
		return fmt.Errorf("init readline: %w", err)
	}
	defer rl.Close()

	// 一次性交互提示（系统样式，留痕）
	fmt.Fprintln(out, st.system.Render("多行输入：行尾 \\ + 回车续行；exit / Ctrl+D 退出"))

	for {
		text, err := readMultiline(rl)
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

// 读多行输入：行尾单个反斜杠 + enter 表示续行，拼接下一行；空行或无续行符即完成
func readMultiline(rl *readline.Instance) (string, error) {
	var b strings.Builder
	for {
		line, err := rl.ReadLine()
		if err != nil {
			return "", err
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

// 渲染 spinner 行（清行 + 写当前帧）
func renderSpinner(out io.Writer, st styles, frame int) {
	fmt.Fprint(out, "\r\033[2K")
	fmt.Fprint(out, st.spinner.Render(spinnerFrames[frame]), " 思考中…")
}

// 清当前行
func clearLine(out io.Writer) {
	fmt.Fprint(out, "\r\033[2K")
}
