// inline 模式命令面板的会话状态与绘制（步骤 2 内核件）。
// 架构（2026-10-21 评审裁决）：stdin 包装层（ioloop goroutine）只做字节翻译——
// 菜单可见时把 ↑↓/孤立 Esc/Tab 翻译为 readline.MetaShiftTab 注入序列并把意图
// 入队（见 inline.go translateMenuKeys）；所有菜单绘制与擦除只在主 goroutine
// （Listener 回调与提交/退出路径），与 readline 自身的终端写天然串行，
// 避免跨 goroutine 输出交错。视图推导复用 palette.go 纯函数内核，
// 键位语义与 app 模式逐条对齐（by construction，功能头完成标志 1/2）。
// 纯展示层（#20）：不写 Store / RunLedger、命令轮零业务写入；
// UI 语法不进 agent 意图枚举、模型不可见（#2）。
package tui

import (
	"fmt"
	"io"
	"slices"
	"sync"

	"github.com/ergochat/readline"
)

// 菜单意图：包装层拦截到键后入队，Listener 消费注入键时弹出执行。
// FIFO 队列保证混合连击（一次读入多个拦截键）不乱序
type paletteIntent int

const (
	// 上移选中（↑）；视图未激活时内核 moveSel 为无操作
	intentUp paletteIntent = iota
	// 下移选中（↓）
	intentDown
	// 关闭菜单（Esc）：置 off 抑制并擦除，↑↓ 归还历史导航
	intentEsc
	// 重画菜单（Tab 补全后）：Tab 改写缓冲会触发 readline 全量重绘
	// （clean 清到屏尾）擦掉菜单，由紧跟的注入键在 Listener 里重画
	intentRedraw
)

// paletteCtl：inline 模式的菜单会话状态，inlineRun 作用域单点持有。
// 绘制与状态字段（rows/line/pos）只在主 goroutine 访问；ioloop 侧仅经
// 锁操作意图队列与可见性快照，不触碰绘制
type paletteCtl struct {
	// 保护 view/off/pending 的跨 goroutine 访问（ioloop 读写队列与
	// 可见性、主 goroutine 更新视图）；rows/line/pos 主 goroutine 专用不加锁
	mu sync.Mutex
	// 待消费的菜单意图（FIFO）；包装层入队、Listener 消费
	pending []paletteIntent
	// 最近一次推导的菜单视图；items 为空 = 不显示
	view paletteView
	// Esc 抑制标志：置位后菜单隐藏；行值变化时清除（与 app 同语义）
	off bool
	// 上次绘制的菜单行数（含键位自述行），重绘/擦除按此清理防残留
	rows int
	// 会话选择器是否打开（锁内读写；包装层据此拦截按键）
	pickerOn bool
	// 选择器视图与打开时的会话快照（主 goroutine 专用：过滤只在主 goroutine 做）
	picker      pickerView
	pickerItems []sessionItem
	// 上次成功绘制的行内容与锚定下移量：与本次完全一致时跳过重绘，消除光标在
	// 同一显示行内移动（← → Home End 等）时的无意义擦画闪烁。比较行内容而不是
	// 行数 + 选中：换过滤词后行数可能不变但内容全变（如 cat → dog），只看行数会
	// 把旧行留在屏上。任何擦除路径置空
	lastRows []paletteLine
	lastDown int
	// 最近一次回调的行内容与光标位（锚定与列恢复的几何输入；只读拷贝）
	line []rune
	// 光标在行内的 rune 下标
	pos int
}

// visible 报告菜单当前是否显示（锁内快照）；供包装层（ioloop goroutine）
// 决定是否拦截按键。菜单不可见时 ↑↓/Esc/Tab 全部透传，保留 readline
// 历史导航等既有行为
func (c *paletteCtl) visible() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.view.active() && !c.off
}

// pickerVisible 报告会话选择器是否打开（锁内快照）。选择器的条目/选中字段
// 仍只在主 goroutine 读写；包装层只读这个布尔位
func (c *paletteCtl) pickerVisible() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pickerOn
}

// active 报告任一菜单类 UI 是否占用方向键：菜单可见或选择器打开。包装层的
// 按键拦截条件用它，避免两处各自的可见性判断渐渐漂移
func (c *paletteCtl) active() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pickerOn || (c.view.active() && !c.off)
}

