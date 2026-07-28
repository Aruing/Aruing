package agent

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"aruing/internal/core"
	"aruing/internal/session"
	"aruing/internal/store"
)

// 假诊断管道：记录收到的 Run，并返回固定报告
type fakeRunExecutor struct {
	lastRun core.Run
	report  core.Report
	err     error
}

func (f *fakeRunExecutor) Execute(_ context.Context, run core.Run) (core.Report, []core.Evidence, error) {
	f.lastRun = run
	if f.err != nil {
		return core.Report{}, nil, f.err
	}
	rep := f.report
	rep.RunID = run.ID
	return rep, nil, nil
}

// Fake reply：基线 Mode，无 Run
func TestFakeTowerReply(t *testing.T) {
	ctx := context.Background()
	factory := newTestFactory(t)
	mem := store.NewMemoryStore()
	tower := &FakeTowerResponder{
		Factory: factory,
		Decide: func(in session.RespondInput) (string, string, string) {
			return towerActionReply, "你好，这是基线回答", ""
		},
	}
	svc := session.NewService(mem, factory, tower)

	sess, err := svc.NewSession(ctx)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	result, err := svc.Turn(ctx, sess.ID, "k8s 是什么")
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	if result.AssistantMessage.Mode != session.ModeBaseline {
		t.Fatalf("mode: %q", result.AssistantMessage.Mode)
	}
	if result.RunID != "" || result.AssistantMessage.RunID != "" {
		t.Fatalf("reply should not set run id")
	}
	if result.AssistantMessage.Content != "你好，这是基线回答" {
		t.Fatalf("content: %q", result.AssistantMessage.Content)
	}
}

// Fake escalate：Run.SessionID 写入，Mode=diagnostic
func TestFakeTowerEscalate(t *testing.T) {
	ctx := context.Background()
	factory := newTestFactory(t)
	mem := store.NewMemoryStore()
	exec := &fakeRunExecutor{
		report: core.Report{Title: "根因报告", Summary: "Pod 未就绪"},
	}
	tower := &FakeTowerResponder{
		Factory:  factory,
		Executor: exec,
		Decide: func(in session.RespondInput) (string, string, string) {
			return towerActionEscalate, "", "定位 demo-api 故障"
		},
	}
	svc := session.NewService(mem, factory, tower)

	sess, err := svc.NewSession(ctx)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	result, err := svc.Turn(ctx, sess.ID, "demo-api 访问不了")
	if err != nil {
		t.Fatalf("turn: %v", err)
	}
	if result.RunID == "" || !strings.HasPrefix(result.RunID, "run_") {
		t.Fatalf("run id: %q", result.RunID)
	}
	if result.AssistantMessage.Mode != session.ModeDiagnostic {
		t.Fatalf("mode: %q", result.AssistantMessage.Mode)
	}
	if exec.lastRun.SessionID != sess.ID {
		t.Fatalf("session id: got %q want %q", exec.lastRun.SessionID, sess.ID)
	}
	if exec.lastRun.Question != "定位 demo-api 故障" {
		t.Fatalf("question: %q", exec.lastRun.Question)
	}
	if !strings.Contains(result.AssistantMessage.Content, "Pod 未就绪") {
		t.Fatalf("content: %q", result.AssistantMessage.Content)
	}
}

// escalate 且 question 空时回退 UserText
func TestFakeTowerEscalateQuestionFallback(t *testing.T) {
	ctx := context.Background()
	factory := newTestFactory(t)
	exec := &fakeRunExecutor{report: core.Report{Summary: "ok"}}
	tower := &FakeTowerResponder{
		Factory:  factory,
		Executor: exec,
		Decide: func(in session.RespondInput) (string, string, string) {
			return towerActionEscalate, "", ""
		},
	}

	out, err := tower.Respond(ctx, session.RespondInput{
		SessionID: "sess_1",
		UserText:  "用户原问",
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if exec.lastRun.Question != "用户原问" {
		t.Fatalf("question: %q", exec.lastRun.Question)
	}
	if out.Mode != session.ModeDiagnostic {
		t.Fatalf("mode: %q", out.Mode)
	}
}

// mock LLM 合法 reply JSON
func TestTowerLLMReply(t *testing.T) {
	body := `{"action":"reply","content":"这是概念解释","question":""}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	exec := &fakeRunExecutor{}
	tower, err := NewTowerResponder(client, newTestFactory(t), exec)
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}

	out, err := tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_x",
		UserText:  "什么是 Deployment",
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Mode != session.ModeBaseline || out.RunID != "" {
		t.Fatalf("output: %+v", out)
	}
	if out.Content != "这是概念解释" {
		t.Fatalf("content: %q", out.Content)
	}
	if exec.lastRun.ID != "" {
		t.Fatal("execute should not be called on reply")
	}
}

// mock LLM escalate
func TestTowerLLMEscalate(t *testing.T) {
	body := `{"action":"escalate","content":"","question":"查 demo-api 根因"}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	exec := &fakeRunExecutor{
		report: core.Report{Title: "T", Summary: "S"},
	}
	factory := newTestFactory(t)
	tower, err := NewTowerResponder(client, factory, exec)
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}

	out, err := tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_y",
		UserText:  "demo-api 挂了",
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Mode != session.ModeDiagnostic || out.RunID == "" {
		t.Fatalf("output: %+v", out)
	}
	if exec.lastRun.SessionID != "sess_y" {
		t.Fatalf("session: %q", exec.lastRun.SessionID)
	}
	if exec.lastRun.Question != "查 demo-api 根因" {
		t.Fatalf("question: %q", exec.lastRun.Question)
	}
}

// 非法 action 持续不合规 → ErrLLMOutputInconsistent
func TestTowerLLMInvalidActionRetries(t *testing.T) {
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeChatCompletion(w, `{"action":"fly","content":"x"}`)
	})
	tower, err := NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{})
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}

	_, err = tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_z",
		UserText:  "hi",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Fatalf("want ErrLLMOutputInconsistent, got %v", err)
	}
	if n := calls.Load(); n != maxTowerAttempts {
		t.Fatalf("attempts: %d want %d", n, maxTowerAttempts)
	}
}

// reply 空 content 应业务重试后失败
func TestTowerLLMEmptyReplyContent(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"action":"reply","content":"  "}`)
	})
	tower, err := NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{})
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}
	_, err = tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_z",
		UserText:  "hi",
	})
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Fatalf("want ErrLLMOutputInconsistent, got %v", err)
	}
}

func TestNewTowerResponderRequiresDeps(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{}`)
	})
	factory := newTestFactory(t)
	exec := &fakeRunExecutor{}

	if _, err := NewTowerResponder(nil, factory, exec); err == nil {
		t.Fatal("expected nil client error")
	}
	if _, err := NewTowerResponder(client, nil, exec); err == nil {
		t.Fatal("expected nil factory error")
	}
	if _, err := NewTowerResponder(client, factory, nil); err == nil {
		t.Fatal("expected nil executor error")
	}
}

func TestValidateTowerDecision(t *testing.T) {
	if err := validateTowerDecision(towerDecision{Action: "reply", Content: "ok"}); err != nil {
		t.Fatalf("valid reply: %v", err)
	}
	if err := validateTowerDecision(towerDecision{Action: "escalate"}); err != nil {
		t.Fatalf("valid escalate: %v", err)
	}
	if err := validateTowerDecision(towerDecision{Action: "reply"}); err == nil {
		t.Fatal("empty reply content should fail")
	}
	if err := validateTowerDecision(towerDecision{Action: "other"}); err == nil {
		t.Fatal("invalid action should fail")
	}
}
