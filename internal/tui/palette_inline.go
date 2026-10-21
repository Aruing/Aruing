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
	"sync"
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

// onLine 处理一次普通键回调：重算过滤视图并按可见性重画或擦除。
// line 为 nil 是 readline 的初始回调（prompt 打印前、行为空）：此时光标在
// 新行 prompt 之后且下方只可能是上一行遗留的菜单残迹（软换行进草稿下一行
// 的场景），清到屏尾安全；空行不折行，不会误伤输入显示
func (c *paletteCtl) onLine(out io.Writer, st styles, line []rune, pos int) {
	if line == nil {
		if c.rows > 0 {
			fmt.Fprint(out, "\x1b[J")
			c.rows = 0
			c.setView(paletteView{}, false)
		}
		c.line, c.pos = nil, 0
		return
	}
	// 行值变化清除 Esc 抑制（与 app 的编辑键清除同语义）；仅移动光标不清
	changed := string(line) != string(c.line)
	c.line, c.pos = append([]rune(nil), line...), pos
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

// repaintByVisibility 按当前视图重画（可见）或擦除（不可见但有残留）
func (c *paletteCtl) repaintByVisibility(out io.Writer, st styles) {
	var rows []paletteLine
	if c.visible() {
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
	// 光标在行尾时清本行行尾：输入增长越过折行点时，旧菜单首行残迹会
	// 遗留在输入末行行尾（终端自动折行只覆写一列），此处兜住；
	// 光标在行中的编辑由 readline 全量重绘清屏兜底，不清行尾防误伤
	if pos == len(c.line) {
		fmt.Fprint(out, "\x1b[K")
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
	fmt.Fprintf(out, "\x1b[%dA\r\x1b[%dC", down+sweep-1, promptCols+displayCols(string(c.line[:pos])))
	c.rows = len(rows)
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
}

// promptCols 输入行 prompt（「❯ 」）的显示列数，折行与列恢复估算用
const promptCols = 2

// anchorDownMoves 计算从光标行下移到「输入显示末行之下」的行数：
// 输入总显示行数减光标行序（0 基）。宽度按 displayCols 粗估（CJK 2 列，
// 与 echoUserMessage 同款容忍：宁多勿少，错位退化不崩溃）
func anchorDownMoves(line string, pos, width int) int {
	total := rowsFor(promptCols+displayCols(line), width)
	cursorRow := rowsFor(promptCols+displayCols(line[:pos]), width) - 1
	return total - cursorRow
}
