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
