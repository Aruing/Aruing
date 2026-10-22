package tui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/ergochat/readline"
	"github.com/muesli/termenv"
)

// 快捷构造：已按行激活菜单的 ctl（画过一次，rows 已记账）
func newVisibleCtl(t *testing.T, line string) (*paletteCtl, *bytes.Buffer) {
	t.Helper()
	c := &paletteCtl{}
	var b bytes.Buffer
	c.onLine(&b, mustLoadStyles("dark"), []rune(line), len(line))
	if !c.visible() {
		t.Fatalf("line %q should activate the menu", line)
	}
	return c, &b
}

// onLine 主路径：激活全量、过滤收窄、无命中隐藏擦除、行值变化清 Esc 抑制、
// 初始回调清软换行残迹
func TestPaletteCtlOnLine(t *testing.T) {
	st := mustLoadStyles("dark")

	// 激活：/ 出全量菜单（含键位自述行），rows = 命令数 + 1
	c, b := newVisibleCtl(t, "/")
	if !strings.Contains(b.String(), "/help") || !strings.Contains(b.String(), paletteKeyHint) {
		t.Fatalf("menu output missing entries or key hint: %q", b.String())
	}
	if c.rows != len(commandDocs)+1 {
		t.Fatalf("rows = %d, want %d", c.rows, len(commandDocs)+1)
	}

	// 过滤收窄：/se 只剩 sessions/session，rows 跟随收缩
	b.Reset()
	c.onLine(b, st, []rune("/se"), 3)
	if c.rows != 3 {
		t.Fatalf("narrowed rows = %d, want 3", c.rows)
	}

	// 无命中隐藏：输出为纯擦除（清行序列），rows 归零
	b.Reset()
	c.onLine(b, st, []rune("/zz"), 3)
	if c.visible() || c.rows != 0 {
		t.Fatalf("no-match should hide: visible=%v rows=%d", c.visible(), c.rows)
	}
	if out := b.String(); !strings.Contains(out, "\x1b[2K") || strings.Contains(out, "/help") {
		t.Fatalf("hide output should only erase: %q", out)
	}

	// Esc 抑制由行值变化清除：Esc 后改行内容，菜单应重新可见
	c, b = newVisibleCtl(t, "/se")
	c.applyIntent(b, st, []rune("/se"), 3, intentEsc)
	if c.visible() {
		t.Fatal("esc should suppress the menu")
	}
	b.Reset()
	c.onLine(b, st, []rune("/ses"), 4)
	if !c.visible() {
		t.Fatal("line change should clear esc suppression")
	}

	// 初始回调（nil 行）：软换行后残迹清到屏尾，状态复位
	if c.rows == 0 {
		t.Fatal("precondition: menu rows recorded")
	}
	b.Reset()
	c.onLine(b, st, nil, 0)
	if c.rows != 0 || c.visible() {
		t.Fatalf("initial callback should reset: rows=%d visible=%v", c.rows, c.visible())
	}
	if out := b.String(); !strings.Contains(out, "\x1b[J") {
		t.Fatalf("initial callback should clear to screen end: %q", out)
	}

	// 初始回调无残迹时零输出（首轮会话常见路径）
	b.Reset()
	c.onLine(b, st, nil, 0)
	if b.Len() != 0 {
		t.Fatalf("no-op initial callback should write nothing, got %q", b.String())
	}
}

// 意图队列 FIFO：混合连击（一次读入 ↑↓）不乱序；空队列安全
func TestPaletteCtlIntentQueue(t *testing.T) {
	c := &paletteCtl{}
	c.pushIntent(intentUp)
	c.pushIntent(intentDown)
	for _, want := range []paletteIntent{intentUp, intentDown} {
		got, ok := c.popIntent()
		if !ok || got != want {
			t.Fatalf("popIntent() = (%v, %v), want (%v, true)", got, ok, want)
		}
	}
	if _, ok := c.popIntent(); ok {
		t.Fatal("empty queue should pop false")
	}
}

