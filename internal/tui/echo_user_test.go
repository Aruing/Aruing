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
}
