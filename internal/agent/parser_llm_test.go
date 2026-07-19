package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"aruing/internal/core"
	"aruing/internal/llm"
)

// 按测试需要把任意正文包装成 OpenAI 兼容的 chat completion 响应
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

// 创建指向 mock 服务端的 LLM 客户端，服务端随测试结束自动关闭
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

// 构造使用固定时钟的工厂，避免编号生成引入时间相关的不可控值
func newTestFactory(t *testing.T) *core.Factory {
	t.Helper()
	return core.NewFactory()
}

// 单节点单现象的标准问题应被解析为含正确前缀和回填字段的 Query
func TestLLMParserParse(t *testing.T) {
	body := `{"goal":"定位 demo-api 访问失败的原因","nodes":[{"ref":"n1","type":"resource","text":"demo-api","attrs":{"k8s.namespace":"default","k8s.name":"demo-api"}}],"edges":[],"since":"30m"}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})

	parser, err := NewLLMParser(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}

	run := core.Run{ID: "run_test", Question: "default 里的 demo-api 为什么访问不了"}
	query, err := parser.Parse(context.Background(), run)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if !strings.HasPrefix(query.ID, "query_") {
		t.Errorf("query ID = %q, want query_ prefix", query.ID)
	}
	if query.RunID != "run_test" {
		t.Errorf("query RunID = %q, want run_test", query.RunID)
	}
	if query.Goal == "" {
		t.Error("query Goal is required")
	}
	if query.CreatedAt.IsZero() {
		t.Error("query CreatedAt should not be zero")
	}

	if len(query.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(query.Nodes))
	}
	node := query.Nodes[0]
	if !strings.HasPrefix(node.ID, "node_") {
		t.Errorf("node ID = %q, want node_ prefix", node.ID)
	}
	if node.Type != "resource" || node.Text != "demo-api" {
		t.Errorf("node = %+v", node)
	}
	if node.Attrs["k8s.namespace"] != "default" {
		t.Errorf("node attrs = %+v", node.Attrs)
	}

	if query.TimeRange == nil || query.TimeRange.Since != "30m" {
		t.Errorf("time range = %+v, want Since=30m", query.TimeRange)
	}
}

// 多节点带边的输出应正确建立 ref→id 映射，证明多节点兼容已就绪
func TestLLMParserMultiNode(t *testing.T) {
	body := `{"goal":"定位 checkout 延迟升高","nodes":[{"ref":"n1","type":"resource","text":"checkout"},{"ref":"n2","type":"resource","text":"postgres"}],"edges":[{"from":"n1","to":"n2","type":"depends_on","attrs":{}}],"since":""}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})

	parser, err := NewLLMParser(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}

	query, err := parser.Parse(context.Background(), core.Run{ID: "run_multi", Question: "checkout 延迟升高"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(query.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(query.Nodes))
	}
	if query.Nodes[0].Text != "checkout" || query.Nodes[1].Text != "postgres" {
		t.Errorf("nodes = %+v", query.Nodes)
	}

	if len(query.Edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(query.Edges))
	}
	edge := query.Edges[0]
	if !strings.HasPrefix(edge.ID, "edge_") {
		t.Errorf("edge ID = %q, want edge_ prefix", edge.ID)
	}
	if edge.From != query.Nodes[0].ID {
		t.Errorf("edge From = %q, want %q (node[0] ID)", edge.From, query.Nodes[0].ID)
	}
	if edge.To != query.Nodes[1].ID {
		t.Errorf("edge To = %q, want %q (node[1] ID)", edge.To, query.Nodes[1].ID)
	}
	if edge.Type != "depends_on" {
		t.Errorf("edge Type = %q, want depends_on", edge.Type)
	}

	if query.TimeRange != nil {
		t.Errorf("time range = %+v, want nil when since empty", query.TimeRange)
	}
}