// 注入键意图：↑↓ 移动选中、Esc 擦除、redraw 按当前行重算
func TestPaletteCtlApplyIntent(t *testing.T) {
	st := mustLoadStyles("dark")
	c, b := newVisibleCtl(t, "/se")
	// /se 过滤序 = sessions, session（声明序）
	first, _ := c.view.selectedDoc()
	if first.name != "sessions" {
		t.Fatalf("initial selection = %q, want sessions", first.name)
	}

	c.applyIntent(b, st, []rune("/se"), 3, intentDown)
	if doc, _ := c.view.selectedDoc(); doc.name != "session" {
		t.Fatalf("after down = %q, want session", doc.name)
	}
	c.applyIntent(b, st, []rune("/se"), 3, intentUp)
	if doc, _ := c.view.selectedDoc(); doc.name != "sessions" {
		t.Fatalf("after up = %q, want sessions", doc.name)
	}

	// Esc 擦除：rows 归零、不可见
	b.Reset()
	c.applyIntent(b, st, []rune("/se"), 3, intentEsc)
	if c.visible() || c.rows != 0 {
		t.Fatalf("esc should erase: visible=%v rows=%d", c.visible(), c.rows)
	}

	// redraw：按补全后的行重算（/sessions 过滤命中 sessions）
	c.applyIntent(b, st, []rune("/sessions"), 9, intentRedraw)
	if !c.visible() {
		t.Fatal("redraw intent should repaint the menu")
	}
	if doc, _ := c.view.selectedDoc(); doc.name != "sessions" {
		t.Fatalf("redraw selection = %q, want sessions", doc.name)
	}
}

// Tab 补全：剥 readline 刚插入的 '\t' 后调内核；含参命令补尾空格；
// 菜单不可见时 ok=false 保留既有插字符行为
func TestPaletteCtlTab(t *testing.T) {
	c, _ := newVisibleCtl(t, "/se")
	// 选中 sessions（无参）：剥光标前刚插入的 \t 后补全为规范名，无尾空格
	// （pos 为插入后的光标位，\t 恰在其前）
	line, pos, ok := c.tab([]rune("/se\t"), 4)
	if !ok || string(line) != "/sessions" || pos != len("/sessions") {
		t.Fatalf("tab = (%q, %d, %v), want (/sessions, %d, true)", line, pos, ok, len("/sessions"))
	}

	// 选中 session（有参）：先 ↓ 再 Tab，补尾空格引导输参
	c.applyIntent(bytes.NewBuffer(nil), mustLoadStyles("dark"), []rune("/se"), 3, intentDown)
	line, pos, ok = c.tab([]rune("/se\t"), 4)
	if !ok || string(line) != "/session " || pos != len("/session ") {
		t.Fatalf("tab with args = (%q, %d, %v), want (/session\\x20, %d, true)", line, pos, ok, len("/session "))
	}

	// 已在输参：保留既有参数部分（光标行尾插入 \t）
	line, _, ok = c.tab([]rune("/se abc\t"), 8)
	if !ok || string(line) != "/session abc" {
		t.Fatalf("tab keep args = (%q, %v), want (/session abc, true)", line, ok)
	}

	// 菜单不可见：不改写
	c2 := &paletteCtl{}
	if _, _, ok := c2.tab([]rune("hi\t"), 3); ok {
		t.Fatal("invisible menu should not complete")
	}
}