// openPicker 打开会话选择器：items 为打开瞬间的会话快照（一次 ListSessions），
// 之后的过滤都在内存里做。打开时清掉菜单的 Esc 抑制，两者互斥。
// 这里只置状态不绘制：输入提交后光标已在新行，而 prompt 还没打印，此时画出的
// 行会被随后的 prompt 覆盖；绘制统一由下个提示符的初始回调完成
func (c *paletteCtl) openPicker(items []sessionItem) {
	c.pickerItems = items
	c.picker = pickerView{items: items}
	c.mu.Lock()
	c.pickerOn = true
	c.off = false
	c.setViewLocked(paletteView{}, false)
	c.mu.Unlock()
}

// closePicker 关闭并擦除选择器（Esc / 回车确认 / 空视图）：行数与绘制签名
// 跟着汇总，避免残留行被下次重绘算错
func (c *paletteCtl) closePicker(out io.Writer) {
	if !c.pickerVisible() {
		return
	}
	c.picker = pickerView{}
	c.pickerItems = nil
	c.mu.Lock()
	c.pickerOn = false
	c.mu.Unlock()
	// 行内容此时是过滤词（非命令），擦除只需清行数不重算菜单
	c.repaint(out, styles{}, nil)
}

// pushIntent 把拦截到的键的意图入队（包装层 / ioloop goroutine 调用）
func (c *paletteCtl) pushIntent(i paletteIntent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = append(c.pending, i)
}

// popIntent 弹出最早入队的意图（Listener 消费注入键时调用；空队列返回 false）
func (c *paletteCtl) popIntent() (paletteIntent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) == 0 {
		return 0, false
	}
	i := c.pending[0]
	c.pending = c.pending[1:]
	return i, true
}

// setView 在锁内更新视图与 off（主 goroutine 绘制路径调用）
func (c *paletteCtl) setView(v paletteView, off bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.view, c.off = v, off
}

// setViewLocked 同 setView，要求调用方已持锁（openPicker 一次锁内完成多字段
// 更新，避免包装层看到半旧状态）
func (c *paletteCtl) setViewLocked(v paletteView, off bool) {
	c.view, c.off = v, off
}

// onLine 处理一次普通键回调：重算过滤视图并按可见性重画或擦除。
// line 为 nil 是 readline 的初始回调（prompt 打印前、行为空，光标在新行行首）
func (c *paletteCtl) onLine(out io.Writer, st styles, line []rune, pos int) {
	if line == nil {
		// 会话选择器跨提示符存活：新提示符到来时把它重画到新行之下，不能当
		// 上一轮残留擦掉（那样列表要等首个按键才出现）。重画后把光标交回行首：
		// 初始回调发生在 prompt 打印前，重画的列恢复按「prompt 已打印」估算
		if c.pickerVisible() {
			c.line, c.pos = nil, 0
			c.lastRows = nil
			c.lastDown = 0
			c.repaintByVisibility(out, st)
			fmt.Fprint(out, "\r")
			return
		}
		// 无选择器：视图必须复位——提交路径 clearBelow 只清屏上菜单，
		// 不复位会让 visible() 恒真、包装层在新提示符继续拦截 ↑↓（历史
		// 导航被劫持直到下一个普通键）。此时下方只可能是上一轮菜单残迹
		// （软换行进草稿下一行的场景），清到屏尾安全
		if c.rows > 0 {
			fmt.Fprint(out, "\x1b[J")
			c.rows = 0
			c.lastRows = nil
			c.lastDown = 0
		}
		c.setView(paletteView{}, false)
		c.line, c.pos = nil, 0
		return
	}
	// 行值变化清除 Esc 抑制（与 app 的编辑键清除同语义）；仅移动光标不清
	changed := string(line) != string(c.line)
	c.line, c.pos = append([]rune(nil), line...), pos
	// 选择器打开时输入行是过滤词（无 / 语义）：只重算过滤，不推导命令菜单
	if c.pickerVisible() {
		c.picker = pickerView{items: filterSessions(c.pickerItems, string(line))}
		c.repaintByVisibility(out, st)
		return
	}
	v := filterPalette(string(line))
	if changed {
		c.setView(v, false)
	} else {
		c.setView(v, c.off)
	}
	c.repaintByVisibility(out, st)
}

