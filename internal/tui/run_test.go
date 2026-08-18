package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/config"
)

// Run 按模式分发：app 走 bubbletea 全屏；inline/空走行内（非 tty 下报明确错误证明走了行内）
func TestRunDispatch(t *testing.T) {
	ctx := context.Background()

	// inline（默认）：非 tty 无 svc 时应先撞到行内入口的 tty 检查
	err := Run(ctx, nil, "s", "markdown", &bytes.Buffer{}, config.TUI{}, nil)
	if err == nil || !strings.Contains(err.Error(), "requires a terminal") {
		t.Fatalf("inline dispatch err = %v, want requires a terminal", err)
	}

	// 显式 inline 同上
	err = Run(ctx, nil, "s", "markdown", &bytes.Buffer{}, config.TUI{Mode: "inline"}, nil)
	if err == nil || !strings.Contains(err.Error(), "requires a terminal") {
		t.Fatalf("explicit inline err = %v", err)
	}

	// app：应报 app 自己的 tty 错误（证明分发走了 appRun 而非 inlineRun）
	err = Run(ctx, nil, "s", "markdown", &bytes.Buffer{}, config.TUI{Mode: "app"}, nil)
	if err == nil || !strings.Contains(err.Error(), "app UI requires a terminal") {
		t.Fatalf("app dispatch err = %v, want app UI requires a terminal", err)
	}
	// 未知值按 inline
	err = Run(ctx, nil, "s", "markdown", &bytes.Buffer{}, config.TUI{Mode: "bogus"}, nil)
	if err == nil || !strings.Contains(err.Error(), "inline chat requires a terminal") {
		t.Fatalf("bogus mode should fall back to inline, err = %v", err)
	}
}
