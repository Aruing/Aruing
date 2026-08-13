// TUI 样式 token：按主题（dark/light）分两套色表；auto 用 termenv 检测终端背景。
// 集中在此文件，守 #20「样式经主题 token」；Step 3 接 config.TUI.Theme。
package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// 一套主题下的样式 token
type styles struct {
	user      lipgloss.Style
	assistant lipgloss.Style
	err       lipgloss.Style
	system    lipgloss.Style
	spinner   lipgloss.Style
	prompt    lipgloss.Style
	divider   lipgloss.Style
}

// 把主题名归一到 dark/light；空 / auto / 未知用 termenv 检测终端背景
func resolveTheme(theme string) string {
	switch theme {
	case "dark", "light":
		return theme
	default:
		if termenv.HasDarkBackground() {
			return "dark"
		}
		return "light"
	}
}

// 按配置主题加载样式色表
func loadStyles(theme string) styles {
	if resolveTheme(theme) == "dark" {
		return darkStyles()
	}
	return lightStyles()
}

// 暗色主题色表（ANSI 256 色）
func darkStyles() styles {
	return styles{
		user:      lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true),
		assistant: lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		err:       lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true),
		system:    lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		spinner:   lipgloss.NewStyle().Foreground(lipgloss.Color("212")),
		prompt:    lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true),
		divider:   lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
	}
}

// 亮色主题色表
func lightStyles() styles {
	return styles{
		user:      lipgloss.NewStyle().Foreground(lipgloss.Color("27")).Bold(true),
		assistant: lipgloss.NewStyle().Foreground(lipgloss.Color("238")),
		err:       lipgloss.NewStyle().Foreground(lipgloss.Color("124")).Bold(true),
		system:    lipgloss.NewStyle().Foreground(lipgloss.Color("242")),
		spinner:   lipgloss.NewStyle().Foreground(lipgloss.Color("99")),
		prompt:    lipgloss.NewStyle().Foreground(lipgloss.Color("27")).Bold(true),
		divider:   lipgloss.NewStyle().Foreground(lipgloss.Color("250")),
	}
}
