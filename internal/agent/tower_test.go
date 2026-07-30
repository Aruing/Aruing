package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"aruing/internal/core"
	"aruing/internal/session"
	"aruing/internal/store"
	"aruing/internal/tools"
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

// Fake call_tool 后 reply：经 Dispatcher，RunID 空，最终 baseline 无 Run
func TestFakeTowerCallToolThenReply(t *testing.T) {
	ctx := context.Background()
	factory := newTestFactory(t)
	registry := tools.NewRegistry()
	if err := registry.Register(tools.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatcher := tools.NewDispatcher(registry, tools.NewReadonlyPolicy())

	var steps atomic.Int32
	tower := &FakeTowerResponder{
		Factory:    factory,
		Dispatcher: dispatcher,
		CallTool: towerToolCall{
			ToolName:  "fake.list_pods",
			Arguments: json.RawMessage(`{}`),
			Purpose:   "查 demo-api pod",
		},
		Decide: func(in session.RespondInput) (string, string, string) {
			if steps.Add(1) == 1 {
				return towerActionCallTool, "", ""
			}
			return towerActionReply, "根据查询，Pod 未就绪", ""
		},
	}

	out, err := tower.Respond(ctx, session.RespondInput{
		SessionID: "sess_tool",
		UserText:  "demo-api 状态如何",
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Mode != session.ModeBaseline || out.RunID != "" {
		t.Fatalf("output: %+v", out)
	}
	if out.Content != "根据查询，Pod 未就绪" {
		t.Fatalf("content: %q", out.Content)
	}
	if steps.Load() < 2 {
		t.Fatalf("expected at least 2 decide steps, got %d", steps.Load())
	}
}

// mock LLM 合法 reply JSON
func TestTowerLLMReply(t *testing.T) {
	body := `{"action":"reply","content":"这是概念解释","question":""}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	exec := &fakeRunExecutor{}
	tower, err := NewTowerResponder(client, newTestFactory(t), exec, nil, nil)
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

// 有 prior 诊断时解释追问 → reply，不调用诊断管道
func TestTowerLLMExplainPriorReplyNoEscalate(t *testing.T) {
	var sawUser string
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		// 捕获 user 载荷，确认含 prior_diagnostics
		var reqBody struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		for _, m := range reqBody.Messages {
			if m.Role == "user" {
				sawUser = m.Content
			}
		}
		writeChatCompletion(w, `{"action":"reply","content":"上次依据 ImagePullBackOff 判定镜像问题","question":""}`)
	})
	exec := &fakeRunExecutor{}
	tower, err := NewTowerResponder(client, newTestFactory(t), exec, nil, nil)
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}

	out, err := tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_explain",
		UserText:  "为什么上次那么判断",
		History: []session.Message{
			{Role: session.RoleUser, Content: "demo-api 挂了"},
			{
				Role:    session.RoleAssistant,
				Content: "根因：ImagePullBackOff",
				Mode:    session.ModeDiagnostic,
				RunID:   "run_prior",
			},
		},
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Mode != session.ModeBaseline {
		t.Fatalf("mode: %q", out.Mode)
	}
	if exec.lastRun.ID != "" {
		t.Fatal("executor must not run for explain reply")
	}
	if !strings.Contains(sawUser, "prior_diagnostics") || !strings.Contains(sawUser, "run_prior") {
		t.Fatalf("user payload should include prior: %s", sawUser)
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
	tower, err := NewTowerResponder(client, factory, exec, nil, nil)
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

// mock LLM：先 call_tool 再 reply；空 RunID 观察不落 Message
func TestTowerLLMCallToolThenReply(t *testing.T) {
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			writeChatCompletion(w, `{"action":"call_tool","tool_call":{"tool_name":"fake.list_pods","arguments":{},"purpose":"查 pod 状态"}}`)
			return
		}
		writeChatCompletion(w, `{"action":"reply","content":"Pod 处于 CrashLoopBackOff","question":""}`)
	})

	registry := tools.NewRegistry()
	if err := registry.Register(tools.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatcher := tools.NewDispatcher(registry, tools.NewReadonlyPolicy())
	specs := registry.Specs()

	exec := &fakeRunExecutor{}
	tower, err := NewTowerResponder(client, newTestFactory(t), exec, dispatcher, specs)
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}

	out, err := tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_ct",
		UserText:  "demo-api pod 怎样了",
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Mode != session.ModeBaseline || out.RunID != "" {
		t.Fatalf("output: %+v", out)
	}
	if !strings.Contains(out.Content, "CrashLoopBackOff") {
		t.Fatalf("content: %q", out.Content)
	}
	if calls.Load() < 2 {
		t.Fatalf("llm calls: %d", calls.Load())
	}
	if exec.lastRun.ID != "" {
		t.Fatal("diagnostic executor should not run")
	}
}

// 非法 action 持续不合规 → ErrLLMOutputInconsistent
func TestTowerLLMInvalidActionRetries(t *testing.T) {
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeChatCompletion(w, `{"action":"fly","content":"x"}`)
	})
	tower, err := NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, nil, nil)
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
	tower, err := NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, nil, nil)
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

// 无 dispatcher 时 call_tool 应业务重试失败
func TestTowerLLMCallToolWithoutDispatcher(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"action":"call_tool","tool_call":{"tool_name":"fake.list_pods","arguments":{},"purpose":"x"}}`)
	})
	tower, err := NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, nil, []tools.ToolSpec{{
		Name:        "fake.list_pods",
		Description: "d",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}})
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

	if _, err := NewTowerResponder(nil, factory, exec, nil, nil); err == nil {
		t.Fatal("expected nil client error")
	}
	if _, err := NewTowerResponder(client, nil, exec, nil, nil); err == nil {
		t.Fatal("expected nil factory error")
	}
	if _, err := NewTowerResponder(client, factory, nil, nil, nil); err == nil {
		t.Fatal("expected nil executor error")
	}
}

