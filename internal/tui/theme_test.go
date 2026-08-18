package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// 写临时主题文件
func writeTheme(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "theme.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write theme: %v", err)
	}
	return path
}

// 声明一个样式项一个字段：只该字段覆盖，其余回落基底
func TestThemeOverridePartial(t *testing.T) {
	path := writeTheme(t, `
styles:
  user:
    foreground: "200"
`)
	st, err := loadStyles("dark", path)
	if err != nil {
		t.Fatalf("loadStyles: %v", err)
	}
	// user 前景被覆盖（200 ≠ 基底 39）
	if got, ok := st.user.GetForeground().(lipgloss.Color); !ok || string(got) != "200" {
		t.Fatalf("user foreground not overridden: %v", got)
	}
	// 其余角色不受影响：assistant 仍为基底 252
	if got, ok := st.assistant.GetForeground().(lipgloss.Color); !ok || string(got) != "252" {
		t.Fatalf("assistant foreground should stay base: %v", got)
	}
	// user 粗体（基底自带）未声明不丢
	if !st.user.GetBold() {
		t.Fatal("user bold dropped by partial override")
	}
}

// base 显式指定优先于 tui.theme：dark 基底 + tui.theme=light → 用 dark
func TestThemeOverrideBase(t *testing.T) {
	path := writeTheme(t, `
base: dark
styles:
  assistant:
    foreground: "200"
`)
	st, err := loadStyles("light", path)
	if err != nil {
		t.Fatalf("loadStyles: %v", err)
	}
	// 基底 dark：user 是 39（dark 值）而非 27（light 值）
	if got, ok := st.user.GetForeground().(lipgloss.Color); !ok || string(got) != "39" {
		t.Fatalf("base=dark not applied: %v", got)
	}
}

// bold=false 显式关粗体（指针区分未声明与显式 false）
func TestThemeOverrideBoldOff(t *testing.T) {
	path := writeTheme(t, `
styles:
  user:
    bold: false
`)
	st, err := loadStyles("dark", path)
	if err != nil {
		t.Fatalf("loadStyles: %v", err)
	}
	if st.user.GetBold() {
		t.Fatal("explicit bold:false ignored")
	}
}

// margin 覆盖语义：声明解析为 spacing 显式值，不再写 lipgloss 样式项
// （Render 会按块级应用 margin，与渲染点手动空行双重叠加；显式 0 也须与未配置可区分）
func TestThemeOverrideSpacing(t *testing.T) {
	path := writeTheme(t, `
styles:
  divider:
    margin: [2, 0, 3, 0]
  user:
    margin: [2, 0, 0, 0]
  assistant:
    margin: [0, 0, 2, 0]
`)
	st, err := loadStyles("dark", path)
	if err != nil {
		t.Fatalf("loadStyles: %v", err)
	}
	if st.spacing.userTop != 2 {
		t.Fatalf("userTop = %d, want 2", st.spacing.userTop)
	}
	// assistant 显式 0 合法：与未配置（默认 1）可区分
	if st.spacing.assistantTop != 0 || st.spacing.assistantBottom != 2 {
		t.Fatalf("assistant spacing = %d/%d, want 0/2", st.spacing.assistantTop, st.spacing.assistantBottom)
	}
	if st.spacing.dividerTop != 2 || st.spacing.dividerBottom != 3 {
		t.Fatalf("divider spacing = %d/%d, want 2/3", st.spacing.dividerTop, st.spacing.dividerBottom)
	}
	// margin 不进 lipgloss 样式项：避免 Render 块级应用与手动空行双重叠加
	if st.divider.GetMarginTop() != 0 || st.divider.GetMarginBottom() != 0 {
		t.Fatal("margin must not be applied to lipgloss styles")
	}
}

// 未声明 margin：默认视觉基线（输入上 1、助手块上 1 下 0、分割线上 1 下 0）
func TestSpacingDefaults(t *testing.T) {
	st, err := loadStyles("dark", "")
	if err != nil {
		t.Fatal(err)
	}
	want := spacing{userTop: 1, assistantTop: 1, assistantBottom: 0, dividerTop: 1, dividerBottom: 0}
	if st.spacing != want {
		t.Fatalf("default spacing = %+v, want %+v", st.spacing, want)
	}
}

// 无间距消费点的角色声明 margin：启动报错（防误解后静默无效）
func TestMarginUnsupportedRole(t *testing.T) {
	path := writeTheme(t, `
styles:
  err:
    margin: [1, 0, 0, 0]
`)
	if _, err := loadStyles("dark", path); err == nil || !strings.Contains(err.Error(), "styles.err.margin") {
		t.Fatalf("want error for err.margin, got: %v", err)
	}
}

