package tui

import (
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/session"
)

// 续行符判断：单个 \ 结尾续行，双 \\ 字面反斜杠
func TestContinuation(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		want  string
		moreW bool
	}{
		{"普通行", "你好", "你好\n", false},
		{"空行", "", "\n", false},
		{"续行", "帮我看\\", "帮我看\n", true},
		{"续行带尾空格", "帮我看\\  ", "帮我看\n", true},
		{"字面反斜杠", "路径 C:\\\\", "路径 C:\\\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, more := continuation(c.line)
			if got != c.want || more != c.moreW {
				t.Fatalf("continuation(%q) = (%q, %v), want (%q, %v)", c.line, got, more, c.want, c.moreW)
			}
		})
	}
}

// 续行拼接最终产出多行文本
func TestMultilineJoin(t *testing.T) {
	// continuation 是 readMultiline 的纯逻辑核心：三行续一行收尾
	var b strings.Builder
	for _, line := range []string{"第一行\\", "第二行\\", "第三行"} {
		content, more := continuation(line)
		b.WriteString(content)
		if !more {
			break
		}
	}
	got := b.String()
	want := "第一行\n第二行\n第三行\n"
	if got != want {
		t.Fatalf("join = %q want %q", got, want)
	}
	if !strings.Contains(got, "\n") {
		t.Fatal("multiline join should contain newlines")
	}
}

// 换行序列翻译：shift+enter / option+enter → 普通回车 + 软换行计数（屏幕无 \ 残留）
func TestTranslateNewlineSeqs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		soft int
	}{
		{"xterm shift+enter", "帮我\x1b[27;2;13~看", "帮我\r看", 1},
		{"kitty shift+enter", "\x1b[13;2u", "\r", 1},
		{"option+enter", "帮\x1b\r我", "帮\r我", 1},
		{"多个序列", "\x1b[13;2u\x1b\r", "\r\r", 2},
		{"混合顺序：option 在前", "\x1b\r\x1b[13;2u", "\r\r", 2},
		{"混合顺序：option 前后夹文本", "前\x1b\r中\x1b[27;2;13~后", "前\r中\r后", 2},
		{"普通回车不动", "你好\r", "你好\r", 0},
		{"方向键透传", "\x1b[A", "\x1b[A", 0},
		{"普通文本透传", "hello 世界", "hello 世界", 0},
		{"孤立 ESC 透传", "\x1b", "\x1b", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, soft := translateNewlineSeqs([]byte(c.in))
			if string(got) != c.want || soft != c.soft {
				t.Fatalf("translate(%q) = (%q, %d), want (%q, %d)", c.in, got, soft, c.want, c.soft)
			}
		})
	}
}

// 软换行计数消费：先报 true 耗尽后报 false
func TestConsumeSoft(t *testing.T) {
	n := &newlineKeyReader{}
	n.soft = 2
	if !n.consumeSoft() {
		t.Fatal("first consumeSoft should be true")
	}
	if !n.consumeSoft() {
		t.Fatal("second consumeSoft should be true")
	}
	if n.consumeSoft() {
		t.Fatal("third consumeSoft should be false")
	}
}

// 轮间分割线：默认结构 = 上 1 空行 + 水平线（下方 0 空行，与下轮输入的空行由 userTop 提供）；
// margin 覆盖结构；宽度自适应与非法回退
func TestRenderMessageDivider(t *testing.T) {
	st := mustLoadStyles("dark")

	var b strings.Builder
	renderMessageDivider(&b, st, 10)
	got := b.String()
	// 默认 spacing：divider 上 1 下 0 → 空行 + 线，恰一个结尾换行
	if !strings.HasSuffix(got, "─\n") || strings.HasSuffix(got, "\n\n") {
		t.Fatalf("divider must end with the line + one newline: %q", got)
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 2 || strings.TrimSpace(lines[0]) != "" {
		t.Fatalf("want blank/line structure, got %q", got)
	}
	if !strings.Contains(lines[1], strings.Repeat("─", 10)) {
		t.Fatalf("divider line missing in %q", got)
	}

	// margin 覆盖：上 2 下 1 → 2 空行 + 线 + 1 空行
	over, err := loadStyles("dark", writeTheme(t, "styles:\n  divider:\n    margin: [2, 0, 1, 0]\n"))
	if err != nil {
		t.Fatalf("loadStyles: %v", err)
	}
	b.Reset()
	renderMessageDivider(&b, over, 10)
	lines = strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if len(lines) != 4 || strings.TrimSpace(lines[0]) != "" || strings.TrimSpace(lines[1]) != "" || strings.TrimSpace(lines[3]) != "" {
		t.Fatalf("want 2 blank + line + 1 blank, got %q", b.String())
	}

	// 非法宽度回退 80
	b.Reset()
	renderMessageDivider(&b, st, 0)
	if !strings.Contains(b.String(), strings.Repeat("─", 80)) {
		t.Fatal("width<=0 should fall back to 80")
	}
}

// printGap：n 行空行；n<=0 不输出
func TestPrintGap(t *testing.T) {
	var b strings.Builder
	printGap(&b, 0)
	if b.Len() != 0 {
		t.Fatalf("gap 0 should print nothing, got %q", b.String())
	}
	printGap(&b, 2)
	if b.String() != "\n\n" {
		t.Fatalf("gap 2 = %q, want two newlines", b.String())
	}
}

// erasePrevLine：上移 → 清行 → 下移回原行（错误路径撤回称呼行的光标序列）
func TestErasePrevLine(t *testing.T) {
	var b strings.Builder
	erasePrevLine(&b)
	if got, want := b.String(), "\x1b[1A\r\x1b[2K\x1b[1B"; got != want {
		t.Fatalf("erasePrevLine = %q, want %q", got, want)
	}
}

// 行内 markdown：有 renderer 时输出含 glamour 样式（非降级原文）
func TestInlineMarkdownRendered(t *testing.T) {
	r, err := newMarkdownRenderer("dark", 80)
	if err != nil {
		t.Fatalf("newMarkdownRenderer: %v", err)
	}
	views := renderAssistant(r, session.TurnResult{
		AssistantMessage: session.Message{Content: "# 标题", Mode: session.ModeBaseline},
	})
	if len(views) != 1 {
		t.Fatalf("views = %d", len(views))
	}
	if !strings.Contains(views[0].text, "标题") {
		t.Fatalf("rendered = %q", views[0].text)
	}
	// 降级原文（纯文本）不含 ANSI 转义序列；渲染后应含
	if !strings.Contains(views[0].text, "\x1b[") {
		t.Fatalf("expected glamour ANSI styling, got %q", views[0].text)
	}
}