// 缺少运行身份、原始问题或依赖时应尽早失败，避免产出无法关联的问题数据
func TestLLMParserValidate(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"goal":"x","nodes":[{"ref":"n1","type":"resource","text":"x"}]}`)
	})

	tests := []struct {
		name   string
		parser *LLMParser
		run    core.Run
		want   string
	}{
		{
			name:   "missing run ID",
			parser: mustNewLLMParser(t, client),
			run:    core.Run{Question: "x"},
			want:   "run ID",
		},
		{
			name:   "missing question",
			parser: mustNewLLMParser(t, client),
			run:    core.Run{ID: "run_test"},
			want:   "question",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.parser.Parse(context.Background(), test.run)
			if err == nil {
				t.Fatal("error = nil, want validation failure")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want containing %q", err, test.want)
			}
		})
	}
}

// 构造阶段依赖缺失时直接失败，避免运行期才暴露空对象
func TestNewLLMParserValidate(t *testing.T) {
	factory := core.NewFactory()
	if _, err := NewLLMParser(nil, factory); err == nil {
		t.Fatal("error = nil, want nil client rejection")
	}
	if _, err := NewLLMParser(mustNewLLMParser(t, newMockLLMClient(t, nil)).client, nil); err == nil {
		t.Fatal("error = nil, want nil factory rejection")
	}
}

// 模型返回非 JSON 应给出可读错误，不让上游包装吞掉真实原因
func TestLLMParserGenerateError(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, "this is not json")
	})
	parser := mustNewLLMParser(t, client)

	_, err := parser.Parse(context.Background(), core.Run{ID: "run_test", Question: "x"})
	if err == nil {
		t.Fatal("error = nil, want JSON parse error")
	}
	if !strings.Contains(err.Error(), "parse question with LLM") {
		t.Errorf("error = %q, want wrapping", err)
	}
}

// 模型返回缺 goal、缺节点、缺 ref 或 ref 重复都应触发业务级重试，最终 ErrLLMOutputInconsistent
func TestLLMParserInvalidOutput(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string // 期望 error 链中包含的语义片段
	}{
		{name: "empty goal", body: `{"goal":"","nodes":[{"ref":"n1","type":"r","text":"x"}]}`, want: "goal"},
		{name: "empty nodes", body: `{"goal":"x","nodes":[]}`, want: "node"},
		{name: "node missing ref", body: `{"goal":"x","nodes":[{"ref":"","type":"r","text":"x"}]}`, want: "ref"},
		{name: "duplicated ref", body: `{"goal":"x","nodes":[{"ref":"n1","type":"r","text":"a"},{"ref":"n1","type":"r","text":"b"}]}`, want: "duplicated"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := test.body
			client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeChatCompletion(w, body)
			})
			parser := mustNewLLMParser(t, client)
			_, err := parser.Parse(context.Background(), core.Run{ID: "run_test", Question: "x"})
			if err == nil {
				t.Fatal("error = nil, want inconsistency error")
			}
			// 业务重试耗尽：调用方靠 errors.Is 识别，error 文本里要带原始校验原因
			if !errors.Is(err, ErrLLMOutputInconsistent) {
				t.Errorf("error = %q, want errors.Is ErrLLMOutputInconsistent", err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want containing %q", err, test.want)
			}
		})
	}
}

// 模型偶发产出重复 ref 后第二次自愈，应在重试次数内成功，并只发 2 次请求
func TestLLMParserDuplicateRefRetry(t *testing.T) {
	dirty := `{"goal":"x","nodes":[{"ref":"n1","type":"r","text":"a"},{"ref":"n1","type":"r","text":"b"}]}`
	clean := `{"goal":"x","nodes":[{"ref":"n1","type":"r","text":"a"},{"ref":"n2","type":"r","text":"b"}],"edges":[{"from":"n1","to":"n2","type":"rel"}]}`

	calls := 0
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			writeChatCompletion(w, dirty)
			return
		}
		writeChatCompletion(w, clean)
	})
	parser := mustNewLLMParser(t, client)

	query, err := parser.Parse(context.Background(), core.Run{ID: "run_test", Question: "x"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if calls != 2 {
		t.Errorf("LLM calls = %d, want 2 (first dirty, retry clean)", calls)
	}
	if len(query.Nodes) != 2 || len(query.Edges) != 1 {
		t.Errorf("query = %+v, want 2 nodes + 1 edge", query)
	}
	// 第二个节点的 text 必须是干净输出里的 "b"，证明脏的那次被丢掉
	if query.Nodes[1].Text != "b" {
		t.Errorf("node[1] Text = %q, want b (clean retry output)", query.Nodes[1].Text)
	}
}

// 连续 maxParseAttempts 次都返回重复 ref 应返回 ErrLLMOutputInconsistent，且恰好调用 3 次
func TestLLMParserRetryExhausted(t *testing.T) {
	dirty := `{"goal":"x","nodes":[{"ref":"n1","type":"r","text":"a"},{"ref":"n1","type":"r","text":"b"}]}`

	calls := 0
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeChatCompletion(w, dirty)
	})
	parser := mustNewLLMParser(t, client)

	_, err := parser.Parse(context.Background(), core.Run{ID: "run_test", Question: "x"})
	if err == nil {
		t.Fatal("error = nil, want inconsistency error")
	}
	if !errors.Is(err, ErrLLMOutputInconsistent) {
		t.Errorf("error = %q, want errors.Is ErrLLMOutputInconsistent", err)
	}
	if !strings.Contains(err.Error(), "duplicated") {
		t.Errorf("error = %q, want containing duplicated", err)
	}
	if calls != maxParseAttempts {
		t.Errorf("LLM calls = %d, want %d", calls, maxParseAttempts)
	}
}

// 运输层错误（如 HTTP 400 不可重试）不应触发业务级重试，直接返回
// 选用 400 而不是 500：500 在 llm.Client 内部仍会被重试，calls 不易断言；
// 400 是 OpenAI 协议的不可重试错误，能精确验证"Parser 不把 transport 错误当业务重试"
func TestLLMParserTransportErrorNotRetried(t *testing.T) {
	calls := 0
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad request","type":"invalid_request_error","code":"invalid_api_key"}}`))
	})
	parser := mustNewLLMParser(t, client)

	_, err := parser.Parse(context.Background(), core.Run{ID: "run_test", Question: "x"})
	if err == nil {
		t.Fatal("error = nil, want transport error")
	}
	if errors.Is(err, ErrLLMOutputInconsistent) {
		t.Errorf("error classified as inconsistency, want transport: %q", err)
	}
	if !strings.Contains(err.Error(), "parse question with LLM") {
		t.Errorf("error = %q, want transport wrapping", err)
	}
	if calls != 1 {
		t.Errorf("LLM calls = %d, want 1 (transport errors not business-retried)", calls)
	}
}

