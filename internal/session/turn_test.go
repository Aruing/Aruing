package session_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

func newTestFactory() *core.Factory {
	return core.NewFactory()
}

// 两轮回声：消息顺序为用户/助手/用户/助手；内容与模式符合基线约定
func TestTurnEchoMessageOrder(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	svc := session.NewService(mem, newTestFactory(), session.EchoResponder{})

	sess, err := svc.NewSession(ctx)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if !strings.HasPrefix(sess.ID, "sess_") {
		t.Fatalf("session id prefix: %q", sess.ID)
	}

	r1, err := svc.Turn(ctx, sess.ID, "a")
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if r1.AssistantMessage.Content != "收到：a" {
		t.Fatalf("assistant content: %q", r1.AssistantMessage.Content)
	}
	if r1.AssistantMessage.Mode != session.ModeBaseline {
		t.Fatalf("mode: %q", r1.AssistantMessage.Mode)
	}
	if r1.RunID != "" || r1.AssistantMessage.RunID != "" {
		t.Fatalf("echo should not set run id: %q / %q", r1.RunID, r1.AssistantMessage.RunID)
	}

	if _, err = svc.Turn(ctx, sess.ID, "b"); err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	msgs, err := mem.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages, got %d", len(msgs))
	}
	// 顺序与内容足以证明两轮回声落库；不逐条扫编号与会话绑定
	wantRoles := []string{session.RoleUser, session.RoleAssistant, session.RoleUser, session.RoleAssistant}
	wantContent := []string{"a", "收到：a", "b", "收到：b"}
	for i := range msgs {
		if msgs[i].Role != wantRoles[i] || msgs[i].Content != wantContent[i] {
			t.Fatalf("msg %d: role=%q content=%q", i, msgs[i].Role, msgs[i].Content)
		}
	}
}

// 不存在的会话编号应返回明确错误
func TestTurnMissingSession(t *testing.T) {
	ctx := context.Background()
	svc := session.NewService(store.NewMemoryStore(), newTestFactory(), session.EchoResponder{})

	_, err := svc.Turn(ctx, "sess_missing", "hello")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}
}

// 假诊断管道：记录收到的运行，并返回固定报告
type fakeExecutor struct {
	lastRun core.Run
	report  core.Report
	err     error
}

func (f *fakeExecutor) Execute(_ context.Context, run core.Run) (core.Outcome, error) {
	f.lastRun = run
	if f.err != nil {
		return core.Outcome{}, f.err
	}
	rep := f.report
	rep.RunID = run.ID
	return core.Outcome{Report: &rep}, nil
}

// 临时诊断加假执行器：助手消息应有运行编号，且执行时会话编号与当前会话一致
func TestTurnDiagnoseSessionID(t *testing.T) {
	ctx := context.Background()
	factory := newTestFactory()
	mem := store.NewMemoryStore()
	ledger := store.NewMemoryRunLedger()
	exec := &fakeExecutor{
		report: core.Report{
			Title:   "demo 诊断报告",
			Summary: "后端异常",
		},
	}
	svc := session.NewService(mem, factory, session.NewDiagnoseResponder(factory, exec, ledger))

	sess, err := svc.NewSession(ctx)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	result, err := svc.Turn(ctx, sess.ID, "demo-api 访问不了")
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	if result.RunID == "" {
		t.Fatal("expected run id")
	}
	if !strings.HasPrefix(result.RunID, "run_") {
		t.Fatalf("run id prefix: %q", result.RunID)
	}
	if result.AssistantMessage.RunID != result.RunID {
		t.Fatalf("assistant run id %q != result %q", result.AssistantMessage.RunID, result.RunID)
	}
	if result.AssistantMessage.Mode != session.ModeDiagnostic {
		t.Fatalf("mode: %q", result.AssistantMessage.Mode)
	}
	if exec.lastRun.SessionID != sess.ID {
		t.Fatalf("run session id: got %q want %q", exec.lastRun.SessionID, sess.ID)
	}
	if exec.lastRun.Question != "demo-api 访问不了" {
		t.Fatalf("run question: %q", exec.lastRun.Question)
	}
	if !strings.Contains(result.AssistantMessage.Content, "后端异常") {
		t.Fatalf("assistant content: %q", result.AssistantMessage.Content)
	}
	if result.Report == nil || result.Report.Summary != "后端异常" {
		t.Fatalf("report: %+v", result.Report)
	}
	// 升级成功后台账可按运行编号读回
	rec, err := ledger.Get(ctx, result.RunID)
	if err != nil {
		t.Fatalf("ledger get: %v", err)
	}
	if rec.SessionID != sess.ID || rec.Report.Summary != "后端异常" {
		t.Fatalf("ledger record: %+v", rec)
	}
}

