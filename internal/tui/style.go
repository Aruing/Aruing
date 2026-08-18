// TUI 样式 token：按主题（dark/light）分两套色表；auto 用 termenv 检测终端背景。
// 集中在此文件，守 #20「样式经主题 token」；Step 3 接 config.TUI.Theme。
package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// 一套主题下的样式 token
// spacing / labels 随样式表一起加载（margin 从样式项剥离后的显式承载，见 theme.go）
type styles struct {
	user      lipgloss.Style
	assistant lipgloss.Style
	err       lipgloss.Style
	system    lipgloss.Style
	spinner   lipgloss.Style
	prompt    lipgloss.Style
	divider   lipgloss.Style
	// 块间距显式值（默认 defaultSpacing；主题 margin 声明覆盖）
	spacing spacing
	// 称呼开关与文案（默认关；开启后称呼独立一行 + 换行 + 内容）
	labels labels
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

// 按配置主题加载样式表；themeFile 非空时解析覆盖文件并在基底上合成
// （部分覆盖：未声明样式项回落内置；解析/校验失败返回人话错误，启动即失败不静默降级）
func loadStyles(theme, themeFile string) (styles, error) {
	ov, err := loadThemeOverrides(themeFile)
	if err != nil {
		return styles{}, err
	}
	base := "light"
	if resolveTheme(theme) == "dark" {
		base = "dark"
	}
	if ov != nil && ov.base != "" {
		base = ov.base
	}
	var st styles
	if base == "dark" {
		st = darkStyles()
	} else {
		st = lightStyles()
	}
	// 间距与称呼先落默认视觉基线，再由覆盖声明逐项替换（显式 0 合法，不会被默认值顶掉）
	st.spacing = defaultSpacing()
	st.labels = labels{user: "你", assistant: "aruing"}
	return applyThemeOverrides(st, ov, themeFile)
}

// 暗色主题色表（ANSI 256 色）。基底表只存颜色/粗体；块间距解析为 spacing
// 显式值由消费点输出（margin 若进样式项，Render 块级应用会与手动空行双重叠加）
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

// 测试便利：加载失败即 Fatal（测试用；产品路径须经 loadStyles 显式处理错误）
func mustLoadStyles(theme string) styles {
	st, err := loadStyles(theme, "")
	if err != nil {
		panic(err)
	}
	return st
}
