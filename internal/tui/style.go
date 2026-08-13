// TUI 样式 token 集中定义；Step 3 接 config.TUI.Theme 后这些值由配置覆盖。
// 当前用基础 ANSI 色，集中在 token 定义处（不散落硬编码），守 #20「样式经主题 token」。
package tui

import "github.com/charmbracelet/lipgloss"

// 各类消息与组件的样式 token
var (
	userStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	systemStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	spinnerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	promptStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	dividerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)