// 应答器收到的历史不含本轮用户
func TestTurnHistoryExcludesCurrentUser(t *testing.T) {
	ctx := context.Background()
	factory := newTestFactory()
	mem := store.NewMemoryStore()
	spy := &historySpyResponder{}
	svc := session.NewService(mem, factory, spy)

	sess, err := svc.NewSession(ctx)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if _, err := svc.Turn(ctx, sess.ID, "first"); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if len(spy.last.History) != 0 {
		t.Fatalf("first turn history len: %d", len(spy.last.History))
	}
	if _, err := svc.Turn(ctx, sess.ID, "second"); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if len(spy.last.History) != 2 {
		t.Fatalf("second turn history len: %d", len(spy.last.History))
	}
	if spy.last.History[0].Content != "first" || spy.last.History[1].Content != "收到：first" {
		t.Fatalf("history content: %+v", spy.last.History)
	}
	if spy.last.UserText != "second" {
		t.Fatalf("user text: %q", spy.last.UserText)
	}
}

// 记录最后一次应答输入，并回回声风格回复
type historySpyResponder struct {
	last session.RespondInput
}

func (s *historySpyResponder) Respond(_ context.Context, in session.RespondInput) (session.RespondOutput, error) {
	s.last = in
	return session.RespondOutput{
		Content: "收到：" + in.UserText,
		Mode:    session.ModeBaseline,
	}, nil
}

// 检查点正文非空时写序：用户 → 检查点 → 助手；用户原文不丢
func TestTurnCheckpoint(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemoryStore()
	svc := session.NewService(mem, newTestFactory(), &checkpointResponder{})

	sess, err := svc.NewSession(ctx)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	result, err := svc.Turn(ctx, sess.ID, "hello")
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	if result.AssistantMessage.Mode != session.ModeBaseline {
		t.Fatalf("assistant mode: %q", result.AssistantMessage.Mode)
	}

	msgs, err := mem.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages (user/checkpoint/assistant), got %d", len(msgs))
	}
	if msgs[0].Role != session.RoleUser || msgs[0].Content != "hello" {
		t.Fatalf("msg0: %+v", msgs[0])
	}
	if msgs[1].Mode != session.ModeCheckpoint || !strings.Contains(msgs[1].Content, "handoff") {
		t.Fatalf("msg1 checkpoint: %+v", msgs[1])
	}
	if msgs[2].Role != session.RoleAssistant || msgs[2].Mode != session.ModeBaseline {
		t.Fatalf("msg2: %+v", msgs[2])
	}
	// 存储全量：检查点不替代用户原文
	if msgs[0].Content != "hello" {
		t.Fatal("user message must remain")
	}
}

type checkpointResponder struct{}

func (checkpointResponder) Respond(_ context.Context, in session.RespondInput) (session.RespondOutput, error) {
	return session.RespondOutput{
		Content:           "收到：" + in.UserText,
		Mode:              session.ModeBaseline,
		CheckpointContent: "[checkpoint] handoff summary for tests",
	}, nil
}

// 流式测试双用脚本控制增量与终态，阻塞入口用于准备既有历史
type scriptedStreamResponder struct {
	// 本轮流式应答脚本
	stream func(context.Context, session.RespondInput, func(string) error) (session.RespondOutput, error)
}

// 阻塞路径保持可用，供两种入口交替调用
func (s scriptedStreamResponder) Respond(_ context.Context, in session.RespondInput) (session.RespondOutput, error) {
	return session.RespondOutput{Content: in.UserText, Mode: session.ModeBaseline}, nil
}

// 同步执行脚本，模拟真实应答器的消费契约
func (s scriptedStreamResponder) RespondStream(ctx context.Context, in session.RespondInput, emit func(string) error) (session.RespondOutput, error) {
	return s.stream(ctx, in, emit)
}

