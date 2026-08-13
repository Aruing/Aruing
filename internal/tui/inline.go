// 行内 TUI：消息留痕（终端普通输出往下滚）+ 底部输入框 + raw mode readline + 多行 + 容错。
// 与 app（bubbletea 全屏）模式并列；默认走此模式（Step 1 硬编码，Step 3 加 config.TUI.Mode 选）。
// 纯展示层——事实来自 svc.Turn，消息 print 后留 scrollback，不持有业务事实（守 #20）。
package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"aruing/internal/session"
	"golang.org/x/term"
)

// spinner 帧间隔与帧表（自写，不引 bubbles spinner——行内模式要直接控制光标）
const (
	spinnerInterval = 100 * time.Millisecond
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// 按键动作类别
type keyKind int

const (
	keyChar    keyKind = iota
	keyEnter           // 提交
	keyNewline         // 换行（shift+enter / alt+enter / ctrl+j）
	keyBackspace
	keyCtrlC
	keyUnknown
	keyEOF
)

// 一个按键事件
type keyEvent struct {
	kind keyKind
	r    rune // 仅 keyChar 有效
}

// 行内模式入口：raw mode 读键 + 留痕输出 + 底部输入框 + spinner + 容错
func inlineRun(ctx context.Context, svc *session.Service, sessionID, format, tuiTheme string, out io.Writer) error {
	st := loadStyles(tuiTheme)

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return fmt.Errorf("inline chat requires a terminal (stdin is not a tty)")
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("enter raw mode: %w", err)
	}
	defer func() { _ = term.Restore(fd, old) }()

	r := bufio.NewReader(os.Stdin)
	var buf strings.Builder // 输入缓冲，可含 \n 表示多行

	renderInput := func() { renderInlineInput(out, st, buf.String()) }
	renderInput()

	for {
		k, err := readKey(r)
		if err == io.EOF || k.kind == keyEOF {
			fmt.Fprintln(out)
			return nil
		}
		if err != nil {
			return fmt.Errorf("read key: %w", err)
		}
		switch k.kind {
		case keyCtrlC:
			fmt.Fprintln(out)
			return nil
		case keyChar:
			buf.WriteRune(k.r)
		case keyBackspace:
			backspaceBuffer(&buf)
		case keyNewline:
			buf.WriteRune('\n')
		case keyEnter:
			text := strings.TrimSpace(buf.String())
			if text == "" {
				buf.Reset()
				renderInput()
				continue
			}
			if text == "exit" || text == "quit" {
				fmt.Fprintln(out)
				return nil
			}
			// 留痕：清除当前输入框，print 用户消息，提交
			clearInlineInput(out, buf.String())
			fmt.Fprintln(out, st.user.Render("你 ")+text)
			last := buf.String()
			buf.Reset()
			waitTurn(ctx, out, st, svc, sessionID, text)
			_ = last
			renderInput()
			continue
		}
		renderInput()
	}
}

// 等待一轮 Turn 完成期间在底部渲染 spinner 动画；完成后 print 助手 / 错误（留痕）
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

// 渲染底部输入框：回车行首、清行、写提示符与当前输入（多行时含 \n 跨行）
func renderInlineInput(out io.Writer, st styles, buf string) {
	fmt.Fprint(out, "\r\033[2K") // 回车行首 + 清当前行
	fmt.Fprint(out, st.prompt.Render("❯ "), buf)
}

// 提交时清除输入框占的行：按缓冲中的换行数上溯清行
func clearInlineInput(out io.Writer, last string) {
	lines := strings.Count(last, "\n") // N 个换行 => 至少 N+1 行，但起始于最后一行
	for i := 0; i < lines; i++ {
		fmt.Fprint(out, "\r\033[2K\033[A") // 清行 + 上移一行
	}
	fmt.Fprint(out, "\r\033[2K") // 清当前行
}

// 渲染 spinner 行（清行 + 写当前帧）
func renderSpinner(out io.Writer, st styles, frame int) {
	fmt.Fprint(out, "\r\033[2K")
	fmt.Fprint(out, st.spinner.Render(spinnerFrames[frame]), " 思考中…")
}

// 清当前行（spinner / 消息接替用）
func clearLine(out io.Writer) {
	fmt.Fprint(out, "\r\033[2K")
}

// 删除缓冲末尾一个 rune（支持跨换行回退）
func backspaceBuffer(buf *strings.Builder) {
	s := buf.String()
	if s == "" {
		return
	}
	r := []rune(s)
	buf.Reset()
	buf.WriteString(string(r[:len(r)-1]))
}

// 读取一个按键事件：可见字符 / 控制键 / ANSI 序列
func readKey(r *bufio.Reader) (keyEvent, error) {
	b, err := r.ReadByte()
	if err != nil {
		return keyEvent{kind: keyEOF}, err
	}
	switch b {
	case 0x03:
		return keyEvent{kind: keyCtrlC}, nil
	case 0x0D: // \r = enter
		return keyEvent{kind: keyEnter}, nil
	case 0x0A: // \n = ctrl+j，换行最稳
		return keyEvent{kind: keyNewline}, nil
	case 0x7F, 0x08: // backspace / ^H
		return keyEvent{kind: keyBackspace}, nil
	case 0x1B: // ESC，可能 alt 序列或 ANSI
		return readEscape(r)
	}
	if b&0x80 != 0 {
		// 多字节 UTF-8：回退后用 ReadRune
		_ = r.UnreadByte()
		rr, _, err := r.ReadRune()
		if err != nil {
			return keyEvent{kind: keyEOF}, err
		}
		return keyEvent{kind: keyChar, r: rr}, nil
	}
	return keyEvent{kind: keyChar, r: rune(b)}, nil
}

// 解析 ESC 起始序列：alt+enter（\x1b\r）、shift+enter（\x1b[27;2;13~ / kitty \x1b[13;2u）等
// 单 ESC（无后续）当作未知（不绑退出，避免误触）
func readEscape(r *bufio.Reader) (keyEvent, error) {
	next, err := r.ReadByte()
	if err != nil {
		return keyEvent{kind: keyUnknown}, nil
	}
	switch next {
	case 0x0D: // \x1b\r = alt+enter
		return keyEvent{kind: keyNewline}, nil
	case '[':
		rest, _ := readCSITail(r)
		seq := "[" + rest
		switch {
		case strings.Contains(seq, "27;2;13"): // xterm modifyOtherKeys shift+enter
			return keyEvent{kind: keyNewline}, nil
		case strings.Contains(seq, "13;2u"): // kitty CSI u shift+enter
			return keyEvent{kind: keyNewline}, nil
		case strings.Contains(seq, "13;3u"), strings.Contains(seq, "13;4u"): // kitty alt/meta+enter 变体
			return keyEvent{kind: keyNewline}, nil
		default:
			return keyEvent{kind: keyUnknown}, nil // 方向键等忽略
		}
	}
	return keyEvent{kind: keyUnknown}, nil
}

// 读 CSI 序列尾：从 '[' 之后读到终止符（0x40–0x7e），防失控限制长度
func readCSITail(r *bufio.Reader) (string, error) {
	var sb strings.Builder
	for i := 0; i < 16; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return sb.String(), err
		}
		sb.WriteByte(b)
		if b >= 0x40 && b <= 0x7e { // 终止符 @ A..Z [ \\ ] ^ _ ` a..z { | } ~
			return sb.String(), nil
		}
	}
	return sb.String(), nil
}
