package tui

import (
	"strings"
	"testing"
)

// 流式表格保留每行内容；围栏中的示例和无表格正文沿用原有渲染
func TestStreamingMarkdown(t *testing.T) {
	md, err := newMarkdownRenderer("dark", 80)
	if err != nil {
		t.Fatal(err)
	}
	table := "| Name | Description |\n| --- | --- |\n| alpha | **small** |\n"
	for _, tc := range []struct {
		name string
		text string
	}{
		{name: "table", text: table},
		{name: "intro", text: "Introduction\n" + table},
		{name: "quote", text: "> " + strings.ReplaceAll(table, "\n", "\n> ")},
		{name: "crlf", text: strings.ReplaceAll(table, "\n", "\r\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := stripANSI(renderStreamingMarkdown(md, tc.text))
			if strings.Contains(live, "┼") || !strings.Contains(live, "alpha | small") {
				t.Fatalf("unstable table or missing cell: %q", live)
			}
			final := stripANSI(renderMarkdown(md, tc.text))
			if !strings.Contains(final, "┼") || !strings.Contains(final, "alpha") {
				t.Fatalf("final table lost formatting: %q", final)
			}
		})
	}
	for _, content := range []string{"**strong**\n\ntext | text", "```md\n" + table + "```"} {
		if got, want := renderStreamingMarkdown(md, content), renderMarkdown(md, content); got != want {
			t.Fatalf("unrelated markdown changed: %q", got)
		}
	}
}
