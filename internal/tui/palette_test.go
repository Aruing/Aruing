package tui

import (
	"strings"
	"testing"
)

// 过滤基础：'/' 全量按声明序；前缀优先；子序列兜底；大小写不敏感
func TestFilterPalette(t *testing.T) {
	// 全量用例从命令表派生：新增命令不该让本测试变成需要手改的计数断言
	all := make([]string, 0, len(commandDocs))
	for _, d := range commandDocs {
		all = append(all, d.name)
	}
	cases := []struct {
		name  string
		line  string
		names []string
	}{
		{"全量", "/", all},
		{"前导空白容忍", "  /", all},
		{"前缀命中在前", "/s", []string{"sessions", "session"}},
		{"精确名也是前缀", "/session", []string{"sessions", "session"}},
		{"子序列兜底", "/xit", []string{"exit"}},
		{"大小写不敏感", "/SES", []string{"sessions", "session"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := filterPalette(tc.line)
			if len(v.items) != len(tc.names) {
				t.Fatalf("items = %d, want %d (%+v)", len(v.items), len(tc.names), v.items)
			}
			for i, want := range tc.names {
				if v.items[i].name != want {
					t.Fatalf("items[%d] = %s, want %s", i, v.items[i].name, want)
				}
			}
		})
	}
}

// 激活边界：非 / 开头、多行、无命中 → 菜单隐藏（提交走既有错误路径）
func TestFilterPaletteInactive(t *testing.T) {
	for _, tc := range []struct{ name, line string }{
		{"非命令", "hello"},
		{"斜杠在中段", "say /help"},
		{"多行草稿", "/help\nmore"},
		{"无命中", "/zzz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if filterPalette(tc.line).active() {
				t.Fatalf("filterPalette(%q) should be inactive", tc.line)
			}
		})
	}
}

// 选中移动：负向与正向都钳制在 [0, len)
func TestPaletteMoveSel(t *testing.T) {
	v := filterPalette("/")
	if v = v.moveSel(-1); v.sel != 0 {
		t.Fatalf("sel = %d, want clamp at 0", v.sel)
	}
	if v = v.moveSel(3); v.sel != 3 {
		t.Fatalf("sel = %d, want 3", v.sel)
	}
	if v = v.moveSel(9); v.sel != len(v.items)-1 {
		t.Fatalf("sel = %d, want clamp at %d", v.sel, len(v.items)-1)
	}
}

// Tab 补全：替换命令名片段、保留其余文本；需参命令补尾空格
func TestCompletePaletteLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		sel  int
		want string
	}{
		{"无参命令补全", "/ses", 0, "/sessions"},
		{"需参命令补尾空格", "/se", 1, "/session "},
		{"保留已输参数", "/ses a1", 1, "/session a1"},
		{"保留前导空白", "  /he", 0, "  /help"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := filterPalette(tc.line)
			v = v.moveSel(tc.sel)
			got, cursor, ok := completePaletteLine(tc.line, v)
			if !ok {
				t.Fatalf("completePaletteLine(%q) not ok", tc.line)
			}
			if got != tc.want {
				t.Fatalf("completed = %q, want %q", got, tc.want)
			}
			if cursor != len([]rune(got)) {
				t.Fatalf("cursor = %d, want line end %d", cursor, len([]rune(got)))
			}
		})
	}
	// 菜单隐藏时不接管
	if _, _, ok := completePaletteLine("hello", paletteView{}); ok {
		t.Fatal("inactive palette must not complete")
	}
}

// Enter 提交规则：裸命令替换为选中名；含参数原样；隐藏不接管
func TestPaletteSubmit(t *testing.T) {
	v := filterPalette("/hel")
	if got, handled := paletteSubmit("/hel", v); !handled || got != "/help" {
		t.Fatalf("paletteSubmit(/hel) = %q, %v", got, handled)
	}
	v = filterPalette("/session abc")
	if got, handled := paletteSubmit("/session abc", v); handled || got != "/session abc" {
		t.Fatalf("paletteSubmit(with args) = %q, %v, want as-is", got, handled)
	}
	if _, handled := paletteSubmit("hello", paletteView{}); handled {
		t.Fatal("inactive palette must not handle submit")
	}
}

// 菜单行：正文与 /help 同形、选中标记与对齐、末行键位自述
func TestPaletteRows(t *testing.T) {
	v := filterPalette("/")
	rows := paletteRows(v)
	if len(rows) != len(v.items)+1 {
		t.Fatalf("rows = %d, want %d", len(rows), len(v.items)+1)
	}
	if !strings.HasPrefix(rows[v.sel].text, "❯ /help") || !rows[v.sel].selected {
		t.Fatalf("selected row = %+v", rows[v.sel])
	}
	if !strings.HasPrefix(rows[1].text, "  /sessions") || rows[1].selected {
		t.Fatalf("unselected row = %+v", rows[1])
	}
	last := rows[len(rows)-1]
	if last.text != paletteKeyHint || last.selected {
		t.Fatalf("key hint row = %+v", last)
	}
}
