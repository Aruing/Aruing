package tui

import (
	"strings"
	"testing"
)

// 已稳定的行不重放，当前行可更新；完成时重复同一正文不会再写终端
func TestInlineStreamView(t *testing.T) {
	st := mustLoadStyles("dark")
	view := inlineStreamView{width: 80, height: 24}
	var out strings.Builder
	for _, part := range []string{"first\n", "second", " more"} {
		if err := view.append(&out, st, nil, part); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Count(out.String(), "first") != 1 {
		t.Fatalf("stable line replayed: %q", out.String())
	}
	before := out.Len()
	if err := view.render(&out, st, nil, "first\nsecond more"); err != nil {
		t.Fatal(err)
	}
	if out.Len() != before {
		t.Fatal("completed body replayed")
	}
}

// 流式正文使用既有 Markdown 渲染器，代码块和加粗不退化为逐块裸文本
func TestInlineStreamMarkdown(t *testing.T) {
	md, err := newMarkdownRenderer("dark", 60)
	if err != nil {
		t.Fatal(err)
	}
	view := inlineStreamView{width: 80, height: 24}
	var out strings.Builder
	if err = view.append(&out, mustLoadStyles("dark"), md, "**strong**\n\n```go\nfmt.Println(1)\n```"); err != nil {
		t.Fatal(err)
	}
	got := stripANSI(strings.Join(view.lines, "\n"))
	if !strings.Contains(got, "strong") || strings.Contains(got, "**strong**") || !strings.Contains(got, "Println") {
		t.Fatalf("markdown view: %q", got)
	}
}

// 长回复只重画屏内尾部，不能回退到此前已滚走的行
func TestInlineStreamViewport(t *testing.T) {
	view := inlineStreamView{width: 80, height: 4}
	st := mustLoadStyles("dark")
	var out strings.Builder
	if err := view.render(&out, st, nil, "a\nb\nc\nd\ne\nf"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := view.render(&out, st, nil, "A\nB\nC\nD\nE\nF"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "\x1b[2A") || strings.Contains(out.String(), "A\r\nB") {
		t.Fatalf("redrew beyond viewport: %q", out.String())
	}
}