// 重绘几何：锚点下移序列必须在首次清行之前发出（否则菜单画到输入行上、
// 光标还原错位）；下移用行馈、上移还原与之对称。回归钉住 pr-agent 发现的
// 缺失下移序列缺陷
func TestPaletteRepaintGeometry(t *testing.T) {
	st := mustLoadStyles("dark")
	c := &paletteCtl{}
	var b bytes.Buffer
	c.onLine(&b, st, []rune("/"), 1)
	out := b.String()

	// 光标行尾 → 先清行尾，再下移 1 行到锚点，才出现首次清行
	if !strings.HasPrefix(out, "\x1b[K\n\r\x1b[2K") {
		t.Fatalf("down-move must precede the first clear: %q", out[:min(24, len(out))])
	}
	// 下移与上移对称：菜单行数随命令表变化（全量命令 + 键位行），还原行数同值
	// prompt 2 列 + 光标前 1 列
	menuRows := len(commandDocs) + 1
	wantTail := fmt.Sprintf("\x1b[%dA\r\x1b[%dC", menuRows, promptCols+1)
	if !strings.HasSuffix(out, wantTail) {
		t.Fatalf("restore tail = ..., want suffix %q in %q", wantTail, out)
	}
	// 隐藏擦除同样先下移到锚点再清行
	b.Reset()
	c.onLine(&b, st, []rune("/zz"), 3)
	if !strings.HasPrefix(b.String(), "\x1b[K\n\r\x1b[2K") {
		t.Fatalf("hide sweep must anchor below the input line: %q", b.String()[:min(24, b.Len())])
	}

	// 折行续行上的光标列还原：余数列而非全宽。带参长行（/session + 91 字符
	// 参数，光标行尾）前缀 102 列、宽 80 → 光标在续行 22 列；菜单命中 session
	// 单条 + 键位行，上移 = down(1) + sweep(2) - 1
	c2 := &paletteCtl{}
	var b2 bytes.Buffer
	c2.onLine(&b2, st, []rune("/session "+strings.Repeat("a", 91)), 100)
	out2 := b2.String()
	wantTail2 := fmt.Sprintf("\x1b[%dA\r\x1b[%dC", 3, 22)
	if !strings.HasSuffix(out2, wantTail2) {
		t.Fatalf("wrapped restore tail = ..., want suffix %q in %q", wantTail2, out2)
	}
}

// cursorColOnRow：列位余数与行末悬挂边界的纯函数钉板
func TestCursorColOnRow(t *testing.T) {
	cases := []struct {
		prefixCols, width, want int
	}{
		{3, 80, 3},   // 单行短前缀：列即前缀宽
		{80, 80, 80}, // 恰为整数倍：行末悬挂位（CUF 钳边，既有容忍）
		{102, 80, 22},
		{161, 80, 1},
	}
	for _, c := range cases {
		if got := cursorColOnRow(c.prefixCols, c.width); got != c.want {
			t.Fatalf("cursorColOnRow(%d, %d) = %d, want %d", c.prefixCols, c.width, got, c.want)
		}
	}
}

// 擦除路径必须复位视图：否则下个提示符 visible() 恒真，包装层劫持 ↑↓
// 历史导航（提交路径 clearBelow 清屏但不复位的回归）
func TestEraseResetsView(t *testing.T) {
	st := mustLoadStyles("dark")
	var b bytes.Buffer

	// clearBelow（提交路径）
	c, _ := newVisibleCtl(t, "/")
	c.clearBelow(&b)
	if c.visible() {
		t.Fatal("clearBelow must reset the stale view")
	}

	// 初始回调（rows 已被 clearBelow 清零，复位不能藏在 if 里）
	c2, _ := newVisibleCtl(t, "/")
	c2.rows = 0
	c2.onLine(&b, st, nil, 0)
	if c2.visible() {
		t.Fatal("initial callback must reset the view regardless of rows")
	}

	// 复位后包装层不再拦截 ↑（归还历史导航）
	n := &newlineKeyReader{ctl: c2}
	if got := n.translateMenuKeys([]byte("\x1b[A")); string(got) != "\x1b[A" {
		t.Fatalf("arrow should pass through after reset, got %q", got)
	}
}

// 锚定几何：菜单锚在输入显示末行之下——折行 + 光标行中时下移量补足，
// 不与输入显示重叠（评审裁决 2）
func TestAnchorDownMoves(t *testing.T) {
	// 短行光标行尾：下移 1 行即输入末行之下
	if got := anchorDownMoves("/hello", 6, 80); got != 1 {
		t.Fatalf("short line anchor = %d, want 1", got)
	}
	// 100 列输入折成 2 行、光标在第 1 行中部：须下移 2 行才到末行之下
	long := "/" + strings.Repeat("a", 99)
	if got := anchorDownMoves(long, 50, 80); got != 2 {
		t.Fatalf("wrapped anchor = %d, want 2", got)
	}
	// 同一行光标行尾：下移 1
	if got := anchorDownMoves(long, 100, 80); got != 1 {
		t.Fatalf("wrapped end anchor = %d, want 1", got)
	}
}

