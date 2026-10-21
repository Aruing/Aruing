// 会话选择器内核：`/session` 无参时打开的模态列表（↑↓ 选择、Enter 切换、
// 输入即过滤）。条目来自一次 ListSessions 快照，过滤在内存里做，不逐键读盘。
// 双模式共用同一推导与展示行（行为一致由构造保证）；纯展示层——只读会话
// 发现能力与本地 UI 状态，不写 Store / RunLedger（#20），UI 语法不进 agent
// 意图枚举、模型不可见（#2）。
package tui

import (
	"fmt"
	"strings"

	"github.com/Aruing/Aruing/internal/session"
)

// 会话选择器条目：id 为切换目标，label 为与 /sessions 同形的展示正文
type sessionItem struct {
	// 会话编号（切换目标）
	id string
	// 展示正文：id + 消息数 + 最近活跃时间 + 首问预览，当前会话带 * 标记
	label string
	// 是否当前会话（选中该项视为原地不动）
	cur bool
}

// 会话列表的键位自述行（完成标志 2：键位集在面板内可发现）
const pickerKeyHint = "↑↓ 选择 · Enter 切换 · Esc 关闭"

// sessionItems 把会话汇总快照转成选择器条目；顺序沿用 ListSessions 的
// 最近活跃降序。当前会话标 * 但仍可选中（切换语义由调用方按 cur 决定）
func sessionItems(summaries []session.SessionSummary, current string) []sessionItem {
	items := make([]sessionItem, 0, len(summaries))
	for _, s := range summaries {
		mark := " "
		cur := s.ID == current
		if cur {
			mark = "*"
		}
		items = append(items, sessionItem{
			id:    s.ID,
			label: fmt.Sprintf("%s %s  %d 条消息  %s  %s", mark, s.ID, s.MessageCount, s.UpdatedAt.Format("2006-01-02 15:04"), firstQuestionPreview(s)),
			cur:   cur,
		})
	}
	return items
}

// filterSessions 按输入行过滤：空 query 返回全量；否则大小写不敏感子串匹配
// id 与展示正文（含首问预览，便于按提问找会话）。无命中返回空切片，调用方
// 据此显示空视图而不是新造错误路径
func filterSessions(items []sessionItem, query string) []sessionItem {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return items
	}
	out := make([]sessionItem, 0, len(items))
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.id), q) || strings.Contains(strings.ToLower(it.label), q) {
			out = append(out, it)
		}
	}
	return out
}

// pickerView 选择器视图：过滤后的条目与选中下标
type pickerView struct {
	// 过滤后的条目，顺序即展示顺序
	items []sessionItem
	// 选中下标，由 moveSel 维护在 [0, len(items)) 内
	sel int
}

// active 报告选择器是否有可展示条目
func (v pickerView) active() bool { return len(v.items) > 0 }

// moveSel 移动选中并钳制在 [0, len(items))（不环绕，与菜单同口径）
func (v pickerView) moveSel(delta int) pickerView {
	if !v.active() {
		return v
	}
	v.sel += delta
	if v.sel < 0 {
		v.sel = 0
	}
	if v.sel > len(v.items)-1 {
		v.sel = len(v.items) - 1
	}
	return v
}

// selected 返回当前选中条目；空视图或下标越界返回 false（防御状态漂移）
func (v pickerView) selected() (sessionItem, bool) {
	if !v.active() || v.sel < 0 || v.sel >= len(v.items) {
		return sessionItem{}, false
	}
	return v.items[v.sel], true
}

// pickerTarget Enter 的目标会话；空视图返回 false（调用方提示并关闭选择器）
func pickerTarget(v pickerView) (sessionID string, ok bool) {
	it, ok := v.selected()
	if !ok {
		return "", false
	}
	return it.id, true
}

// pickerRows 生成展示行：复用菜单的行类型与样式 token（menu / menuSelected），
// 选中行带 ❯ 前缀、未选中行两空格对齐，末行固定键位自述
func pickerRows(v pickerView) []paletteLine {
	rows := make([]paletteLine, 0, len(v.items)+1)
	for i, it := range v.items {
		text := "  " + it.label
		if i == v.sel {
			text = "❯ " + it.label
		}
		rows = append(rows, paletteLine{text: text, selected: i == v.sel})
	}
	rows = append(rows, paletteLine{text: pickerKeyHint})
	return rows
}
