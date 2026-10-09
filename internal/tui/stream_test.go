package tui

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 用脚本控制流的推进；阻塞入口不得被流式界面调用
type streamResponder struct {
	// 同步流式脚本
	run func(context.Context, func(string) error) (session.RespondOutput, error)
}

// 意外走阻塞路径时明确失败，防止测试把整块输出当作流式
func (s streamResponder) Respond(context.Context, session.RespondInput) (session.RespondOutput, error) {
	return session.RespondOutput{}, errors.New("unexpected blocking response")
}

// 执行本轮脚本
func (s streamResponder) RespondStream(ctx context.Context, _ session.RespondInput, emit func(string) error) (session.RespondOutput, error) {
	return s.run(ctx, emit)
}

// 构造真实会话服务和存储，便于从业务边界核实是否落库
func newStreamService(t *testing.T, run func(context.Context, func(string) error) (session.RespondOutput, error)) (*session.Service, *store.MemoryStore, string) {
	t.Helper()
	mem := store.NewMemoryStore()
	svc := session.NewService(mem, core.NewFactory(), streamResponder{run: run})
	sess, err := svc.NewSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return svc, mem, sess.ID
}

// 消费确认是提交前的边界，取消能解除生产者在确认上的等待
func TestStreamLifecycle(t *testing.T) {
	for _, mode := range []string{"success", "consumer error", "cancel", "exit"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				parent, stop := context.WithCancel(t.Context())
				defer stop()
				ctx, cancel := context.WithCancel(parent)
				defer cancel()
				svc, mem, sid := newStreamService(t, func(_ context.Context, emit func(string) error) (session.RespondOutput, error) {
					if err := emit("chunk"); err != nil {
						return session.RespondOutput{}, err
					}
					return session.RespondOutput{Content: "chunk", Mode: session.ModeBaseline}, nil
				})
				events := startStream(parent, ctx, svc, sid, "question")
				first := <-events
				if first.ack == nil || first.delta != "chunk" {
					t.Fatalf("first event: %+v", first)
				}
				msgs, err := mem.ListMessages(t.Context(), sid)
				if err != nil || len(msgs) != 1 {
					t.Fatalf("committed before consumption: %v, %v", msgs, err)
				}
				want := 1
				switch mode {
				case "success":
					first.ack <- nil
					want = 2
				case "consumer error":
					first.ack <- errBoom
				case "cancel":
					cancel()
				case "exit":
					stop()
				}
				if mode == "exit" {
					// 界面退出后不再消费终态；生产者必须自行退出
					synctest.Wait()
				} else {
					last := <-events
					if last.ack != nil || (last.final.err == nil) != (mode == "success") {
						t.Fatalf("terminal event: %+v", last)
					}
				}
				synctest.Wait()
				msgs, err = mem.ListMessages(t.Context(), sid)
				if err != nil || len(msgs) != want {
					t.Fatalf("messages: %v, %v", msgs, err)
				}
			})
		})
	}
}
