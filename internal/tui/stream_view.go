// 行内流式视图保留已滚出的正文，只重绘仍可见的变化行；权威正文仍由会话层提交
package tui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// 当前轮次的临时渲染状态；不写消息存储，轮次结束后丢弃
type inlineStreamView struct {
	// 流式原文，仅用于重绘
	text strings.Builder
	// 上次渲染的物理行，最后一行不带换行
	lines []string
	// 终端宽高，用于限制光标回退范围
	width, height int
}

// 追加片段并刷新可见区域；显示失败作为消费者错误返回上游
func (v *inlineStreamView) append(out io.Writer, st styles, md *glamour.TermRenderer, delta string) error {
	v.text.WriteString(delta)
	return v.render(out, st, md, v.text.String())
}

// 按完整正文渲染差异，保留滚屏历史；跨屏段落的旧样式不回写不可见区域
func (v *inlineStreamView) render(out io.Writer, st styles, md *glamour.TermRenderer, text string) error {
	width := max(v.width-1, 1)
	rendered := st.assistant.Render(renderMarkdown(md, text))
	lines := strings.Split(lipgloss.NewStyle().Width(width).Render(rendered), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	common := 0
	for common < len(v.lines) && common < len(lines) && v.lines[common] == lines[common] {
		common++
	}
	if common == len(v.lines) && common == len(lines) {
		return nil
	}
	// 不回退到屏外，以免覆盖本轮前的进度或用户消息
	start := max(common, len(v.lines)-max(v.height, 2)+1)
	var update strings.Builder
	if len(v.lines) > 0 {
		if common == len(v.lines) {
			update.WriteString("\r\n")
		} else {
			up := len(v.lines) - 1 - start
			if up > 0 {
				fmt.Fprintf(&update, "\x1b[%dA", up)
			}
			update.WriteString("\r\x1b[J")
		}
	}
	update.WriteString(strings.Join(lines[min(start, len(lines)):], "\r\n"))
	if _, err := io.WriteString(out, update.String()); err != nil {
		return err
	}
	v.lines = lines
	return nil
}
