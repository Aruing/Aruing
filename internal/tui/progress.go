// Turn 期间的进度行与 spinner 终端协调器。
// 背景：编排/Tower 的 progress 输出（解析问题… / 执行 k8s：… / ↳ …）与行内
// spinner 各自写同一终端；spinner 用 \r 清行只作用于当前行，进度行自带换行把
// 光标顶到下一行后，旧 spinner 帧残留在上一行行尾（每步都留「⠋ 思考中…」）。
// 协调规则：进度行落屏前先清 spinner 行、落行后把 spinner 重画到最新行下方；
// 思考结束 spinnerStop 只清 spinner 行，进度行保留为调查留痕。
// 纯展示层协调，不改变进度内容（守 #20）
package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// TurnProgress 同时是两样东西：
// 1) io.Writer——cmd wiring 注入编排/Tower 的 progress 出口（经 SetProgress）；
// 2) spinner 控制器——行内引擎每轮 Turn 驱动 Start/Tick/Stop。
// 两侧跨 goroutine 并发调用，内部用 mutex 串行化对终端的写序
type TurnProgress struct {
	// 未绑定终端（app / 单句 / stdin 行模式）时进度透传到此（通常是 stderr）
	fallback io.Writer
	// 绑定后的终端（inline 模式的 out）；nil = 未绑定
	out io.Writer
	// spinner 渲染所需样式表（绑定时定格）
	st styles
	// spinner 行当前是否在屏
	visible bool
	// 当前 spinner 帧下标
	frame int
	// spinnerStart 以来落屏的进度行数（错误路径判断称呼行是否紧邻 spinner 用）
	lines int
	// 串行化进度行与 spinner 帧的写序（两条流来自不同 goroutine）
	mu sync.Mutex
}

// NewTurnProgress 建协调器；fallback 为未绑定时的进度透传出口，可为 nil（丢弃）
func NewTurnProgress(fallback io.Writer) *TurnProgress {
	return &TurnProgress{fallback: fallback}
}

// bind 行内引擎装载终端与样式；此后进度行与 spinner 同屏协调
func (p *TurnProgress) bind(out io.Writer, st styles) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.out, p.st = out, st
}

// Write 实现 io.Writer：编排/Tower progressf 的入口，每次写入按行处理
// （清 spinner 行 → 印进度行 → spinner 重画到该行下方）
func (p *TurnProgress) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.out == nil {
		if p.fallback == nil {
			return len(b), nil
		}
		return p.fallback.Write(b)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if p.visible {
			clearLine(p.out)
		}
		fmt.Fprintln(p.out, line)
		p.lines++
		if p.visible {
			p.draw()
		}
	}
	return len(b), nil
}

// spinnerStart spinner 首帧上屏（行内引擎每轮 Turn 开始时调用）；未绑定终端时空操作
func (p *TurnProgress) spinnerStart(st styles) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.out == nil {
		return
	}
	p.st = st
	p.visible = true
	p.frame = 0
	p.lines = 0
	p.draw()
}

// spinnerTick 推进一帧（ticker 调用）；spinner 不在屏时空操作
func (p *TurnProgress) spinnerTick() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.visible {
		return
	}
	p.frame = (p.frame + 1) % len(spinnerFrames)
	p.draw()
}

// spinnerStop 清 spinner 行（Turn 完成 / 取消）；进度行不受影响
func (p *TurnProgress) spinnerStop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.visible {
		return
	}
	p.visible = false
	clearLine(p.out)
}

// progressLines 报告 spinnerStart 以来落屏的进度行数（spinner 不在屏时为最后一次
// 统计值；调用方用于判断称呼行与 spinner 是否相邻）
func (p *TurnProgress) progressLines() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lines
}

// draw 在当前行画 spinner 帧（调用方须持锁）
func (p *TurnProgress) draw() {
	renderSpinner(p.out, p.st, p.frame)
}