// 称呼开关：默认关；enabled + 文案覆盖；文案留空回落默认；enabled 缺省为关
func TestLabelsOverride(t *testing.T) {
	// 默认：关 + 默认文案
	st, err := loadStyles("dark", "")
	if err != nil {
		t.Fatal(err)
	}
	if st.labels.enabled || st.labels.user != "你" || st.labels.assistant != "aruing" {
		t.Fatalf("default labels = %+v", st.labels)
	}
	// 开启 + 自定义文案
	path := writeTheme(t, `
labels:
  enabled: true
  user: "me"
  assistant: "bot"
`)
	st, err = loadStyles("dark", path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.labels.enabled || st.labels.user != "me" || st.labels.assistant != "bot" {
		t.Fatalf("labels = %+v", st.labels)
	}
	// 开启但文案留空：回落默认 你 / aruing
	path = writeTheme(t, `
labels:
  enabled: true
`)
	st, err = loadStyles("dark", path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.labels.enabled || st.labels.user != "你" || st.labels.assistant != "aruing" {
		t.Fatalf("labels fallback = %+v", st.labels)
	}
	// 声明 labels 但未写 enabled：视为关
	path = writeTheme(t, `
labels:
  user: "me"
`)
	st, err = loadStyles("dark", path)
	if err != nil {
		t.Fatal(err)
	}
	if st.labels.enabled {
		t.Fatal("labels without enabled must stay off")
	}
	// enabled 非布尔：YAML 解析报错
	path = writeTheme(t, "labels:\n  enabled: yes-please\n")
	if _, err := loadStyles("dark", path); err == nil {
		t.Fatal("invalid labels type should error")
	}
}

// border 声明后可渲染不 panic
func TestThemeOverrideBorder(t *testing.T) {
	path := writeTheme(t, `
styles:
  assistant:
    border:
      color: "240"
      rounded: true
`)
	st, err := loadStyles("dark", path)
	if err != nil {
		t.Fatalf("loadStyles: %v", err)
	}
	out := st.assistant.Render("hello")
	if !strings.Contains(out, "hello") {
		t.Fatalf("bordered render lost content: %q", out)
	}
}

// 空 styles / 空文件 = 纯基底（等价现状）；空路径无覆盖
func TestThemeOverrideEmpty(t *testing.T) {
	for _, content := range []string{"", "\nstyles: {}\n"} {
		path := writeTheme(t, content)
		st, err := loadStyles("dark", path)
		if err != nil {
			t.Fatalf("loadStyles(%q): %v", content, err)
		}
		if got, ok := st.user.GetForeground().(lipgloss.Color); !ok || string(got) != "39" {
			t.Fatalf("empty override changed base: %v", got)
		}
	}
	// 未配置路径：等价现状（原两参调用传空）
	st, err := loadStyles("dark", "")
	if err != nil || st.user.GetForeground() == nil {
		t.Fatalf("no theme file must be zero-change: %+v err=%v", st, err)
	}
}

// 非法输入启动即报错且含定位信息：未知角色 / 坏颜色 / 负间距 / 错基数
func TestThemeOverrideInvalid(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"unknown role", "styles:\n  userr:\n    foreground: \"1\"\n", "unknown style role"},
		{"bad hex", "styles:\n  user:\n    foreground: \"#zzzzzz\"\n", "invalid color"},
		{"bad number", "styles:\n  user:\n    foreground: \"999\"\n", "invalid color"},
		{"negative margin", "styles:\n  user:\n    margin: [-1, 0, 0, 0]\n", "must be >= 0"},
		{"wrong arity", "styles:\n  user:\n    margin: [1, 2]\n", "want 4 values"},
		{"bad base", "base: solarized\nstyles: {}\n", "base must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTheme(t, tc.content)
			_, err := loadStyles("dark", path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got: %v", tc.want, err)
			}
		})
	}
}

// 基底表回归：dark/light 关键值不被本变更移动
func TestThemeBaseTables(t *testing.T) {
	dark, err := loadStyles("dark", "")
	if err != nil {
		t.Fatal(err)
	}
	light, err := loadStyles("light", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := dark.assistant.GetForeground().(lipgloss.Color); !ok || string(got) != "252" {
		t.Fatalf("dark assistant: %v", got)
	}
	if got, ok := light.assistant.GetForeground().(lipgloss.Color); !ok || string(got) != "238" {
		t.Fatalf("light assistant: %v", got)
	}
	// 间距不进基底表：margin 解析为 spacing 显式值由消费点输出，
	// 未配置时默认见 defaultSpacing（TestSpacingDefaults 锚定）
}