// Summary 无业务标记、Raw 含唯一串时，第二轮 user JSON 必须回喂该串
func TestTowerLLMCallToolFeedsRawIntoObservations(t *testing.T) {
	const mark = "NS_MARK_raw_feed_42"
	var secondUser string
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			writeChatCompletion(w, `{"action":"call_tool","tool_call":{"tool_name":"fake.raw_only","arguments":{},"purpose":"取 raw"}}`)
			return
		}
		var reqBody struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		for _, m := range reqBody.Messages {
			if m.Role == "user" {
				secondUser = m.Content
			}
		}
		writeChatCompletion(w, `{"action":"reply","content":"ok","question":""}`)
	})

	registry := tools.NewRegistry()
	if err := registry.Register(&rawOnlyFakeTool{mark: mark}); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatcher := tools.NewDispatcher(registry, tools.NewReadonlyPolicy())
	specs := registry.Specs()

	tower, err := NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, dispatcher, specs)
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}

	out, err := tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_raw",
		UserText:  "集群里有什么",
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Mode != session.ModeBaseline {
		t.Fatalf("mode: %q", out.Mode)
	}
	if !strings.Contains(secondUser, mark) {
		t.Fatalf("second-round user payload must include raw mark %q; got: %s", mark, secondUser)
	}
	if !strings.Contains(secondUser, `"raw"`) {
		t.Fatalf("payload should include raw field: %s", secondUser)
	}
	// Summary 故意无 mark，确保不是从 summary 漏进
	if strings.Contains(secondUser, `"summary":"NS_MARK`) {
		t.Fatal("mark must not appear only via summary")
	}
}

