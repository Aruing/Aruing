// 行内流式视图保留已滚出的正文，只重绘仍可见的变化行；权威正文仍由会话层提交
package tui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// 当前轮次的临时渲染状态；不写消息存储，轮次结束后丢弃
type inlineStreamView struct {
	// 流式原文，仅用于重绘
	text strings.Builder
	// 上次渲染的物理行，最后一行不带换行
	lines []string
	// 仍能安全回退到的首行；整体收缩跨出可见区时随新末行重定位
	firstVisible int
	// 终端宽高，用于限制光标回退范围
	width, height int
}

// 追加片段并刷新可见区域；显示失败作为消费者错误返回上游
func (v *inlineStreamView) append(out io.Writer, st styles, md *markdownRenderer, delta string) error {
	v.text.WriteString(delta)
	return v.update(out, st.assistant.Render(renderStreamingMarkdown(md, v.text.String())))
}

// 按完整正文渲染差异，保留滚屏历史；跨屏段落的旧样式不回写不可见区域
func (v *inlineStreamView) render(out io.Writer, st styles, md *markdownRenderer, text string) error {
	return v.update(out, st.assistant.Render(renderMarkdown(md, text)))
}

// 输出物理行差异，并在成功写入后同步光标可回退范围
func (v *inlineStreamView) update(out io.Writer, rendered string) error {
	width := max(v.width-1, 1)
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
	start := max(min(common, len(lines)-1), v.firstVisible, len(v.lines)-max(v.height, 2)+1)
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
	update.WriteString(strings.Join(lines[min(start, len(lines)-1):], "\r\n"))
	if _, err := io.WriteString(out, update.String()); err != nil {
		return err
	}
	v.lines = lines
	v.firstVisible = max(min(v.firstVisible, len(lines)-1), len(lines)-max(v.height, 2)+1)
	return nil
}