// clearBelow 提交路径擦除：从当前行向下清 rows+1 行并回到起始行
func TestPaletteCtlClearBelow(t *testing.T) {
	c, _ := newVisibleCtl(t, "/")
	rows := c.rows
	var b bytes.Buffer
	c.clearBelow(&b)
	out := b.String()
	// rows+1 行各一次清行，行间下移
	if got := strings.Count(out, "\x1b[2K"); got != rows+1 {
		t.Fatalf("clearBelow cleared %d lines, want %d", got, rows+1)
	}
	if !strings.Contains(out, fmt.Sprintf("\x1b[%dA", rows)) {
		t.Fatal("clearBelow should return the cursor to its starting row")
	}
	if c.rows != 0 {
		t.Fatalf("rows = %d after clearBelow, want 0", c.rows)
	}
	// 无残留时零输出
	b.Reset()
	c.clearBelow(&b)
	if b.Len() != 0 {
		t.Fatalf("no-op clearBelow wrote %q", b.String())
	}
}

// 菜单翻译：可见时 ↑↓/孤立 Esc/Tab → 注入序列 + 意图；不可见或 ctl 为空时
// 全透传（保留历史导航 / 插字符既有行为）
func TestTranslateMenuKeys(t *testing.T) {
	visible := func() *paletteCtl {
		c, _ := newVisibleCtl(t, "/")
		return c
	}
	cases := []struct {
		name     string
		ctl      *paletteCtl
		in       string
		want     string
		intents  []paletteIntent
		noIntent bool
	}{
		{"上箭头", visible(), "\x1b[A", "\x1b[Z", []paletteIntent{intentUp}, false},
		{"下箭头", visible(), "\x1b[B", "\x1b[Z", []paletteIntent{intentDown}, false},
		{"孤立 Esc", visible(), "\x1b", "\x1b[Z", []paletteIntent{intentEsc}, false},
		{"Tab 注入重画", visible(), "\t", "\t\x1b[Z", []paletteIntent{intentRedraw}, false},
		{"混合文本与箭头", visible(), "x\x1b[Ay", "x\x1b[Zy", []paletteIntent{intentUp}, false},
		{"换行序列不被菜单段碰", visible(), "\x1b[27;2;13~", "\x1b[27;2;13~", nil, true},
		{"不可见全透传", func() *paletteCtl { return &paletteCtl{} }(), "\x1b[A\t\x1b", "\x1b[A\t\x1b", nil, true},
		{"无 ctl 全透传", nil, "\x1b[A\t\x1b", "\x1b[A\t\x1b", nil, true},
		{"批量 Esc+↑：Esc 关菜单后同块键透传", visible(), "\x1b\x1b[A", "\x1b[Z\x1b[A", []paletteIntent{intentEsc}, false},
		{"批量 Esc+↑+Tab：同块内全部不再拦截", visible(), "\x1b\x1b[A\t", "\x1b[Z\x1b[A\t", []paletteIntent{intentEsc}, false},
		{"Esc 后随普通键（alt+字母歧义）不识别为 Esc", visible(), "\x1b\t\x1b[B", "\x1b\t\x1b[Z\x1b[Z", []paletteIntent{intentRedraw, intentDown}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := &newlineKeyReader{ctl: tc.ctl}
			got := n.translateMenuKeys([]byte(tc.in))
			if string(got) != tc.want {
				t.Fatalf("translate(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if tc.noIntent {
				// ctl 为空的用例无法查队列；输出原样即证明未拦截
				if tc.ctl != nil && len(tc.ctl.pending) != 0 {
					t.Fatalf("unexpected intents queued: %v", tc.ctl.pending)
				}
				return
			}
			if len(tc.ctl.pending) != len(tc.intents) {
				t.Fatalf("intents = %v, want %v", tc.ctl.pending, tc.intents)
			}
			for i, want := range tc.intents {
				if tc.ctl.pending[i] != want {
					t.Fatalf("intents = %v, want %v", tc.ctl.pending, tc.intents)
				}
			}
		})
	}
}

