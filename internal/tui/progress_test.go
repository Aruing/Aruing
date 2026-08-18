package tui

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
)

// 绑定后协调：进度行先清 spinner 再落行，spinner 重画到进度行下方（无残留帧）；
// spinnerStart 以来进度行计数归零、写入累加
func TestTurnProgressCoordination(t *testing.T) {
	var b strings.Builder
	p := NewTurnProgress(io.Discard)
	st := mustLoadStyles("dark")
	p.bind(&b, st)
	p.spinnerStart(st)
	if !strings.Contains(b.String(), "思考中") {
		t.Fatalf("spinner start missing: %q", b.String())
	}
	if got := p.progressLines(); got != 0 {
		t.Fatalf("progressLines at start = %d, want 0", got)
	}
	b.Reset()
	if _, err := p.Write([]byte("  执行 k8s：列出 Ingress\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := p.progressLines(); got != 1 {
		t.Fatalf("progressLines after write = %d, want 1", got)
	}
	got := stripANSI(b.String())
	// 顺序：进度行在前、spinner 重画在后（思考中不残留在进度行行尾）
	i, j := strings.Index(got, "执行 k8s"), strings.LastIndex(got, "思考中")
	if i < 0 || j < 0 || j < i {
		t.Fatalf("want progress line then spinner redraw, got %q", got)
	}
	// 停止：只清 spinner 行，不再新增思考中文本
	b.Reset()
	p.spinnerStop()
	if s := stripANSI(b.String()); strings.Contains(s, "思考中") {
		t.Fatalf("stop must clear spinner only: %q", s)
	}
}

// 未绑定：进度透传 fallback（app / 单句 / stdin 行模式维持 stderr 现状）；spinner 空操作不 panic
func TestTurnProgressFallback(t *testing.T) {
	var fb bytes.Buffer
	p := NewTurnProgress(&fb)
	p.spinnerStart(mustLoadStyles("dark"))
	if _, err := p.Write([]byte("解析问题…\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(fb.String(), "解析问题") {
		t.Fatalf("fallback missing progress: %q", fb.String())
	}
}

// 并发写序：进度行与 spinner tick 来自不同 goroutine，mutex 串行化（-race 下验证）
func TestTurnProgressConcurrent(t *testing.T) {
	p := NewTurnProgress(io.Discard)
	var b strings.Builder
	st := mustLoadStyles("dark")
	p.bind(&b, st)
	p.spinnerStart(st)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			p.spinnerTick()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_, _ = p.Write([]byte("line\n"))
		}
	}()
	wg.Wait()
}