// applyIntent 执行一个注入键的意图（Listener 消费时调用）。
// line/pos 为注入键回调携带的当前缓冲（↑↓ 不改缓冲即原行；redraw 为
// Tab 补全后的新行）
func (c *paletteCtl) applyIntent(out io.Writer, st styles, line []rune, pos int, i paletteIntent) {
	c.line, c.pos = append([]rune(nil), line...), pos
	if c.pickerVisible() {
		// 选择器下的意图：↑↓ 改选中、Esc 关闭、redraw 只重画（Tab 由包装层吞掉）
		switch i {
		case intentUp, intentDown:
			if i == intentUp {
				c.picker = c.picker.moveSel(-1)
			} else {
				c.picker = c.picker.moveSel(1)
			}
			c.repaintByVisibility(out, st)
		case intentEsc:
			c.closePicker(out)
		case intentRedraw:
			c.repaintByVisibility(out, st)
		}
		return
	}
	switch i {
	case intentUp, intentDown:
		delta := 1
		if i == intentUp {
			delta = -1
		}
		c.mu.Lock()
		c.view = c.view.moveSel(delta)
		c.mu.Unlock()
		c.repaintByVisibility(out, st)
	case intentEsc:
		c.setView(c.view, true)
		c.repaintByVisibility(out, st)
	case intentRedraw:
		c.setView(filterPalette(string(line)), false)
		c.repaintByVisibility(out, st)
	}
}

// tab 处理 Tab 键补全：先剥掉 readline 刚插入缓冲的 '\t'（无 AutoComplete
// 时 Tab 按普通字符进缓冲），再调内核补全。菜单不可见时返回 ok=false，
// 保留 Tab 插字符的既有行为
func (c *paletteCtl) tab(line []rune, pos int) (newLine []rune, newPos int, ok bool) {
	// 选择器下 Tab 无补全语义（输入行是过滤词），包装层已吞掉此键，此处兼做防御
	if c.pickerVisible() {
		return nil, 0, false
	}
	if pos > 0 && pos <= len(line) && line[pos-1] == '\t' {
		stripped := make([]rune, 0, len(line)-1)
		stripped = append(stripped, line[:pos-1]...)
		stripped = append(stripped, line[pos:]...)
		// 剥后光标位不再使用：补全后的光标由内核返回（固定行尾）
		line = stripped
	}
	if !c.visible() {
		return nil, 0, false
	}
	completed, cursor, ok := completePaletteLine(string(line), c.view)
	if !ok {
		return nil, 0, false
	}
	return []rune(completed), cursor, true
}

// repaintByVisibility 按当前视图重画（可见）或擦除（不可见但有残留）。
// 选择器与命令菜单互斥：选择器打开时只画会话条目
func (c *paletteCtl) repaintByVisibility(out io.Writer, st styles) {
	var rows []paletteLine
	switch {
	case c.pickerVisible():
		// 选中下标与条目只在主 goroutine 读写，无需加锁
		rows = pickerRows(c.picker)
	case c.visible():
		c.mu.Lock()
		rows = paletteRows(c.view)
		c.mu.Unlock()
	}
	c.repaint(out, st, rows)
}

// repaint 单点绘制原语：从光标行下移到「输入显示末行之下」锚点，
// 一次扫描完成清旧行 + 画新行（K = max(旧行数+1, 新行数)，含一行估算
// 余量），再把光标移回输入行原位。rows 为空且无残留时零输出。
// 锚定输入末行之下（评审裁决 2）：折行与光标行中均不与输入显示重叠，
// 擦除永不碰输入行
func (c *paletteCtl) repaint(out io.Writer, st styles, rows []paletteLine) {
	if len(rows) == 0 && c.rows == 0 {
		return
	}
	// 光标位防御钳制：状态漂移时不许 panic（同内核 selectedDoc 的防御口径）
	pos := c.pos
	if pos > len(c.line) {
		pos = len(c.line)
	}
	width := terminalWidth()
	down := anchorDownMoves(string(c.line), pos, width)
	// 行内容与位置都与上次绘制一致：屏上已是目标状态，跳过重绘（防光标移动闪烁）。
	// 比较整行内容而非行数 + 选中——换过滤词后行数可能不变，只比行数会把旧行留在屏上
	if len(rows) > 0 && down == c.lastDown && slices.Equal(rows, c.lastRows) {
		return
	}
	// 光标还原列 = 光标前逻辑列数落在其显示行上的余数（折行续行时用全宽
	// 会钳在右边距错位列）；恰为整数倍时余数为 width（行末悬挂换行位），
	// CUF 钳在右边距属既有折行估算容忍
	prefixCols := promptCols + displayCols(string(c.line[:pos]))
	restoreCol := cursorColOnRow(prefixCols, width)
	// 光标在行尾时清本行行尾：输入增长越过折行点时，旧菜单首行残迹会
	// 遗留在输入末行行尾（终端自动折行只覆写一列），此处兜住；
	// 光标在行中的编辑由 readline 全量重绘清屏兜底，不清行尾防误伤
	if pos == len(c.line) {
		fmt.Fprint(out, "\x1b[K")
	}
	// 下移到锚点（输入显示末行之下）。用行馈 \n 而非 CUD（\x1b[B）：行馈在
	// 屏幕底行会滚动终端腾出新行，CUD 只会钳在底边把菜单画到输入行上——
	// 聊天输入行恰常在屏底，锚点在屏外时菜单仍能整体呈现在输入行下方；
	// 滚动后相对行距不变，末尾上移还原仍精确
	for i := 0; i < down; i++ {
		fmt.Fprint(out, "\n")
	}
	sweep := len(rows)
	if c.rows+1 > sweep {
		sweep = c.rows + 1
	}
	for i := 0; i < sweep; i++ {
		fmt.Fprint(out, "\r\x1b[2K")
		if i < len(rows) {
			s := st.menu
			if rows[i].selected {
				s = st.menuSelected
			}
			fmt.Fprint(out, s.Render(rows[i].text))
		}
		if i < sweep-1 {
			fmt.Fprint(out, "\n")
		}
	}
	// 回到光标行与列：上移 (down + sweep - 1) 行后归零列、右移恢复列位
	fmt.Fprintf(out, "\x1b[%dA\r\x1b[%dC", down+sweep-1, restoreCol)
	c.rows = len(rows)
	c.lastRows = slices.Clone(rows)
	c.lastDown = down
}

