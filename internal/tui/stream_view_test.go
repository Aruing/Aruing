package tui

import (
	"regexp"
	"strconv"
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

// 围栏补齐会收缩渲染行数，随后片段仍须从终端实际的末行继续
func TestInlineStreamCursor(t *testing.T) {
	md, err := newMarkdownRenderer("dark", 40)
	if err != nil {
		t.Fatal(err)
	}
	view := inlineStreamView{width: 40, height: 24}
	upRE := regexp.MustCompile("\x1b\\[([0-9]+)A")
	cursorRow := 0
	for _, r := range "Example:\n\n```go\nprintln(1)\n```\nDone" {
		var out strings.Builder
		if err := view.append(&out, mustLoadStyles("dark"), md, string(r)); err != nil {
			t.Fatal(err)
		}
		for _, match := range upRE.FindAllStringSubmatch(out.String(), -1) {
			up, err := strconv.Atoi(match[1])
			if err != nil {
				t.Fatal(err)
			}
			cursorRow -= up
		}
		cursorRow += strings.Count(out.String(), "\n")
		if cursorRow != len(view.lines)-1 {
			t.Fatalf("after %q: cursor row=%d, last row=%d, output=%q", r, cursorRow, len(view.lines)-1, out.String())
		}
	}
}

// 末行增长不能改变表头和已完成行的列宽
func TestInlineStreamTable(t *testing.T) {
	md, err := newMarkdownRenderer("dark", 40)
	if err != nil {
		t.Fatal(err)
	}
	view := inlineStreamView{width: 40, height: 24}
	st := mustLoadStyles("dark")
	var out strings.Builder
	initial := "| Name | Description |\n| --- | --- |\n| alpha | small |\n| beta | growing descri"
	if err := view.append(&out, st, md, initial); err != nil {
		t.Fatal(err)
	}
	before := strings.Join(view.lines[:len(view.lines)-1], "\n")
	if err := view.append(&out, st, md, "p"); err != nil {
		t.Fatal(err)
	}
	after := strings.Join(view.lines[:len(view.lines)-1], "\n")
	if before != after {
		t.Fatalf("completed rows moved:\n%s\n->\n%s", stripANSI(before), stripANSI(after))
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

// 收缩跨过屏内首行后，后续更新不能按已消失的历史行数回退
func TestInlineStreamShrink(t *testing.T) {
	view := inlineStreamView{width: 80, height: 4}
	st := mustLoadStyles("dark")
	var out strings.Builder
	for _, text := range []string{"a\nb\nc\nd\ne\nf", "short"} {
		if err := view.render(&out, st, nil, text); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(out.String(), "short") {
		t.Fatal("shrunk reply disappeared")
	}
	out.Reset()
	if err := view.render(&out, st, nil, "changed\nmore"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "A") || !strings.Contains(out.String(), "changed\r\nmore") {
		t.Fatalf("cursor moved above retained reply: %q", out.String())
	}
}
