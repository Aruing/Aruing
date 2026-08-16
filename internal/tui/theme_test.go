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

// margin/padding 覆盖生效
func TestThemeOverrideSpacing(t *testing.T) {
	path := writeTheme(t, `
styles:
  divider:
    margin: [2, 0, 3, 0]
`)
	st, err := loadStyles("dark", path)
	if err != nil {
		t.Fatalf("loadStyles: %v", err)
	}
	if got := st.divider.GetMarginTop(); got != 2 || st.divider.GetMarginBottom() != 3 {
		t.Fatalf("divider margin override: top=%d bottom=%d", got, st.divider.GetMarginBottom())
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
	// 间距不再进基底表：消费点显式读 margin 配置，未配置时 fallback 默认行数
	// （视觉基线由消费点保证，见 renderMessageDivider / echoUserMessage / waitTurn）
}
