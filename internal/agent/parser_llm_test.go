package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// 模型返回缺少 goal 或缺少节点应被 validateParserOutput 拦下
func TestLLMParserInvalidOutput(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "empty goal", body: `{"goal":"","nodes":[{"ref":"n1","type":"r","text":"x"}]}`, want: "goal"},
		{name: "empty nodes", body: `{"goal":"x","nodes":[]}`, want: "node"},
		{name: "node missing ref", body: `{"goal":"x","nodes":[{"ref":"","type":"r","text":"x"}]}`, want: "ref"},
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
				t.Fatal("error = nil, want output validation failure")
			}
			if !strings.Contains(err.Error(), "parse output") {
				t.Errorf("error = %q, want parse output wrapping", err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want containing %q", err, test.want)
			}
		})
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
