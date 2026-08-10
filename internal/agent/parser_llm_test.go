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

// 按测试需要把任意正文包装成兼容对话补全响应
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

// 创建指向模拟服务端的大模型客户端，服务端随测试结束自动关闭
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

// 单节点问题应回填编号、绑定运行，并保留关键线索
func TestLLMParserParse(t *testing.T) {
	body := `{"goal":"定位 demo-api 访问失败的原因","nodes":[{"ref":"n1","type":"resource","text":"demo-api","attrs":{"k8s.namespace":"default","k8s.name":"demo-api"}}],"edges":[],"since":"30m"}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})

	parser, err := NewLLMParser(client, newTestFactory(t))
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}

	query, err := parser.Parse(context.Background(), core.Run{ID: "run_test", Question: "default 里的 demo-api 为什么访问不了"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if !strings.HasPrefix(query.ID, "query_") || query.RunID != "run_test" {
		t.Errorf("binding ID=%q RunID=%q", query.ID, query.RunID)
	}
	if len(query.Nodes) != 1 || query.Nodes[0].Text != "demo-api" {
		t.Errorf("nodes = %+v", query.Nodes)
	}
	if query.TimeRange == nil || query.TimeRange.Since != "30m" {
		t.Errorf("time range = %+v", query.TimeRange)
	}
}

// 多节点带边：局部引用到系统编号映射正确建立
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

	if len(query.Nodes) != 2 || len(query.Edges) != 1 {
		t.Fatalf("nodes=%d edges=%d", len(query.Nodes), len(query.Edges))
	}
	edge := query.Edges[0]
	if edge.From != query.Nodes[0].ID || edge.To != query.Nodes[1].ID {
		t.Errorf("edge mapping From=%q To=%q", edge.From, edge.To)
	}
}

// 入口校验与构造依赖
func TestLLMParserValidate(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"goal":"x","nodes":[{"ref":"n1","type":"resource","text":"x"}]}`)
	})

	t.Run("missing run fields", func(t *testing.T) {
		parser := mustNewLLMParser(t, client)
		if _, err := parser.Parse(context.Background(), core.Run{Question: "x"}); err == nil {
			t.Fatal("missing run ID should fail")
		}
		if _, err := parser.Parse(context.Background(), core.Run{ID: "run_test"}); err == nil {
			t.Fatal("missing question should fail")
		}
	})

	t.Run("new requires deps", func(t *testing.T) {
		factory := core.NewFactory()
		if _, err := NewLLMParser(nil, factory); err == nil {
			t.Fatal("nil client should fail")
		}
		if _, err := NewLLMParser(mustNewLLMParser(t, client).client, nil); err == nil {
			t.Fatal("nil factory should fail")
		}
	})
}

// 非法模型输出触发业务重试并最终报输出不一致
func TestLLMParserInvalidOutput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty goal", body: `{"goal":"","nodes":[{"ref":"n1","type":"r","text":"x"}]}`},
		{name: "empty nodes", body: `{"goal":"x","nodes":[]}`},
		{name: "duplicated ref", body: `{"goal":"x","nodes":[{"ref":"n1","type":"r","text":"a"},{"ref":"n1","type":"r","text":"b"}]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := test.body
			client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeChatCompletion(w, body)
			})
			_, err := mustNewLLMParser(t, client).Parse(context.Background(), core.Run{ID: "run_test", Question: "x"})
			if !errors.Is(err, ErrLLMOutputInconsistent) {
				t.Fatalf("error = %v, want ErrLLMOutputInconsistent", err)
			}
		})
	}
}

// 业务重试：脏输出后自愈，或持续违规耗尽；运输层错误不计入业务重试
func TestLLMParserRetry(t *testing.T) {
	t.Run("then success", func(t *testing.T) {
		dirty := `{"goal":"x","nodes":[{"ref":"n1","type":"r","text":"a"},{"ref":"n1","type":"r","text":"b"}]}`
		clean := `{"goal":"x","nodes":[{"ref":"n1","type":"r","text":"a"},{"ref":"n2","type":"r","text":"b"}],"edges":[{"from":"n1","to":"n2","type":"rel"}]}`
		var calls atomic.Int32
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				writeChatCompletion(w, dirty)
				return
			}
			writeChatCompletion(w, clean)
		})
		query, err := mustNewLLMParser(t, client).Parse(context.Background(), core.Run{ID: "run_test", Question: "x"})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if calls.Load() != 2 || len(query.Nodes) != 2 {
			t.Errorf("calls=%d nodes=%d", calls.Load(), len(query.Nodes))
		}
	})

	t.Run("exhausted", func(t *testing.T) {
		dirty := `{"goal":"x","nodes":[{"ref":"n1","type":"r","text":"a"},{"ref":"n1","type":"r","text":"b"}]}`
		var calls atomic.Int32
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			writeChatCompletion(w, dirty)
		})
		_, err := mustNewLLMParser(t, client).Parse(context.Background(), core.Run{ID: "run_test", Question: "x"})
		if !errors.Is(err, ErrLLMOutputInconsistent) {
			t.Fatalf("error = %v", err)
		}
		if calls.Load() != maxParseAttempts {
			t.Errorf("calls = %d, want %d", calls.Load(), maxParseAttempts)
		}
	})

	// 协议层四百类不可重试错误，解析器不得把它当业务校验重试
	t.Run("transport not retried", func(t *testing.T) {
		var calls atomic.Int32
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"bad request","type":"invalid_request_error","code":"invalid_api_key"}}`))
		})
		_, err := mustNewLLMParser(t, client).Parse(context.Background(), core.Run{ID: "run_test", Question: "x"})
		if err == nil || errors.Is(err, ErrLLMOutputInconsistent) {
			t.Fatalf("want transport error, got %v", err)
		}
		if calls.Load() != 1 {
			t.Errorf("calls = %d, want 1", calls.Load())
		}
	})
}

// 取消上下文应停在循环边界，不把业务重试跑满
func TestLLMParserContextCancel(t *testing.T) {
	t.Run("before request", func(t *testing.T) {
		serverCalled := false
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			serverCalled = true
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := mustNewLLMParser(t, client).Parse(ctx, core.Run{ID: "run_test", Question: "x"})
		if err == nil {
			t.Fatal("want cancellation error")
		}
		if serverCalled {
			t.Error("server was called despite context cancellation")
		}
	})

	// 第一次返回脏输出后取消，应在下次循环开头停住
	t.Run("mid retry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		dirty := `{"goal":"x","nodes":[{"ref":"n1","type":"r","text":"a"},{"ref":"n1","type":"r","text":"b"}]}`
		var calls atomic.Int32
		client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				defer cancel()
			}
			writeChatCompletion(w, dirty)
		})
		_, err := mustNewLLMParser(t, client).Parse(ctx, core.Run{ID: "run_test", Question: "x"})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
		if got := int(calls.Load()); got >= maxParseAttempts {
			t.Errorf("calls = %d, want < %d", got, maxParseAttempts)
		}
	})
}

// 工具：构造一个保证可用的大模型解析器
func mustNewLLMParser(t *testing.T, client llm.Client) *LLMParser {
	t.Helper()
	parser, err := NewLLMParser(client, core.NewFactory())
	if err != nil {
		t.Fatalf("new parser: %v", err)
	}
	return parser
}