// clearBelow 提交路径擦除：Enter 后 readline 回显换行，光标已在输入末行
// 下一行（恰为菜单首行位置），从当前行向下清 rows+1 行并回到起始行，
// 交还 echoUserMessage 预期的光标位
func (c *paletteCtl) clearBelow(out io.Writer) {
	if c.rows == 0 {
		return
	}
	for i := 0; i <= c.rows; i++ {
		fmt.Fprint(out, "\r\x1b[2K")
		if i < c.rows {
			fmt.Fprint(out, "\n")
		}
	}
	fmt.Fprintf(out, "\x1b[%dA", c.rows)
	c.rows = 0
	// 屏上已无菜单：签名作废（防后续同形菜单被误跳过），视图一并复位
	// （否则下个提示符 visible() 恒真，包装层劫持 ↑↓ 历史导航）
	c.lastRows = nil
	c.lastDown = 0
	c.setView(paletteView{}, false)
}

// forceRepaint 绕过签名跳过优化全量重画：清屏（Ctrl+L）/ 挂起恢复（Ctrl+Z）
// 等 readline 整体重绘会擦掉菜单而行内容与光标不变，签名比对会误判无变化
func (c *paletteCtl) forceRepaint(out io.Writer, st styles) {
	c.lastRows = nil
	c.lastDown = 0
	c.repaintByVisibility(out, st)
}

// paletteListener 构造 readline 逐键回调：普通键重算菜单、Tab 补全改写缓冲、
// 注入键（MetaShiftTab）消费意图。readline 保证普通键先处理缓冲后回调、
// Enter/EOF/Interrupt 不回调；初始回调 (nil,0,0) 由 onLine 按空行处理。
// 所有菜单终端写都经此入口（主 goroutine），与 readline 自身重绘串行。
// 样式表经指针读取（Listener 构造后无法替换）：/theme 切换后菜单立即用新色表
func paletteListener(c *paletteCtl, out io.Writer, theme *inlineRenderState) readline.Listener {
	return func(line []rune, pos int, key rune) ([]rune, int, bool) {
		switch key {
		case readline.MetaShiftTab:
			// 注入键：消费翻译器入队的意图（↑↓/Esc/重画）；真实 shift+tab
			// 无意图时纯 no-op，无副作用
			if intent, ok := c.popIntent(); ok {
				wasPicker := c.pickerVisible()
				c.applyIntent(out, theme.st, line, pos, intent)
				// 选择器被 Esc 关闭：输入行里留下的是过滤词，不是待发消息，
				// 一并清空（否则下一次回车会把它当消息发出去）
				if wasPicker && intent == intentEsc {
					return []rune{}, 0, true
				}
			}
			return nil, 0, false
		case '\t':
			if newLine, newPos, ok := c.tab(line, pos); ok {
				// 补全改写缓冲：readline 随后的全量重绘会清掉菜单，由翻译器
				// 紧跟本键注入的重画意图恢复
				return newLine, newPos, true
			}
			// 不可补全：tab 已进缓冲，按普通键重算（过滤口径不含空白，不受影响）
			c.onLine(out, theme.st, line, pos)
			return nil, 0, false
		case readline.CharCtrlL, readline.CharCtrlZ:
			// 清屏 / 挂起恢复整体重绘输入区并擦掉菜单，签名比对会误判无变化，
			// 强制全量重画
			c.onLine(out, theme.st, line, pos)
			c.forceRepaint(out, theme.st)
			return nil, 0, false
		default:
			c.onLine(out, theme.st, line, pos)
			return nil, 0, false
		}
	}
}

