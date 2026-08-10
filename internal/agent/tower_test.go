package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"aruing/internal/agent"
	"aruing/internal/agent/agenttest"
	"aruing/internal/core"
	"aruing/internal/llm"
	"aruing/internal/session"
	"aruing/internal/store"
	"aruing/internal/tools"
	"aruing/internal/tools/toolstest"
)

func writeChatCompletion(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{
			{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": content},
				"finish_reason": "stop",
			},
		},
	})
}

func newMockLLMClient(t *testing.T, handler http.HandlerFunc) llm.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c, err := llm.NewClient(llm.Config{BaseURL: server.URL, APIKey: "test-key", Model: "test-model"})
	if err != nil {
		t.Fatalf("new llm client: %v", err)
	}
	return c
}

func newTestFactory(t *testing.T) *core.Factory {
	t.Helper()
	return core.NewFactory()
}

// 与基线塔最大尝试次数对齐（未导出，黑盒测试用字面量）
const maxTowerAttempts = 3

// 假诊断管道：记录收到的运行，并返回固定报告
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

// 假塔直接回复：基线模式且无诊断运行
func TestFakeTowerReply(t *testing.T) {
	ctx := context.Background()
	factory := newTestFactory(t)
	mem := store.NewMemoryStore()
	tower := &agenttest.FakeTowerResponder{
		Factory: factory,
		Decide: func(in session.RespondInput) (string, string, string) {
			return "reply", "你好，这是基线回答", ""
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

// 假塔升格：诊断运行绑定会话编号，模式为诊断
func TestFakeTowerEscalate(t *testing.T) {
	ctx := context.Background()
	factory := newTestFactory(t)
	mem := store.NewMemoryStore()
	exec := &fakeRunExecutor{
		report: core.Report{Title: "根因报告", Summary: "Pod 未就绪"},
	}
	ledger := store.NewMemoryRunLedger()
	tower := &agenttest.FakeTowerResponder{
		Factory:  factory,
		Executor: exec,
		Ledger:   ledger,
		Decide: func(session.RespondInput) (string, string, string) {
			return "escalate", "", "定位 demo-api 故障"
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
	rec, err := ledger.Get(ctx, result.RunID)
	if err != nil {
		t.Fatalf("ledger get: %v", err)
	}
	if rec.SessionID != sess.ID || rec.Report.Summary != "Pod 未就绪" {
		t.Fatalf("ledger: %+v", rec)
	}
}

// 升格且问题字段空时回退用户原文
func TestFakeTowerEscalateQuestionFallback(t *testing.T) {
	ctx := context.Background()
	factory := newTestFactory(t)
	exec := &fakeRunExecutor{report: core.Report{Summary: "ok"}}
	tower := &agenttest.FakeTowerResponder{
		Factory:  factory,
		Executor: exec,
		Ledger:   store.NewMemoryRunLedger(),
		Decide: func(session.RespondInput) (string, string, string) {
			return "escalate", "", ""
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

// 假塔先调工具再回复：经调度器取证，最终基线且无诊断运行
func TestFakeTowerCallToolThenReply(t *testing.T) {
	ctx := context.Background()
	factory := newTestFactory(t)
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatcher := tools.NewDispatcher(registry, tools.NewReadonlyPolicy())

	var steps atomic.Int32
	tower := &agenttest.FakeTowerResponder{
		Factory:    factory,
		Dispatcher: dispatcher,
		CallTool: agenttest.ToolCall{
			ToolName:  "fake.list_pods",
			Arguments: json.RawMessage(`{}`),
			Purpose:   "查 demo-api pod",
		},
		Decide: func(in session.RespondInput) (string, string, string) {
			if steps.Add(1) == 1 {
				return "call_tool", "", ""
			}
			return "reply", "根据查询，Pod 未就绪", ""
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

// 调用工具触顶后自动升格，不返回预算错误；问题字段用用户原文
func TestFakeTowerToolBudgetAutoEscalate(t *testing.T) {
	ctx := context.Background()
	factory := newTestFactory(t)
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatcher := tools.NewDispatcher(registry, tools.NewReadonlyPolicy())
	exec := &fakeRunExecutor{
		report: core.Report{Title: "升格报告", Summary: "经正式管道"},
	}

	tower := &agenttest.FakeTowerResponder{
		Factory:               factory,
		Executor:              exec,
		Ledger:                store.NewMemoryRunLedger(),
		Dispatcher:            dispatcher,
		BaselineMaxToolRounds: 2,
		CallTool: agenttest.ToolCall{
			ToolName:  "fake.list_pods",
			Arguments: json.RawMessage(`{}`),
			Purpose:   "观察",
		},
		Decide: func(in session.RespondInput) (string, string, string) {
			return "call_tool", "", ""
		},
	}

	out, err := tower.Respond(ctx, session.RespondInput{
		SessionID: "sess_budget",
		UserText:  "demo 为什么挂了",
	})
	if err != nil {
		t.Fatalf("respond must not fail on budget: %v", err)
	}
	if out.Mode != session.ModeDiagnostic {
		t.Fatalf("mode: %q want diagnostic", out.Mode)
	}
	if out.RunID == "" {
		t.Fatal("expected run id after budget escalate")
	}
	if exec.lastRun.Question != "demo 为什么挂了" {
		t.Fatalf("question: %q", exec.lastRun.Question)
	}
	if exec.lastRun.SessionID != "sess_budget" {
		t.Fatalf("session: %q", exec.lastRun.SessionID)
	}
	if strings.Contains(strings.ToLower(out.Content), "budget") {
		t.Fatalf("user-facing content must not mention budget: %q", out.Content)
	}
}

// 大模型路径：工具轮次触顶后自动升格，用户可见内容不含预算错误
func TestTowerLLMToolBudgetAutoEscalate(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"action":"call_tool","tool_call":{"tool_name":"fake.list_pods","arguments":{},"purpose":"再查"}}`)
	})

	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatcher := tools.NewDispatcher(registry, tools.NewReadonlyPolicy())
	exec := &fakeRunExecutor{
		report: core.Report{Title: "正式", Summary: "诊断完成"},
	}

	tower, err := agent.NewTowerResponder(client, newTestFactory(t), exec, store.NewMemoryRunLedger(), dispatcher, registry.Specs())
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}
	tower.SetBaselineMaxToolRounds(1)

	out, err := tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_llm_budget",
		UserText:  "服务访问不了",
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Mode != session.ModeDiagnostic || out.RunID == "" {
		t.Fatalf("want diagnostic escalate, got %+v", out)
	}
	if exec.lastRun.Question != "服务访问不了" {
		t.Fatalf("question: %q", exec.lastRun.Question)
	}
	if strings.Contains(strings.ToLower(out.Content), "budget") {
		t.Fatalf("user-facing content must not mention budget: %q", out.Content)
	}
}

// 模拟大模型返回合法回复动作
func TestTowerLLMReply(t *testing.T) {
	body := `{"action":"reply","content":"这是概念解释","question":""}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	exec := &fakeRunExecutor{}
	tower, err := agent.NewTowerResponder(client, newTestFactory(t), exec, store.NewMemoryRunLedger(), nil, nil)
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

// 有历史诊断时解释追问应直接回复，不调用诊断管道
func TestTowerLLMExplainPriorReplyNoEscalate(t *testing.T) {
	var sawUser string
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		// 捕获用户载荷，确认含既往诊断
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
	tower, err := agent.NewTowerResponder(client, newTestFactory(t), exec, store.NewMemoryRunLedger(), nil, nil)
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

// 模拟大模型返回升格动作
func TestTowerLLMEscalate(t *testing.T) {
	body := `{"action":"escalate","content":"","question":"查 demo-api 根因"}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	exec := &fakeRunExecutor{
		report: core.Report{Title: "T", Summary: "S"},
	}
	factory := newTestFactory(t)
	ledger := store.NewMemoryRunLedger()
	tower, err := agent.NewTowerResponder(client, factory, exec, ledger, nil, nil)
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
	rec, err := ledger.Get(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("ledger get: %v", err)
	}
	if rec.Report.Summary != "S" {
		t.Fatalf("ledger report: %+v", rec.Report)
	}
}

// 模拟大模型先调工具再回复；空运行编号的观察不落会话消息
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
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatcher := tools.NewDispatcher(registry, tools.NewReadonlyPolicy())
	specs := registry.Specs()

	exec := &fakeRunExecutor{}
	tower, err := agent.NewTowerResponder(client, newTestFactory(t), exec, store.NewMemoryRunLedger(), dispatcher, specs)
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

// 非法动作持续不合规应返回模型输出不一致错误
func TestTowerLLMInvalidActionRetries(t *testing.T) {
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeChatCompletion(w, `{"action":"fly","content":"x"}`)
	})
	tower, err := agent.NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, store.NewMemoryRunLedger(), nil, nil)
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
	if !errors.Is(err, agent.ErrLLMOutputInconsistent) {
		t.Fatalf("want agent.ErrLLMOutputInconsistent, got %v", err)
	}
	if n := calls.Load(); n != maxTowerAttempts {
		t.Fatalf("attempts: %d want %d", n, maxTowerAttempts)
	}
}

// 非结构化正文应业务重试后失败，错误链含解析失败
func TestTowerLLMBadJSONRetries(t *testing.T) {
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeChatCompletion(w, "not-json-at-all")
	})
	tower, err := agent.NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, store.NewMemoryRunLedger(), nil, nil)
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}
	var prog bytes.Buffer
	tower.SetProgress(&prog)

	_, err = tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_z",
		UserText:  "hi",
	})
	if !errors.Is(err, agent.ErrLLMOutputInconsistent) {
		t.Fatalf("want agent.ErrLLMOutputInconsistent, got %v", err)
	}
	if !errors.Is(err, llm.ErrJSONParse) {
		t.Fatalf("want ErrJSONParse in chain, got %v", err)
	}
	if n := calls.Load(); n != maxTowerAttempts {
		t.Fatalf("attempts: %d want %d", n, maxTowerAttempts)
	}
	if !strings.Contains(prog.String(), "recoverable LLM error") {
		t.Fatalf("progress missing retry log: %q", prog.String())
	}
}

// 坏结构后恢复成功应返回回复内容
func TestTowerLLMBadJSONThenOK(t *testing.T) {
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeChatCompletion(w, "garbage")
			return
		}
		writeChatCompletion(w, `{"action":"reply","content":"recovered"}`)
	})
	tower, err := agent.NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, store.NewMemoryRunLedger(), nil, nil)
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}
	out, err := tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_z",
		UserText:  "hi",
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Content != "recovered" {
		t.Fatalf("content = %q", out.Content)
	}
}

// 回复动作为空正文应业务重试后失败
func TestTowerLLMEmptyReplyContent(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"action":"reply","content":"  "}`)
	})
	tower, err := agent.NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, store.NewMemoryRunLedger(), nil, nil)
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}
	_, err = tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_z",
		UserText:  "hi",
	})
	if !errors.Is(err, agent.ErrLLMOutputInconsistent) {
		t.Fatalf("want agent.ErrLLMOutputInconsistent, got %v", err)
	}
}

// 无调度器时调用工具应业务重试失败
func TestTowerLLMCallToolWithoutDispatcher(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"action":"call_tool","tool_call":{"tool_name":"fake.list_pods","arguments":{},"purpose":"x"}}`)
	})
	tower, err := agent.NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, store.NewMemoryRunLedger(), nil, []tools.ToolSpec{{
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
	if !errors.Is(err, agent.ErrLLMOutputInconsistent) {
		t.Fatalf("want agent.ErrLLMOutputInconsistent, got %v", err)
	}
}

func TestNewTowerResponderRequiresDeps(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{}`)
	})
	factory := newTestFactory(t)
	exec := &fakeRunExecutor{}

	if _, err := agent.NewTowerResponder(nil, factory, exec, store.NewMemoryRunLedger(), nil, nil); err == nil {
		t.Fatal("expected nil client error")
	}
	if _, err := agent.NewTowerResponder(client, nil, exec, store.NewMemoryRunLedger(), nil, nil); err == nil {
		t.Fatal("expected nil factory error")
	}
	if _, err := agent.NewTowerResponder(client, factory, nil, store.NewMemoryRunLedger(), nil, nil); err == nil {
		t.Fatal("expected nil executor error")
	}
	if _, err := agent.NewTowerResponder(client, factory, exec, nil, nil, nil); err == nil {
		t.Fatal("expected nil ledger error")
	}
}

// 摘要无业务标记而原始输出含唯一串时，第二轮用户载荷必须回喂该串
func TestTowerLLMCallToolFeedsRaw(t *testing.T) {
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

	tower, err := agent.NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, store.NewMemoryRunLedger(), dispatcher, specs)
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
	// 摘要故意无标记，确保不是从摘要漏进
	if strings.Contains(secondUser, `"summary":"NS_MARK`) {
		t.Fatal("mark must not appear only via summary")
	}
}

