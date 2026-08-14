package tui

import (
	"strings"
	"testing"
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
