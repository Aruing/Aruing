// glamour Markdown 渲染：按主题 + 宽度渲染 Turn 正文与 Report。
// 渲染失败或 renderer 未就绪时降级返回原文，不阻断交互（守 #18 不丢内容）。
package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// 同一主题和宽度下的正文渲染器；临时表格需要保留源文本行边界
type markdownRenderer struct {
	// 完整正文使用既有段落与表格排版
	final *glamour.TermRenderer
	// 临时表格逐行显示，禁止段落重排合并各行
	streaming *glamour.TermRenderer
}

// 按主题与宽度建 markdown renderer；主题空/auto 走 glamour AutoStyle（自动检测终端）
func newMarkdownRenderer(theme string, width int) (*markdownRenderer, error) {
	opts := []glamour.TermRendererOption{glamour.WithWordWrap(width)}
	switch resolveTheme(theme) {
	case "dark":
		opts = append(opts, glamour.WithStandardStyle("dark"))
	case "light":
		opts = append(opts, glamour.WithStandardStyle("light"))
	default:
		opts = append(opts, glamour.WithAutoStyle())
	}
	final, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		return nil, err
	}
	streaming, err := glamour.NewTermRenderer(append(opts, glamour.WithPreservedNewLines())...)
	if err != nil {
		return nil, err
	}
	return &markdownRenderer{final: final, streaming: streaming}, nil
}

// 渲染 markdown；renderer 为 nil 或空文本或失败时返回原文
func renderMarkdown(r *markdownRenderer, md string) string {
	if r == nil {
		return md
	}
	return renderMarkdownWith(r.final, md)
}

// 共用渲染失败的原文回退，临时排版和最终排版均不丢内容
func renderMarkdownWith(r *glamour.TermRenderer, md string) string {
	if r == nil || strings.TrimSpace(md) == "" {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return strings.TrimRight(out, "\n")
}

// 生成期间保留表格各行的原始列分隔，避免新单元格反复改变已有列宽；终态仍按正式表格渲染
func renderStreamingMarkdown(r *markdownRenderer, md string) string {
	if r == nil || !strings.Contains(md, "|") {
		return renderMarkdown(r, md)
	}
	source := []byte(md)
	parser := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList)).Parser()
	doc := parser.Parse(text.NewReader(source))
	escaped := make(map[int]bool)
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || node.Kind() != extast.KindTable {
			return ast.WalkContinue, nil
		}
		start := node.FirstChild().Pos()
		headerEnd := start + strings.IndexByte(md[start:], '\n')
		delimiterEnd := markdownLineEnd(md, headerEnd+1)
		// 只转义已被语法树确认为表格的分隔行，不干扰代码围栏里的相似文本
		for i := headerEnd + 1; i < delimiterEnd; i++ {
			if md[i] == '-' {
				escaped[i] = true
			}
		}
		return ast.WalkSkipChildren, nil
	})
	if len(escaped) == 0 {
		return renderMarkdown(r, md)
	}
	var stable strings.Builder
	for i := range len(md) {
		if escaped[i] {
			stable.WriteByte('\\')
		}
		stable.WriteByte(md[i])
	}
	return renderMarkdownWith(r.streaming, stable.String())
}

// 返回当前源文本行的换行位置；末行没有换行时返回文本长度
func markdownLineEnd(md string, start int) int {
	if offset := strings.IndexByte(md[start:], '\n'); offset >= 0 {
		return start + offset
	}
	return len(md)
}
