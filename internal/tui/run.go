// TUI 装配与启动：构造 Model、注入依赖、启动 bubbletea program。
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

// 输入框占用的终端行数（影响 viewport 可用高度）
const inputHeight = 3

// 装配 Model 并启动 bubbletea program，阻塞至退出。
// ctx 在 program 退出时由 defer cancel 触发，取消在途的 Turn 调用。
// tuiCfg 提供主题（dark | light | auto），决定样式色表与 glamour 渲染风格。
func Run(ctx context.Context, svc *session.Service, sessionID, format string, out io.Writer, tuiCfg config.TUI) error {
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
		format:    format,
		tuiTheme:  tuiTheme,
		styles:    st,
		input:     ta,
		viewport:  vp,
		spinner:   sp,
		ctx:       ctx,
	}
}
