// TUI 装配与分发：按模式选行内（inline）或全屏（app）入口。
// Step 1 默认走行内；Step 3 加 config.TUI.Mode 选。
package tui

import (
	"context"
	"io"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"aruing/internal/config"
	"aruing/internal/session"
)

// 输入框占用的终端行数（仅 app 模式 viewport 高度计算用）
const inputHeight = 3

// Run 接收交互入口，按模式分发：app（bubbletea 全屏）或默认 inline（行内留痕）。
// tuiCfg 提供主题与模式；Mode 为 "app" 走全屏，其余（含空）走行内。
func Run(ctx context.Context, svc *session.Service, sessionID, format string, out io.Writer, tuiCfg config.TUI) error {
	if tuiCfg.Mode == "app" {
		return appRun(ctx, svc, sessionID, format, out, tuiCfg)
	}
	return inlineRun(ctx, svc, sessionID, format, tuiCfg.Theme, out)
}

// app 模式入口（bubbletea 全屏）：config.TUI.Mode=="app" 或 --ui app 时经 Run 调入
func appRun(ctx context.Context, svc *session.Service, sessionID, format string, out io.Writer, tuiCfg config.TUI) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p := tea.NewProgram(newModel(ctx, svc, sessionID, format, tuiCfg.Theme), tea.WithOutput(out))
	_, err := p.Run()
	return err
}

// 构造初始 Model：textarea 输入、viewport 历史、spinner 等待指示；按主题加载样式色表
func newModel(ctx context.Context, svc *session.Service, sessionID, format, tuiTheme string) Model {
	st := loadStyles(tuiTheme)

	ta := textarea.New()
	ta.Prompt = "" // 提示符由 View 自绘，避免与输入框自身提示重复
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(inputHeight)
	ta.Focus()

	vp := viewport.New(80, 20) // 初始占位尺寸，WindowSizeMsg 到达后按终端修正

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = st.spinner

	return Model{
		svc:       svc,
		sessionID: sessionID,
		tuiTheme:  tuiTheme,
		styles:    st,
		input:     ta,
		viewport:  vp,
		spinner:   sp,
		ctx:       ctx,
	}
}