// 超预算 raw：注入副本 rawTruncated；权威观测仍保留完整 Raw
func TestPrepareTowerObservationsForPromptTruncatesRaw(t *testing.T) {
	// 远超 budget=10 token（~40 runes）的 raw
	big := strings.Repeat("A", 500)
	raw := json.RawMessage(`{"stdout":"` + big + `"}`)
	auth := []towerObservation{{
		TaskID:   "t_1",
		ToolName: "k8s",
		Summary:  "exitCode=0",
		Raw:      append(json.RawMessage(nil), raw...),
	}}

	view := prepareTowerObservationsForPrompt(auth, 10)
	if len(view) != 1 {
		t.Fatalf("len: %d", len(view))
	}
	if !view[0].RawTruncated {
		t.Fatal("expected rawTruncated on injection copy")
	}
	if estimateTokens(string(view[0].Raw)) > 200 {
		// 截断后应明显小于原文；wrapped JSON 仍可控
		t.Fatalf("injected raw still huge: %d tokens", estimateTokens(string(view[0].Raw)))
	}
	if !strings.Contains(string(view[0].Raw), "truncated") {
		t.Fatalf("injected raw should note truncation: %s", view[0].Raw)
	}
	// 权威切片未改
	if auth[0].RawTruncated {
		t.Fatal("authoritative observation must not set rawTruncated")
	}
	if string(auth[0].Raw) != string(raw) {
		t.Fatalf("authoritative raw mutated")
	}

	// 预算内全文
	small := []towerObservation{{
		ToolName: "k8s",
		Raw:      json.RawMessage(`{"stdout":"hi","exitCode":0}`),
	}}
	full := prepareTowerObservationsForPrompt(small, 8_000)
	if full[0].RawTruncated {
		t.Fatal("small raw should not truncate")
	}
	if string(full[0].Raw) != string(small[0].Raw) {
		t.Fatalf("small raw: got %s", full[0].Raw)
	}
}

// Summary 无业务标记；业务事实只在 Raw
type rawOnlyFakeTool struct {
	mark string
}

func (t *rawOnlyFakeTool) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "fake.raw_only",
		Description: "returns exitCode-style summary and raw with stdout",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
	}
}

func (t *rawOnlyFakeTool) Execute(_ context.Context, _ json.RawMessage) (*core.Evidence, error) {
	mark := t.mark
	if mark == "" {
		mark = "NS_MARK_default"
	}
	raw, err := json.Marshal(map[string]any{
		"stdout":   mark + "\n",
		"stderr":   "",
		"exitCode": 0,
	})
	if err != nil {
		return nil, err
	}
	return &core.Evidence{
		Source:      "fake",
		ToolName:    "fake.raw_only",
		CommandView: "fake raw-only",
		Summary:     "tool completed, exitCode=0",
		Raw:         raw,
	}, nil
}

func TestValidateTowerDecision(t *testing.T) {
	specs := []tools.ToolSpec{{
		Name:        "fake.list_pods",
		Description: "d",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}
	registry := tools.NewRegistry()
	if err := registry.Register(tools.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	tower := &TowerResponder{
		dispatcher: tools.NewDispatcher(registry, nil),
		specs:      specs,
	}

	if err := tower.validateTowerDecision(towerLLMOutput{Action: "reply", Content: "ok"}); err != nil {
		t.Fatalf("valid reply: %v", err)
	}
	if err := tower.validateTowerDecision(towerLLMOutput{Action: "escalate"}); err != nil {
		t.Fatalf("valid escalate: %v", err)
	}
	if err := tower.validateTowerDecision(towerLLMOutput{
		Action: "call_tool",
		ToolCall: &towerToolCallJSON{
			ToolName:  "fake.list_pods",
			Arguments: json.RawMessage(`{}`),
			Purpose:   "查",
		},
	}); err != nil {
		t.Fatalf("valid call_tool: %v", err)
	}
	if err := tower.validateTowerDecision(towerLLMOutput{Action: "reply"}); err == nil {
		t.Fatal("empty reply content should fail")
	}
	if err := tower.validateTowerDecision(towerLLMOutput{Action: "other"}); err == nil {
		t.Fatal("invalid action should fail")
	}
	if err := tower.validateTowerDecision(towerLLMOutput{
		Action: "call_tool",
		ToolCall: &towerToolCallJSON{
			ToolName:  "not.registered",
			Arguments: json.RawMessage(`{}`),
		},
	}); err == nil {
		t.Fatal("unknown tool should fail")
	}
}