// Listener 装配：普通键重算菜单、Tab 补全改写缓冲、注入键消费意图、
// 真实 shift+tab 无意图时无副作用、清屏键强制重画
func TestPaletteListener(t *testing.T) {
	st := mustLoadStyles("dark")
	c := &paletteCtl{}
	var b bytes.Buffer
	l := paletteListener(c, &b, &inlineRenderState{st: st})

	// 初始回调（nil,0,0）：视为空行，零输出
	l(nil, 0, 0)
	if b.Len() != 0 {
		t.Fatalf("initial callback wrote %q", b.String())
	}

	// 普通键：/ 激活菜单
	l([]rune("/"), 1, '/')
	if !c.visible() {
		t.Fatal("plain key should activate the menu")
	}

	// 注入键（↓ 意图）：选中下移并重绘
	c.pushIntent(intentDown)
	l([]rune("/"), 1, readline.MetaShiftTab)
	if doc, _ := c.view.selectedDoc(); doc.name != "sessions" {
		t.Fatalf("after down = %q, want sessions", doc.name)
	}

	// 真实 shift+tab（无意图）：纯 no-op，无输出、菜单不变
	before := b.Len()
	l([]rune("/"), 1, readline.MetaShiftTab)
	if b.Len() != before {
		t.Fatalf("no-intent shift+tab should write nothing, got %q", b.String()[before:])
	}

	// Tab 补全：可见时改写缓冲（返回 ok=true，行与光标行尾）
	c2, _ := newVisibleCtl(t, "/se")
	l2 := paletteListener(c2, &b, &inlineRenderState{st: st})
	line, pos, ok := l2([]rune("/se\t"), 4, '\t')
	if !ok || string(line) != "/sessions" || pos != len("/sessions") {
		t.Fatalf("listener tab = (%q, %d, %v), want (/sessions, %d, true)", line, pos, ok, len("/sessions"))
	}
	// 补全后的重画意图：按新行重算，菜单存活
	c2.pushIntent(intentRedraw)
	l2([]rune("/sessions"), 9, readline.MetaShiftTab)
	if !c2.visible() {
		t.Fatal("redraw intent after completion should keep the menu alive")
	}

	// 不可见时 Tab：不改写、不补全
	c3 := &paletteCtl{}
	l3 := paletteListener(c3, &b, &inlineRenderState{st: st})
	if line, _, ok := l3([]rune("hi\t"), 3, '\t'); ok || line != nil {
		t.Fatalf("invisible tab = (%q, %v), want (nil, false)", line, ok)
	}

	// 清屏键：强制全量重画（签名跳过优化不生效）
	c4, _ := newVisibleCtl(t, "/")
	var b4 bytes.Buffer
	l4 := paletteListener(c4, &b4, &inlineRenderState{st: st})
	l4([]rune("/"), 1, '/') // 同签名：被跳过，无输出
	if b4.Len() != 0 {
		t.Fatalf("identical repaint should be skipped, wrote %q", b4.String())
	}
	l4([]rune("/"), 1, readline.CharCtrlL)
	if b4.Len() == 0 {
		t.Fatal("ctrl+l should force a full repaint")
	}
}

