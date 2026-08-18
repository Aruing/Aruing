// 主题覆盖文件（YAML）解析与覆盖合成：基于内置 dark/light 基底做部分覆盖，
// 未声明的样式项回落内置（不逼全表，#18）；非法值启动即明确报错。
// 样式项即「有名字的样式槽位」（user 的前景色、assistant 的上边距等），
// 守 #20「样式经主题，代码不硬编码」。
package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
	"gopkg.in/yaml.v3"
)

// 主题覆盖文件结构（全部可选；顶层 styles 键的键名为语义角色名）
type themeFile struct {
	// 基底选择：dark | light；空 = 取 tui.theme 归一结果
	Base string `yaml:"base"`
	// 称呼开关与文案（可选；默认关，开启后称呼独立一行 + 换行 + 内容）
	Labels *labelsSpec `yaml:"labels"`
	// 各语义角色的样式覆盖（只覆盖声明的字段）
	Styles map[string]styleSpec `yaml:"styles"`
}

// 称呼声明：enabled 缺省视为关；文案留空回落默认（你 / aruing）
type labelsSpec struct {
	// 显式开关称呼行；未声明 = 关
	Enabled *bool `yaml:"enabled"`
	// 用户消息称呼文案；空 = 默认「你」
	User string `yaml:"user"`
	// 助手消息称呼文案；空 = 默认「aruing」
	Assistant string `yaml:"assistant"`
}

// 块间距的显式值（行数）：主题 margin 声明解析而来，未声明的项用默认视觉基线。
// margin 不写进 lipgloss 样式项：Render 会按块级应用 margin，与渲染调用点的
// 手动空行叠加会双倍；且显式 0 也是合法间距，须与「未配置」区分，故单独承载
type spacing struct {
	// 用户输入块上方空行数（默认 1）
	userTop int
	// 用户输入与助手块（spinner / 回复 / 错误）之间空行数（默认 1）
	assistantTop int
	// 助手块下方空行数（默认 0）
	assistantBottom int
	// 轮间分割线上方空行数（默认 1）
	dividerTop int
	// 轮间分割线下方空行数（默认 0；与下轮输入之间的空行由 userTop 提供，避免双空行）
	dividerBottom int
}

// 默认块间距（视觉基线）：输入与回复之间恰 1 行、分割线两侧各恰 1 行
func defaultSpacing() spacing {
	return spacing{userTop: 1, assistantTop: 1, assistantBottom: 0, dividerTop: 1, dividerBottom: 0}
}

// 称呼配置的解析后形态：开关 + 两角色文案；渲染点消费（默认关）
type labels struct {
	// 是否输出称呼行（独立一行 + 换行 + 内容）
	enabled bool
	// 用户消息称呼文案
	user string
	// 助手消息称呼文案
	assistant string
}

// 归一称呼声明：文案留空回落默认，enabled 缺省为关
func normalizeLabels(spec *labelsSpec) labels {
	out := labels{enabled: spec.Enabled != nil && *spec.Enabled, user: "你", assistant: "aruing"}
	if strings.TrimSpace(spec.User) != "" {
		out.user = spec.User
	}
	if strings.TrimSpace(spec.Assistant) != "" {
		out.assistant = spec.Assistant
	}
	return out
}

// 单个角色的样式覆盖声明；指针与切片区分「未声明」与「显式零值」
type styleSpec struct {
	// 前景颜色：ANSI 256 编号（如 "205"）或 hex（如 "#ff005f"）
	Foreground string `yaml:"foreground"`
	// 背景颜色，格式同前景
	Background string `yaml:"background"`
	// 显式开关粗体；未声明不改动基底
	Bold *bool `yaml:"bold"`
	// 边框样式；未声明不改动基底
	Border *borderSpec `yaml:"border"`
	// 内边距 [上, 右, 下, 左]；非负；未声明不改动基底
	Padding []int `yaml:"padding"`
	// 外边距 [上, 右, 下, 左]；非负；仅 user / assistant / divider 消费（解析为块间距
	// spacing，左右值当前无消费点）；未声明不改动默认间距
	Margin []int `yaml:"margin"`
}

// 边框样式覆盖
type borderSpec struct {
	// 边框颜色，格式同前景
	Color string `yaml:"color"`
	// 是否圆角（true 正常边框带圆角；false 直角）；当前 UI 未启用边框，供未来组件消费
	Rounded bool `yaml:"rounded"`
}

// 覆盖指令：解析并校验后的中间形态，applyThemeOverrides 消费
type themeOverrides struct {
	// 基底（dark | light，已归一）
	base string
	// 称呼声明（可为 nil）
	labels *labelsSpec
	// 角色名 → 已校验的覆盖声明
	styles map[string]styleSpec
}

// 已知语义角色名（与 styles 结构字段一一对应；未知角色名启动报错防拼写错静默无效）
var knownStyleRoles = map[string]struct{}{
	"user": {}, "assistant": {}, "err": {}, "system": {},
	"spinner": {}, "prompt": {}, "divider": {},
}

