package tui

import (
	"regexp"
	"strings"
	"testing"
)

// 剥离 ANSI 转义序列（测试断言只关心文本相邻性；lipgloss 在管道下不发色、
// 在强制色 profile 下会发，断言不应依赖这个环境行为）
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// user 样式项 inline 消费：重印留痕为整行 user 样式内容，默认无称呼前缀；多行逐行渲染
func TestEchoUserMessage(t *testing.T) {
	var b strings.Builder
	echoUserMessage(&b, mustLoadStyles("dark"), "hello")
	got := b.String()
	if !strings.Contains(got, "hello") {
		t.Fatalf("missing user echo: %q", got)
	}
	if strings.Contains(got, "你 ") {
		t.Fatalf("default echo must not carry label prefix: %q", got)
	}
	// 多行：默认 spacing 上方 1 空行 + 三行内容，共 4 个换行
	b.Reset()
	echoUserMessage(&b, mustLoadStyles("dark"), "a\nb\nc")
	if n := strings.Count(b.String(), "\n"); n != 4 {
		t.Fatalf("multiline newline count = %d, want 4 (1 gap + 3 lines): %q", n, b.String())
	}
	// 折行行数估算：宽字符按 2 列、终端 80 列
	if got := displayCols("中文ab"); got != 6 {
		t.Fatalf("displayCols = %d, want 6", got)
	}
	if got := rowsFor(160, 80); got != 2 {
		t.Fatalf("rowsFor = %d, want 2", got)
	}
	if got := rowsFor(81, 80); got != 2 {
		t.Fatalf("rowsFor rounding = %d, want 2", got)
	}
	if got := rowsFor(10, 0); got != 1 {
		t.Fatalf("rowsFor zero width = %d, want 1", got)
	}
}

// 称呼开启：先单独一行称呼（user 样式），再换行放内容
func TestEchoUserMessageLabels(t *testing.T) {
	st, err := loadStyles("dark", writeTheme(t, "labels:\n  enabled: true\n"))
	if err != nil {
		t.Fatalf("loadStyles: %v", err)
	}
	var b strings.Builder
	echoUserMessage(&b, st, "hello")
	got := stripANSI(b.String())
	// 断言只验顺序：称呼行先于内容行出现，中间无其它文本
	if i, j := strings.Index(got, "你\n"), strings.Index(got, "hello"); i < 0 || j < 0 || i > j {
		t.Fatalf("want label line then content: %q", got)
	}
}