// 重绘签名跳过：同内容同位置不重复输出；擦除后同形菜单必须重画
func TestPaletteRepaintSkip(t *testing.T) {
	st := mustLoadStyles("dark")
	c, b := newVisibleCtl(t, "/")

	// 光标在同一显示行内移动（行值与折行几何未变）：跳过重绘，零输出
	b.Reset()
	c.onLine(b, st, []rune("/"), 0)
	if b.Len() != 0 {
		t.Fatalf("same-signature repaint should write nothing, got %q", b.String())
	}

	// 擦除（提交路径）后同形菜单重开：签名已作废，必须重画
	c.clearBelow(bytes.NewBuffer(nil))
	b.Reset()
	c.onLine(b, st, []rune("/"), 1)
	if b.Len() == 0 || c.rows == 0 {
		t.Fatal("repaint after erase must not be skipped")
	}

	// 选中变化（↑↓）改变签名：重画
	b.Reset()
	c.applyIntent(b, st, []rune("/"), 1, intentDown)
	if b.Len() == 0 {
		t.Fatal("selection change should repaint")
	}
}

// 提交替换面：可见裸命令替换选中名、含参原样、不可见不替换、多行草稿不替换
func TestSubmitWithPalette(t *testing.T) {
	c, _ := newVisibleCtl(t, "/se") // 选中 sessions
	if got := submitWithPalette(c, "/se"); got != "/sessions" {
		t.Fatalf("bare submit = %q, want /sessions", got)
	}
	if got := submitWithPalette(c, "/se abc"); got != "/se abc" {
		t.Fatalf("args submit = %q, want unchanged", got)
	}

	// 菜单不可见：不替换（未知命令走既有错误路径）
	c2 := &paletteCtl{}
	if got := submitWithPalette(c2, "/se"); got != "/se" {
		t.Fatalf("invisible submit = %q, want unchanged", got)
	}

	// 多行草稿：软换行后视图已重置（初始回调），不替换
	c3, _ := newVisibleCtl(t, "/se")
	c3.onLine(bytes.NewBuffer(nil), mustLoadStyles("dark"), nil, 0)
	if got := submitWithPalette(c3, "/se\nabc"); got != "/se\nabc" {
		t.Fatalf("multiline submit = %q, want unchanged", got)
	}
}

// clearTail 退出路径：有菜单清到屏尾并复位视图，无菜单零输出
func TestPaletteCtlClearTail(t *testing.T) {
	c, _ := newVisibleCtl(t, "/")
	var b bytes.Buffer
	c.clearTail(&b)
	if out := b.String(); out != "\r\x1b[J" {
		t.Fatalf("clearTail = %q, want \\r\\x1b[J", out)
	}
	if c.rows != 0 {
		t.Fatalf("rows = %d after clearTail, want 0", c.rows)
	}
	if c.visible() {
		t.Fatal("clearTail must reset the view (erase/reset paired)")
	}
	b.Reset()
	c.clearTail(&b)
	if b.Len() != 0 {
		t.Fatalf("no-op clearTail wrote %q", b.String())
	}
}

// 包装层两段翻译链：菜单注入与软换行翻译在同一 Read 流内正确分流
func TestKeyReaderMenuChain(t *testing.T) {
	c, _ := newVisibleCtl(t, "/")
	n := &newlineKeyReader{r: strings.NewReader("/\x1b[A\x1b[27;2;13~\t"), ctl: c}
	var buf [256]byte
	rn, err := n.Read(buf[:])
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// / 透传；↑ → 注入；shift+enter 经菜单段透传后由换行段译为 \r；Tab 透传 + 注入
	want := "/\x1b[Z\r\t\x1b[Z"
	if string(buf[:rn]) != want {
		t.Fatalf("chain output = %q, want %q", buf[:rn], want)
	}
	if !n.consumeSoft() {
		t.Fatal("soft newline should be counted once")
	}
	if got, _ := c.popIntent(); got != intentUp {
		t.Fatalf("first intent = %v, want up", got)
	}
	if got, _ := c.popIntent(); got != intentRedraw {
		t.Fatalf("second intent = %v, want redraw", got)
	}
}

