package session_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"aruing/internal/core"
	"aruing/internal/session"
	"aruing/internal/store"
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

func (f *fakeExecutor) Execute(_ context.Context, run core.Run) (core.Report, []core.Evidence, error) {
	f.lastRun = run
	if f.err != nil {
		return core.Report{}, nil, f.err
	}
	rep := f.report
	rep.RunID = run.ID
	return rep, nil, nil
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