// 业务重试中上下文取消应立即停，不再发下一次请求
//
// 让 mock 在第一次返回后自己调用 cancel，取消时机完全确定：
//
//	第一次 HTTP 调用完成 → mock 调 cancel → Parse 进入下一次循环开头 → ctx.Err() 命中 → 返回
//
// 这样 LLM 调用次数必然为 1，严格小于 maxParseAttempts，断言才有意义。
//
// calls 用 atomic，避免 mock handler goroutine 与 test goroutine 之间的数据竞争。
func TestLLMParserContextCancelMidRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dirty := `{"goal":"x","nodes":[{"ref":"n1","type":"r","text":"a"},{"ref":"n1","type":"r","text":"b"}]}`

	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			// 第一次返回后立即取消，下一次循环开头必然命中 ctx.Err()
			defer cancel()
		}
		writeChatCompletion(w, dirty)
	})
	parser := mustNewLLMParser(t, client)

	_, err := parser.Parse(ctx, core.Run{ID: "run_test", Question: "x"})
	if err == nil {
		t.Fatal("error = nil, want context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %q, want errors.Is(context.Canceled)", err)
	}
	if got := int(calls.Load()); got >= maxParseAttempts {
		t.Errorf("LLM calls = %d, want < %d (cancellation should stop the loop early)", got, maxParseAttempts)
	}
}

// 上下文取消时应立即失败，不再向模型发送请求
func TestLLMParserContextCancel(t *testing.T) {
	serverCalled := false
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
	})
	parser := mustNewLLMParser(t, client)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := parser.Parse(ctx, core.Run{ID: "run_test", Question: "x"})
	if err == nil {
		t.Fatal("error = nil, want context cancellation error")
	}
	if serverCalled {
		t.Error("server was called despite context cancellation")
	}
}

// 工具：构造一个保证可用的 LLMParser，让每个子测试只关注解析逻辑
func mustNewLLMParser(t *testing.T, client llm.Client) *LLMParser {
	t.Helper()
	parser, err := NewLLMParser(client, core.NewFactory())
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}
	return parser
}