// 集群工具可用时基线每轮一次资源清单侦察，载荷含集群资源（含自定义资源）
func TestTowerBaselineReconInjectsClusterResources(t *testing.T) {
	var firstUser string
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		var reqBody struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		for _, m := range reqBody.Messages {
			if m.Role == "user" && firstUser == "" {
				firstUser = m.Content
			}
		}
		writeChatCompletion(w, `{"action":"reply","content":"看到集群类型清单","question":""}`)
	})

	registry := tools.NewRegistry()
	if err := registry.Register(&fakeK8sAPIResourcesTool{
		stdout: "NAME           SHORTNAMES   NAMESPACED   KIND\n" +
			"pods           po           true         Pod\n" +
			"ingressroutes  ico          true         IngressRoute\n",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatcher := tools.NewDispatcher(registry, tools.NewReadonlyPolicy())
	tower, err := agent.NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, store.NewMemoryRunLedger(), dispatcher, registry.Specs())
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}

	out, err := tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_recon",
		UserText:  "集群入口资源有哪些",
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Mode != session.ModeBaseline {
		t.Fatalf("mode: %q", out.Mode)
	}
	if !strings.Contains(firstUser, `"cluster_resources"`) {
		t.Fatalf("payload missing cluster_resources: %s", firstUser)
	}
	if !strings.Contains(firstUser, "IngressRoute") {
		t.Fatalf("payload missing CRD kind IngressRoute: %s", firstUser)
	}
	// 正式升格未跑；侦察不落运行
	if out.RunID != "" {
		t.Fatalf("baseline recon must not create run: %q", out.RunID)
	}
}

