// 将会话同步回调接入终端事件循环；增量确认后才允许上游继续，取消时解除等待
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Aruing/Aruing/internal/session"
)

// 一轮展示事件；增量带确认通道，终态只携带会话服务的完整结果
type streamMsg struct {
	// 本次新增正文，空字符串也是合法增量
	delta string
	// 消费方返回显示错误；容量为一，界面更新无需等待生产者
	ack chan error
	// 非增量事件的完整终态
	final turnMsg
}

// 启动一轮流式会话；父上下文控制界面生命周期，轮次上下文控制当前生成
// 只有生产者关闭事件通道，退出界面后发送与消费确认均可解除阻塞
func startStream(parent, ctx context.Context, svc *session.Service, sessionID, text string) <-chan streamMsg {
	events := make(chan streamMsg)
	go func() {
		defer close(events)
		result, err := svc.TurnStream(ctx, sessionID, text, func(delta string) error {
			ack := make(chan error, 1)
			select {
			case events <- streamMsg{delta: delta, ack: ack}:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case err := <-ack:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		select {
		case events <- streamMsg{final: turnMsg{result: result, err: err}}:
		case <-parent.Done():
		}
	}()
	return events
}

// 每次只消费一个事件，保证增量与终态在界面更新中按序处理
func nextStream(events <-chan streamMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-events
		if !ok {
			return turnMsg{err: context.Canceled}
		}
		return msg
	}
}

// 启动命令将事件通道交回模型，避免更新函数执行后台工作
type streamStartedMsg struct {
	// 当前轮次唯一的事件源
	events <-chan streamMsg
}
