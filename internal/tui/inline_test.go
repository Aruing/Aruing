package tui

import (
	"bufio"
	"strings"
	"testing"
)

// 按 keyKind 比较的断言
func mustKey(t *testing.T, r *bufio.Reader, want keyKind) keyKind {
	t.Helper()
	got, err := readKey(r)
	if err != nil {
		t.Fatalf("readKey err: %v", err)
	}
	if got.kind != want {
		t.Fatalf("kind=%v want %v (r=%q)", got.kind, want, got.r)
	}
	return got.kind
}

func TestReadKeyEnter(t *testing.T) { mustKey(t, bufio.NewReader(strings.NewReader("\r")), keyEnter) }
func TestReadKeyCtrlJNewline(t *testing.T) {
	mustKey(t, bufio.NewReader(strings.NewReader("\n")), keyNewline)
}
func TestReadKeyCtrlC(t *testing.T) { mustKey(t, bufio.NewReader(strings.NewReader("\x03")), keyCtrlC) }
func TestReadKeyBackspace(t *testing.T) {
	mustKey(t, bufio.NewReader(strings.NewReader("\x7f")), keyBackspace)
}

func TestReadKeyCharASCII(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("a"))
	k, err := readKey(r)
	if err != nil || k.kind != keyChar || k.r != 'a' {
		t.Fatalf("got %+v err=%v", k, err)
	}
}

func TestReadKeyUTF8(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("你")) // 3 字节 UTF-8
	k, err := readKey(r)
	if err != nil || k.kind != keyChar || k.r != '你' {
		t.Fatalf("got %+v err=%v", k, err)
	}
}

// alt+enter：\x1b\r -> 换行
func TestReadKeyAltEnter(t *testing.T) {
	mustKey(t, bufio.NewReader(strings.NewReader("\x1b\r")), keyNewline)
}

// shift+enter：xterm modifyOtherKeys \x1b[27;2;13~
func TestReadKeyShiftEnter(t *testing.T) {
	mustKey(t, bufio.NewReader(strings.NewReader("\x1b[27;2;13~")), keyNewline)
}

// shift+enter：kitty CSI u \x1b[13;2u
func TestReadKeyKittyShiftEnter(t *testing.T) {
	mustKey(t, bufio.NewReader(strings.NewReader("\x1b[13;2u")), keyNewline)
}

// alt+enter：kitty 变体 \x1b[13;3u
func TestReadKeyKittyAltEnter(t *testing.T) {
	mustKey(t, bufio.NewReader(strings.NewReader("\x1b[13;3u")), keyNewline)
}

// 方向键 \x1b[A 识别为 unknown（忽略，不误作换行）
func TestReadKeyArrowUnknown(t *testing.T) {
	mustKey(t, bufio.NewReader(strings.NewReader("\x1b[A")), keyUnknown)
}

// backspaceBuffer 删末尾 rune，含 UTF-8
func TestBackspaceBuffer(t *testing.T) {
	var b strings.Builder
	b.WriteString("ab你")
	backspaceBuffer(&b)
	if b.String() != "ab" {
		t.Fatalf("after backspace=%q want ab", b.String())
	}
	backspaceBuffer(&b)
	if b.String() != "a" {
		t.Fatalf("after backspace=%q want a", b.String())
	}
	backspaceBuffer(&b)
	if b.String() != "" {
		t.Fatalf("after backspace=%q want empty", b.String())
	}
	// 空不 panic
	backspaceBuffer(&b)
	if b.String() != "" {
		t.Fatalf("empty backspace should noop, got %q", b.String())
	}
}