// submitWithPalette 提交前菜单收尾的替换面：菜单可见时裸命令片段替换为
// 选中规范名再交既有命令分支，含参输入原样；不可见（含多行草稿，末行非 /
// 开头）不替换，与 app 模式同口径
func submitWithPalette(c *paletteCtl, text string) string {
	if c.visible() {
		if execLine, handled := paletteSubmit(text, c.view); handled {
			return execLine
		}
	}
	return text
}

// clearAll /clear 命令擦屏：清可视屏与回滚区并把光标归位。屏上不再有
// 菜单残迹，行数 / 绘制签名 / 视图一并归零——否则下次重绘会按旧行数清理，
// 且 visible() 残留会让包装层继续把 ↑↓ 当菜单意图（擦除/复位成对）
func (c *paletteCtl) clearAll(out io.Writer) {
	fmt.Fprint(out, "\x1b[3J\x1b[2J\x1b[H")
	c.rows = 0
	c.lastRows = nil
	c.lastDown = 0
	c.mu.Lock()
	c.setViewLocked(paletteView{}, false)
	// 整屏已擦：选择器一并关闭（否则后续方向键仍被当成选择器意图）
	c.pickerOn = false
	c.mu.Unlock()
	c.picker = pickerView{}
	c.pickerItems = nil
	c.line, c.pos = nil, 0
}

// clearTail 退出路径清残迹：EOF / ErrInterrupt 后 readline 写过中断提示、
// 光标精确位置不可知，清当前行行首到屏尾——退出时刻输入行之下只可能是
// 菜单残迹，不会误伤其他内容
func (c *paletteCtl) clearTail(out io.Writer) {
	if c.rows == 0 {
		return
	}
	fmt.Fprint(out, "\r\x1b[J")
	c.rows = 0
	c.lastRows = nil
	c.lastDown = 0
	// 退出前同样复位视图，与 clearBelow 同口径（擦除/复位成对）
	c.setView(paletteView{}, false)
}

// takePicker 回车确认：取选中条目并只关状态，不在此绘制。回车后光标已在
// 输入行下一行，就地重绘的相对位移不可靠——调用方用 clearBelow 按光标位置
// 清掉选择器行，再擦掉输入回显，确认行从干净行位开始
func (c *paletteCtl) takePicker() (sessionItem, bool) {
	if !c.pickerVisible() {
		return sessionItem{}, false
	}
	item, ok := c.picker.selected()
	c.picker = pickerView{}
	c.pickerItems = nil
	c.mu.Lock()
	c.pickerOn = false
	c.mu.Unlock()
	return item, ok
}

// pickerFilter 最近一次按键回调的行内容；选择器期间即过滤词（擦回显用）
func (c *paletteCtl) pickerFilter() string {
	return string(c.line)
}

// promptCols 输入行 prompt（「❯ 」）的显示列数，折行与列恢复估算用
const promptCols = 2

// cursorColOnRow 计算光标在其显示行上的列位：光标前逻辑列数对终端宽取
// 模的余数；恰为整数倍时为 width（行末悬挂换行位，CUF 会在右边距钳住）
func cursorColOnRow(prefixCols, width int) int {
	return prefixCols - (rowsFor(prefixCols, width)-1)*width
}

// anchorDownMoves 计算从光标行下移到「输入显示末行之下」的行数：
// 输入总显示行数减光标行序（0 基）。宽度按 displayCols 粗估（CJK 2 列，
// 与 echoUserMessage 同款容忍：宁多勿少，错位退化不崩溃）
func anchorDownMoves(line string, pos, width int) int {
	total := rowsFor(promptCols+displayCols(line), width)
	cursorRow := rowsFor(promptCols+displayCols(line[:pos]), width) - 1
	return total - cursorRow
}
