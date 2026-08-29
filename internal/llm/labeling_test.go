package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 装饰器给空标签请求补自己的标签后透传；已带标签的请求不覆盖
func TestLabelingClientSetsLabel(t *testing.T) {
	seen := make([]string, 0, 2)
	inner := fakeClient{onGenerate: func(req Request) {
		seen = append(seen, req.Label)
	}}
	c := NewLabelingClient(inner, "planner")

	_, _ = c.Generate(context.Background(), Request{System: "s", User: "u"})
	// 请求自身标签优先：装饰器只补空标签
	_, _ = c.Generate(context.Background(), Request{System: "s", User: "u", Label: "custom"})

	if len(seen) != 2 || seen[0] != "planner" || seen[1] != "custom" {
		t.Fatalf("标签透传错误：%v", seen)
	}
}

// 用量按标签聚合：服务端返回 usage → 快照含对应标签的 in/out 与调用次数；空标签归 unknown
func TestUsageAccounting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"index": 0, "message": map[string]any{"role": "assistant", "content": "ok"}, "finish_reason": "stop"},
			},
			"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
		})
	}))
	t.Cleanup(server.Close)

	c, err := NewClient(Config{BaseURL: server.URL, APIKey: "k", Model: "m"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	tracker, ok := c.(UsageTracker)
	if !ok {
		t.Fatal("具体客户端应实现 UsageTracker")
	}

	labeled := NewLabelingClient(c, "parser")
	if _, err := labeled.Generate(context.Background(), Request{System: "s", User: "u"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := c.Generate(context.Background(), Request{System: "s", User: "u"}); err != nil {
		t.Fatalf("generate: %v", err)
	}

	snap := tracker.UsageSnapshot()
	if got := snap["parser"]; got.PromptTokens != 11 || got.CompletionTokens != 7 || got.Calls != 1 {
		t.Fatalf("parser 用量错误：%+v", got)
	}
	if got := snap["unknown"]; got.Calls != 1 {
		t.Fatalf("空标签应归 unknown：%+v", got)
	}
	// 快照是副本：改动不影响内部状态
	snap["parser"] = UsageTotals{}
	if again := tracker.UsageSnapshot()["parser"]; again.Calls != 1 {
		t.Fatalf("快照应为副本：%+v", again)
	}
}

// 记录请求的假客户端；仅断言标签透传用
type fakeClient struct {
	onGenerate func(req Request)
}

func (f fakeClient) Generate(ctx context.Context, req Request) (Response, error) {
	if f.onGenerate != nil {
		f.onGenerate(req)
	}
	return Response{Content: "ok"}, nil
}

func (f fakeClient) GenerateJSON(ctx context.Context, req Request, out any) error {
	if f.onGenerate != nil {
		f.onGenerate(req)
	}
	return nil
}
