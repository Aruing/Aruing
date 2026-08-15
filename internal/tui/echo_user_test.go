package tui

import (
	"strings"
	"testing"
)

// user 样式项 inline 消费：重印留痕含 user 颜色与「你 」前缀；多行逐行渲染
func TestEchoUserMessage(t *testing.T) {
	var b strings.Builder
	echoUserMessage(&b, mustLoadStyles("dark"), "hello")
	got := b.String()
	if !strings.Contains(got, "你 hello") {
		t.Fatalf("missing user echo: %q", got)
	}
	// 多行：三行各渲染一次
	b.Reset()
	echoUserMessage(&b, mustLoadStyles("dark"), "a\nb\nc")
	if n := strings.Count(b.String(), "你 "); n != 3 {
		t.Fatalf("multiline echo count = %d", n)
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