// 无集群工具时不尝试侦察，载荷无集群资源
func TestTowerBaselineReconSkippedWithoutK8s(t *testing.T) {
	var userPayload string
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		var reqBody struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		for _, m := range reqBody.Messages {
			if m.Role == "user" {
				userPayload = m.Content
			}
		}
		writeChatCompletion(w, `{"action":"reply","content":"ok","question":""}`)
	})

	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatcher := tools.NewDispatcher(registry, tools.NewReadonlyPolicy())
	tower, err := agent.NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, store.NewMemoryRunLedger(), dispatcher, registry.Specs())
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}

	if _, err := tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_norecon",
		UserText:  "hi",
	}); err != nil {
		t.Fatalf("respond: %v", err)
	}
	if strings.Contains(userPayload, `"cluster_resources"`) {
		t.Fatalf("without k8s, payload must omit cluster_resources: %s", userPayload)
	}
}

// 侦察工具失败时降级空列表，仍可直接回复
func TestTowerBaselineReconFailureDegrades(t *testing.T) {
	var userPayload string
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		var reqBody struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		for _, m := range reqBody.Messages {
			if m.Role == "user" {
				userPayload = m.Content
			}
		}
		writeChatCompletion(w, `{"action":"reply","content":"仍可回答","question":""}`)
	})

	registry := tools.NewRegistry()
	if err := registry.Register(&fakeK8sAPIResourcesTool{fail: true}); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatcher := tools.NewDispatcher(registry, tools.NewReadonlyPolicy())
	tower, err := agent.NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, store.NewMemoryRunLedger(), dispatcher, registry.Specs())
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}

	out, err := tower.Respond(context.Background(), session.RespondInput{
		SessionID: "sess_recon_fail",
		UserText:  "hi",
	})
	if err != nil {
		t.Fatalf("respond should not fail on recon error: %v", err)
	}
	if out.Content != "仍可回答" {
		t.Fatalf("content: %q", out.Content)
	}
	if strings.Contains(userPayload, `"cluster_resources"`) {
		t.Fatalf("failed recon must omit cluster_resources: %s", userPayload)
	}
}

// 摘要无业务标记；业务事实只在原文
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