// clearAll：输出擦屏序列并把菜单簿记（行数 / 视图 / 锚定输入）全部复位，
// 否则下一轮重绘按旧行数清理、包装层继续劫持 ↑↓
func TestPaletteCtlClearAll(t *testing.T) {
	c, _ := newVisibleCtl(t, "/")
	var b bytes.Buffer
	c.clearAll(&b)
	if got := b.String(); got != "\x1b[3J\x1b[2J\x1b[H" {
		t.Fatalf("erase sequence = %q", got)
	}
	if c.rows != 0 || c.visible() {
		t.Fatalf("rows = %d visible = %v after clearAll", c.rows, c.visible())
	}
	// 复位后包装层不再拦截方向键（归还历史导航）
	n := &newlineKeyReader{ctl: c}
	if got := n.translateMenuKeys([]byte("\x1b[A")); string(got) != "\x1b[A" {
		t.Fatalf("arrow should pass through after clearAll, got %q", got)
	}
}

// 主题切换族回归：Listener 构造时拿不到新样式表，必须经指针读实时样式——
// 否则切主题后菜单仍按旧色表绘制（pr-agent 采纳项）。
// 测试环境默认无色：临时打开 ANSI 色档，才能从输出字节区分新旧色表
func TestPaletteListenerFollowsTheme(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	dark, light := mustLoadStyles("dark"), mustLoadStyles("light")
	rstate := &inlineRenderState{st: dark, theme: "dark", width: 80}
	c := &paletteCtl{}
	var b bytes.Buffer
	l := paletteListener(c, &b, rstate)

	// 切主题后同一 Listener 回调：输出须用亮色主题的菜单色
	rstate.st, rstate.theme = light, "light"
	l([]rune("/"), 1, 'x')
	// 首行是选中项（menuSelected 色），取未选中的第二行比 menu 色
	const row = "  /sessions  列出历史会话（* 为当前）"
	if !strings.Contains(b.String(), light.menu.Render(row)) {
		t.Fatalf("menu should render with the active theme: %q", b.String())
	}
	if strings.Contains(b.String(), dark.menu.Render(row)) {
		t.Fatal("menu still rendered with the startup theme")
	}
}

// 选择器：打开只置状态（绘制交给下个提示符的初始回调，避免被 prompt 覆盖）、
// 初始回调绘制条目、↑↓ 改选中、回车确认返回目标并关闭擦除、Esc 关闭不产出目标
func TestPaletteCtlPicker(t *testing.T) {
	st := mustLoadStyles("dark")
	c := &paletteCtl{}
	var b bytes.Buffer
	c.openPicker(sessionItems(testSummaries(), "sess_a"))
	if !c.pickerVisible() || c.rows != 0 {
		t.Fatalf("after open: visible=%v rows=%d, want true/0 (draw is deferred)", c.pickerVisible(), c.rows)
	}

	// 新提示符的初始回调负责绘制；画完把光标交回行首（prompt 还没打印）
	c.onLine(&b, st, nil, 0)
	if c.rows != 3 || !strings.Contains(stripANSI(b.String()), pickerKeyHint) {
		t.Fatalf("initial callback must draw the picker: rows=%d out=%q", c.rows, stripANSI(b.String()))
	}
	if !strings.HasSuffix(b.String(), "\r") {
		t.Fatalf("cursor must return to column 0 before the prompt is printed: %q", b.String())
	}

	// ↓ 移动选中后确认：目标为第二条（sess_a，当前会话）
	c.applyIntent(&b, st, nil, 0, intentDown)
	item, ok := c.takePicker()
	c.clearBelow(&b)
	if !ok || item.id != "sess_a" || !item.cur {
		t.Fatalf("confirm = (%+v, %v), want sess_a current", item, ok)
	}
	if c.pickerVisible() || c.rows != 0 {
		t.Fatalf("after confirm: visible=%v rows=%d, want closed/0", c.pickerVisible(), c.rows)
	}

	// Esc 关闭：不产出目标
	c.openPicker(sessionItems(testSummaries(), "sess_a"))
	c.applyIntent(&b, st, nil, 0, intentEsc)
	if c.pickerVisible() {
		t.Fatal("Esc must close the picker")
	}
	if _, ok := c.takePicker(); ok {
		t.Fatal("closed picker must not yield a target")
	}
}