// 读并解析主题覆盖文件：文件不存在/格式错/未知角色/非法值都返回带定位信息的人话错误
func loadThemeOverrides(path string) (*themeOverrides, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read theme file %s: %w", path, err)
	}
	var tf themeFile
	if err := yaml.Unmarshal(data, &tf); err != nil {
		return nil, fmt.Errorf("parse theme file %s: %w", path, err)
	}
	ov := &themeOverrides{base: strings.TrimSpace(tf.Base), labels: tf.Labels, styles: tf.Styles}
	if ov.base != "" && ov.base != "dark" && ov.base != "light" {
		return nil, fmt.Errorf("theme file %s: base must be dark or light, got %q", path, ov.base)
	}
	for role, spec := range ov.styles {
		if _, ok := knownStyleRoles[role]; !ok {
			return nil, fmt.Errorf("theme file %s: unknown style role %q (known: user, assistant, err, system, spinner, prompt, divider)", path, role)
		}
		if err := validateStyleSpec(path, role, spec); err != nil {
			return nil, err
		}
	}
	return ov, nil
}

// 校验单个角色覆盖：颜色可解析、边距四元非负
func validateStyleSpec(path, role string, spec styleSpec) error {
	for name, color := range map[string]string{
		"foreground": spec.Foreground,
		"background": spec.Background,
	} {
		if color == "" {
			continue
		}
		if !validColor(color) {
			return fmt.Errorf("theme file %s: styles.%s.%s: invalid color %q (use ANSI number like \"205\" or hex like \"#ff005f\")", path, role, name, color)
		}
	}
	if spec.Border != nil && spec.Border.Color != "" && !validColor(spec.Border.Color) {
		return fmt.Errorf("theme file %s: styles.%s.border.color: invalid color %q", path, role, spec.Border.Color)
	}
	for name, quad := range map[string][]int{
		"padding": spec.Padding,
		"margin":  spec.Margin,
	} {
		if quad == nil {
			continue
		}
		if len(quad) != 4 {
			return fmt.Errorf("theme file %s: styles.%s.%s: want 4 values [top, right, bottom, left], got %d", path, role, name, len(quad))
		}
		for _, v := range quad {
			if v < 0 {
				return fmt.Errorf("theme file %s: styles.%s.%s: values must be >= 0, got %d", path, role, name, v)
			}
		}
	}
	// margin 语义是块间距，只有 user / assistant / divider 有消费点；
	// 其余角色声明即报错，防拼写/误解后静默无效
	if spec.Margin != nil {
		switch role {
		case "user", "assistant", "divider":
		default:
			return fmt.Errorf("theme file %s: styles.%s.margin: only user/assistant/divider consume spacing (move it or drop it)", path, role)
		}
	}
	return nil
}

// 颜色合法性：hex 须可解析（go-colorful，与 termenv 运行时转换同源）、
// 纯编号在 0–255；防拼写错静默渲染为无色
func validColor(color string) bool {
	if strings.HasPrefix(color, "#") {
		_, err := colorful.Hex(color)
		return err == nil
	}
	if n, err := strconv.Atoi(color); err == nil {
		return n >= 0 && n <= 255
	}
	return false
}

// 把覆盖应用到基底样式表：逐角色逐字段覆盖，未声明字段保持基底
func applyThemeOverrides(base styles, ov *themeOverrides, path string) (styles, error) {
	if ov == nil {
		return base, nil
	}
	out := base
	set := func(role string, mutate func(*lipgloss.Style)) {
		switch role {
		case "user":
			mutate(&out.user)
		case "assistant":
			mutate(&out.assistant)
		case "err":
			mutate(&out.err)
		case "system":
			mutate(&out.system)
		case "spinner":
			mutate(&out.spinner)
		case "prompt":
			mutate(&out.prompt)
		case "divider":
			mutate(&out.divider)
		}
	}
	for role, spec := range ov.styles {
		// margin 解析为 spacing 显式值（不写 lipgloss 样式项，见 spacing 注释）；
		// 左右值当前无消费点，忽略
		if len(spec.Margin) == 4 {
			switch role {
			case "user":
				out.spacing.userTop = spec.Margin[0]
			case "assistant":
				out.spacing.assistantTop = spec.Margin[0]
				out.spacing.assistantBottom = spec.Margin[2]
			case "divider":
				out.spacing.dividerTop = spec.Margin[0]
				out.spacing.dividerBottom = spec.Margin[2]
			}
		}
		set(role, func(s *lipgloss.Style) {
			if spec.Foreground != "" {
				*s = s.Foreground(lipgloss.Color(spec.Foreground))
			}
			if spec.Background != "" {
				*s = s.Background(lipgloss.Color(spec.Background))
			}
			if spec.Bold != nil {
				*s = s.Bold(*spec.Bold)
			}
			if spec.Border != nil {
				border := lipgloss.NormalBorder()
				if spec.Border.Rounded {
					border = lipgloss.RoundedBorder()
				}
				*s = s.Border(border)
				if spec.Border.Color != "" {
					*s = s.BorderForeground(lipgloss.Color(spec.Border.Color))
				}
			}
			if len(spec.Padding) == 4 {
				*s = s.Padding(spec.Padding[0], spec.Padding[1], spec.Padding[2], spec.Padding[3])
			}
		})
	}
	// 称呼声明在样式之后归一（文案回落默认 / enabled 缺省为关）
	if ov.labels != nil {
		out.labels = normalizeLabels(ov.labels)
	}
	_ = path // 定位信息已在解析阶段校验并报错，此处无额外用途
	return out, nil
}
