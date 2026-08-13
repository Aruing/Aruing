// glamour Markdown 渲染：按主题 + 宽度渲染 Turn 正文与 Report。
// 渲染失败或 renderer 未就绪时降级返回原文，不阻断交互（守 #18 不丢内容）。
package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

// 按主题与宽度建 markdown renderer；主题空/auto 走 glamour AutoStyle（自动检测终端）
func newMarkdownRenderer(theme string, width int) (*glamour.TermRenderer, error) {
	opts := []glamour.TermRendererOption{glamour.WithWordWrap(width)}
	switch resolveTheme(theme) {
	case "dark":
		opts = append(opts, glamour.WithStandardStyle("dark"))
	case "light":
		opts = append(opts, glamour.WithStandardStyle("light"))
	default:
		opts = append(opts, glamour.WithAutoStyle())
	}
	return glamour.NewTermRenderer(opts...)
}

// 渲染 markdown；renderer 为 nil 或空文本或失败时返回原文
func renderMarkdown(r *glamour.TermRenderer, md string) string {
	if r == nil || strings.TrimSpace(md) == "" {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return strings.TrimRight(out, "\n")
}