// 选择器跨提示符存活：初始回调不能把刚画好的列表当残留擦掉（pr-agent 采纳项），
// 否则 /session 之后要等首个按键才看得到会话列表
func TestPalettePickerSurvivesNewPrompt(t *testing.T) {
	st := mustLoadStyles("dark")
	c := &paletteCtl{}
	c.openPicker(sessionItems(testSummaries(), "sess_a"))

	var b bytes.Buffer
	c.onLine(&b, st, nil, 0)
	if !strings.Contains(stripANSI(b.String()), pickerKeyHint) {
		t.Fatalf("first prompt must draw the picker: %q", stripANSI(b.String()))
	}

	// 再来一个新提示符（例如切换会话后又回车）：应重画，而不是只清屏尾
	b.Reset()
	c.onLine(&b, st, nil, 0)
	if c.rows == 0 || !strings.Contains(stripANSI(b.String()), pickerKeyHint) {
		t.Fatalf("picker must be redrawn on every new prompt: rows=%d out=%q", c.rows, stripANSI(b.String()))
	}
}

// 选择器打开时包装层吞掉 Tab、仍拦截 ↑↓（选择器占用方向键）
func TestPaletteCtlPickerKeyTranslation(t *testing.T) {
	c := &paletteCtl{}
	c.openPicker(sessionItems(testSummaries(), ""))
	n := &newlineKeyReader{ctl: c}
	if got := n.translateMenuKeys([]byte("\t")); len(got) != 0 {
		t.Fatalf("Tab must be swallowed while picker is open, got %q", got)
	}
	if got := n.translateMenuKeys([]byte("\x1b[B")); !bytes.HasPrefix(got, menuInjectSeq) {
		t.Fatalf("arrow must be translated while picker is open, got %q", got)
	}
	if intent, ok := c.popIntent(); !ok || intent != intentDown {
		t.Fatalf("queued intent = (%v, %v), want down", intent, ok)
	}
}

// 选择器下 Esc：关闭并把输入行里的过滤词清掉（返回 ok=true 的空行），
// 否则下一次回车会把过滤词当消息发出去
func TestPalettePickerEscapeClearsFilter(t *testing.T) {
	st := mustLoadStyles("dark")
	c := &paletteCtl{}
	var b bytes.Buffer
	c.openPicker(sessionItems(testSummaries(), "sess_a"))
	l := paletteListener(c, &b, &inlineRenderState{st: st})

	// 真实路径由包装层把 Esc 翻译成注入键并入队意图
	c.pushIntent(intentEsc)
	line, pos, ok := l([]rune("redis"), 5, readline.MetaShiftTab)
	if !ok || len(line) != 0 || pos != 0 {
		t.Fatalf("Esc should clear the filter line: (%q, %d, %v)", string(line), pos, ok)
	}
	if c.pickerVisible() {
		t.Fatal("picker must be closed")
	}
}

// 换过滤词后行数不变、选中不变，但行内容全变：必须重绘（只比行数的签名会把
// 旧行留在屏上，pr-agent 采纳项）
func TestPalettePickerRepaintsOnFilterChange(t *testing.T) {
	st := mustLoadStyles("dark")
	c := &paletteCtl{}
	var b bytes.Buffer
	c.openPicker(sessionItems(testSummaries(), ""))
	c.onLine(&b, st, nil, 0)

	// filters: "redis" → 只命中 sess_a；"demo" → 只命中 sess_b（行数同为 1、选中同为 0）
	b.Reset()
	c.onLine(&b, st, []rune("redis"), 5)
	if !strings.Contains(stripANSI(b.String()), "sess_a") {
		t.Fatalf("filter redis must paint sess_a: %q", stripANSI(b.String()))
	}

	b.Reset()
	c.onLine(&b, st, []rune("demo"), 4)
	out := stripANSI(b.String())
	if !strings.Contains(out, "sess_b") {
		t.Fatalf("filter demo must repaint with sess_b instead of reusing the old rows: %q", out)
	}
	if strings.Contains(out, "redis") {
		t.Fatalf("stale row from the previous filter still painted: %q", out)
	}
}