// 消费增量期间只有用户消息，成功后按检查点、完整回复顺序提交
func TestTurnStreamCommit(t *testing.T) {
	ctx := t.Context()
	mem := store.NewMemoryStore()
	responder := scriptedStreamResponder{stream: func(_ context.Context, in session.RespondInput, emit func(string) error) (session.RespondOutput, error) {
		if len(in.History) != 2 || in.UserText != "next" {
			t.Fatalf("unexpected input: %+v", in)
		}
		for _, delta := range []string{"完整", "回复"} {
			if err := emit(delta); err != nil {
				return session.RespondOutput{}, err
			}
		}
		return session.RespondOutput{Content: "完整回复", Mode: session.ModeBaseline, CheckpointContent: " handoff "}, nil
	}}
	svc := session.NewService(mem, newTestFactory(), responder)
	sess, err := svc.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Turn(ctx, sess.ID, "prior"); err != nil {
		t.Fatal(err)
	}
	var chunks []string
	result, err := svc.TurnStream(ctx, sess.ID, "next", func(delta string) error {
		msgs, listErr := mem.ListMessages(ctx, sess.ID)
		if listErr != nil || len(msgs) != 3 || msgs[2].Role != session.RoleUser {
			t.Fatalf("premature commit: %v, %v", msgs, listErr)
		}
		chunks = append(chunks, delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := mem.ListMessages(ctx, sess.ID)
	if err != nil || len(msgs) != 5 {
		t.Fatalf("messages: %v, %v", msgs, err)
	}
	if len(chunks) != 2 || strings.Join(chunks, "") != result.AssistantMessage.Content {
		t.Fatalf("chunks %v, result %+v", chunks, result)
	}
	if msgs[3].Mode != session.ModeCheckpoint || msgs[3].Content != "handoff" || msgs[4].ID != result.AssistantMessage.ID {
		t.Fatalf("commit order: %v", msgs)
	}
	updated, err := mem.GetSession(ctx, sess.ID)
	if err != nil || !updated.UpdatedAt.Equal(result.AssistantMessage.CreatedAt) {
		t.Fatalf("session timestamp: %+v, %v", updated, err)
	}
}

// 失败终态即使携带正文和检查点也不能落库，消费错误即使被上游吞掉也仍然失败
func TestTurnStreamAbort(t *testing.T) {
	failure := errors.New("stream failed")
	for _, name := range []string{"upstream", "consumer", "cancel", "cancel without delta"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			mem := store.NewMemoryStore()
			responder := scriptedStreamResponder{stream: func(streamCtx context.Context, _ session.RespondInput, emit func(string) error) (session.RespondOutput, error) {
				out := session.RespondOutput{Content: "partial", CheckpointContent: "handoff"}
				if name == "cancel without delta" {
					cancel()
					return out, nil
				}
				err := emit("partial")
				if name == "upstream" {
					return out, failure
				}
				if err == nil || streamCtx.Err() == nil {
					t.Fatalf("consumer failure must cancel upstream: %v, %v", err, streamCtx.Err())
				}
				// 模拟错误实现继续发片段且返回成功，会话层仍须拒绝提交
				if err := emit("ignored"); err == nil {
					t.Fatal("lost consumer error")
				}
				return out, nil
			}}
			svc := session.NewService(mem, newTestFactory(), responder)
			sess, err := svc.NewSession(ctx)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			result, err := svc.TurnStream(ctx, sess.ID, "question", func(string) error {
				calls++
				if name == "consumer" {
					return failure
				}
				if name == "cancel" {
					cancel()
				}
				return nil
			})
			want := failure
			if strings.HasPrefix(name, "cancel") {
				want = context.Canceled
			}
			if !errors.Is(err, want) || result.AssistantMessage.ID != "" || calls > 1 {
				t.Fatalf("result %+v, error %v, calls %d", result, err, calls)
			}
			msgs, listErr := mem.ListMessages(t.Context(), sess.ID)
			if listErr != nil || len(msgs) != 1 || msgs[0].Role != session.RoleUser {
				t.Fatalf("failed turn committed output: %v, %v", msgs, listErr)
			}
		})
	}
}

// 诊断与澄清可以不发增量，完整终态仍保留模式、运行编号和报告
func TestTurnStreamStructuredResult(t *testing.T) {
	for _, mode := range []string{session.ModeDiagnostic, session.ModeClarify} {
		t.Run(mode, func(t *testing.T) {
			out := session.RespondOutput{Content: "complete", Mode: mode, RunID: "run_test"}
			if mode == session.ModeDiagnostic {
				out.Report = &core.Report{RunID: out.RunID, Summary: "complete"}
			}
			responder := scriptedStreamResponder{stream: func(context.Context, session.RespondInput, func(string) error) (session.RespondOutput, error) {
				return out, nil
			}}
			svc := session.NewService(store.NewMemoryStore(), newTestFactory(), responder)
			sess, err := svc.NewSession(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			result, err := svc.TurnStream(t.Context(), sess.ID, "question", func(string) error {
				t.Fatal("unexpected structured delta")
				return nil
			})
			if err != nil || result.RunID != out.RunID || result.Report != out.Report || result.AssistantMessage.Mode != mode {
				t.Fatalf("result %+v, error %v", result, err)
			}
		})
	}
}

// 不支持流式及缺失消费者在任何消息写入前失败
func TestTurnStreamValidation(t *testing.T) {
	mem := store.NewMemoryStore()
	svc := session.NewService(mem, newTestFactory(), session.EchoResponder{})
	sess, err := svc.NewSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, emit := range []func(string) error{nil, func(string) error { return nil }} {
		if _, err = svc.TurnStream(t.Context(), sess.ID, "question", emit); err == nil {
			t.Fatal("expected validation error")
		}
	}
	msgs, err := mem.ListMessages(t.Context(), sess.ID)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("validation wrote messages: %v, %v", msgs, err)
	}
}
