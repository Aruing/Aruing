package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// -------------------------- 测试工具函数 --------------------------

// 把指定正文按 OpenAI chat completion 协议写回，供 mock 服务端复用
func writeCompletion(w http.ResponseWriter, content string) {
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

// 解析请求体，断言时只关心少量字段，避免对完整结构过度依赖
func decodeBody(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(body).Decode(&m); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return m
}

// 创建指向 mock 服务端的客户端，服务端随测试结束自动关闭
func newTestClient(t *testing.T, handler http.HandlerFunc) Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	c, err := NewClient(Config{BaseURL: server.URL, APIKey: "test-key", Model: "test-model"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

// 断言错误非空且信息包含指定片段，避免测试依赖完整错误文本
func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want containing %q", err, want)
	}
}

// -------------------------- 配置校验 --------------------------

// 缺少必要配置应在构造阶段被拒绝，避免运行期才暴露初始化问题
func TestNewClientValidate(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "missing base url", cfg: Config{Model: "m"}, want: "BaseURL"},
		{name: "missing model", cfg: Config{BaseURL: "http://x"}, want: "Model"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewClient(test.cfg)
			requireErrorContains(t, err, test.want)
		})
	}
}

// 零值超时和重试应回退到默认值，使调用方可以省略显式配置
func TestConfigNormalizeDefaults(t *testing.T) {
	normalized, err := Config{BaseURL: "http://x", Model: "m"}.normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if normalized.Timeout != defaultTimeout {
		t.Errorf("Timeout = %v, want default %v", normalized.Timeout, defaultTimeout)
	}
	if normalized.MaxRetries != defaultMaxRetries {
		t.Errorf("MaxRetries = %v, want default %v", normalized.MaxRetries, defaultMaxRetries)
	}
}

// 负的 MaxRetries 视为 0，避免构造会无限退避的客户端
func TestConfigNormalizeNegativeRetries(t *testing.T) {
	normalized, err := Config{BaseURL: "http://x", Model: "m", MaxRetries: -3}.normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if normalized.MaxRetries != 0 {
		t.Errorf("MaxRetries = %v, want 0", normalized.MaxRetries)
	}
}

// -------------------------- JSON 提取 --------------------------

// extractJSON 应能处理围栏包裹和带说明文字的输出
func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain object", input: `{"a":1}`, want: `{"a":1}`},
		{name: "fenced", input: "```json\n{\"a\":1}\n```", want: `{"a":1}`},
		{name: "prose around", input: `结果如下： {"a":1} 以上`, want: `{"a":1}`},
		{name: "no braces", input: "纯文本没有花括号", want: "纯文本没有花括号"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := extractJSON(test.input); got != test.want {
				t.Errorf("extractJSON = %q, want %q", got, test.want)
			}
		})
	}
}

// -------------------------- 生成主路径 --------------------------

// GenerateJSON 应填充结构化结果，且请求要求供应商返回 JSON 对象
func TestGenerateJSON(t *testing.T) {
	var got map[string]any
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r.Body)
		got = body
		writeCompletion(w, `{"goal":"定位 demo 故障","confidence":0.9}`)
	})

	var out struct {
		Goal       string  `json:"goal"`
		Confidence float64 `json:"confidence"`
	}
	if err := client.GenerateJSON(context.Background(), Request{System: "s", User: "u"}, &out); err != nil {
		t.Fatalf("generate json: %v", err)
	}
	if out.Goal != "定位 demo 故障" {
		t.Errorf("Goal = %q", out.Goal)
	}
	if out.Confidence != 0.9 {
		t.Errorf("Confidence = %v", out.Confidence)
	}

	// 验证确实向供应商声明了 JSON 输出格式
	format, ok := got["response_format"].(map[string]any)
	if !ok || format["type"] != "json_object" {
		t.Errorf("response_format = %v, want type=json_object", got["response_format"])
	}
	// go-openai 默认省略 stream=false；我们强制写入，兼容「缺省即流式」的网关
	if got["stream"] != false {
		t.Errorf("stream = %v, want false", got["stream"])
	}
}

// ensureStreamFalse 只在缺省时补 false，已有 stream 字段时不覆盖
func TestEnsureStreamFalse(t *testing.T) {
	t.Parallel()
	got := ensureStreamFalse([]byte(`{"model":"m","messages":[]}`))
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["stream"] != false {
		t.Fatalf("stream = %v, want false", m["stream"])
	}

	got = ensureStreamFalse([]byte(`{"model":"m","stream":true}`))
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["stream"] != true {
		t.Fatalf("stream = %v, want true (must not override)", m["stream"])
	}
}

// GenerateJSON 应处理被围栏包裹的 JSON 输出
func TestGenerateJSONFenced(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeCompletion(w, "```json\n{\"name\":\"demo\"}\n```")
	})

	var out struct {
		Name string `json:"name"`
	}
	if err := client.GenerateJSON(context.Background(), Request{}, &out); err != nil {
		t.Fatalf("generate json: %v", err)
	}
	if out.Name != "demo" {
		t.Errorf("Name = %q", out.Name)
	}
}

// 输出无法解析为 JSON 时应返回带输出预览的错误
func TestGenerateJSONNonJSON(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeCompletion(w, "这不是 JSON")
	})

	err := client.GenerateJSON(context.Background(), Request{}, &struct{}{})
	requireErrorContains(t, err, "parse json output")
}

// out 非指针或为 nil 时应直接拒绝，避免 Unmarshal 触发 panic
func TestGenerateJSONNilOut(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeCompletion(w, "{}")
	})
	requireErrorContains(t, client.GenerateJSON(context.Background(), Request{}, nil), "non-nil pointer")
}

// Generate 应返回纯文本，且请求不要求 JSON 输出（给 Reporter 出 Markdown 用）
func TestGenerate(t *testing.T) {
	var sawFormat any
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r.Body)
		sawFormat = body["response_format"]
		writeCompletion(w, "# 报告\n\n后端 Pod 异常")
	})

	resp, err := client.Generate(context.Background(), Request{System: "s", User: "u"})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Content != "# 报告\n\n后端 Pod 异常" {
		t.Errorf("Content = %q", resp.Content)
	}
	if sawFormat != nil {
		t.Errorf("response_format = %v, want absent for plain text", sawFormat)
	}
}

// -------------------------- 重试与错误 --------------------------

// 可重试错误（5xx）应自动重试，最终在成功响应上返回结果
func TestGenerateRetryThenSucceed(t *testing.T) {
	var attempts atomic.Int32
	cl := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		writeCompletion(w, "第二次成功")
	})
	// 缩短退避，避免重试用例拖慢测试
	cl.(*client).maxRetries = 1

	resp, err := cl.Generate(context.Background(), Request{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if resp.Content != "第二次成功" {
		t.Errorf("Content = %q", resp.Content)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

// 不可重试的 4xx 应立即失败，不发起额外请求
func TestGenerateNonRetryable(t *testing.T) {
	var attempts atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	})

	_, err := client.Generate(context.Background(), Request{})
	requireErrorContains(t, err, "llm generate")
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 4xx)", got)
	}
}

// 上游上下文超时应中止请求且不重试
func TestGenerateTimeout(t *testing.T) {
	var attempts atomic.Int32
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		// 客户端放弃时服务端也尽快返回，避免 Close 阻塞
		select {
		case <-time.After(200 * time.Millisecond):
			writeCompletion(w, "late")
		case <-r.Context().Done():
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	_, err := client.Generate(ctx, Request{})
	if err == nil {
		t.Fatal("error = nil, want timeout error")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on context timeout)", got)
	}
}

// 错误应保留底层供应商错误，便于上层按需用 errors.As 分类（如鉴权、限流），我们只包裹不擦除
func TestGenerateErrorPreservesUnderlying(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"authentication"}}`))
	})

	_, err := client.Generate(context.Background(), Request{})
	requireErrorContains(t, err, "llm generate")

	// sashabaranov 依据 body 格式返回 *APIError 或 *RequestError，至少一种应出现在错误链中
	var apiErr *openai.APIError
	var reqErr *openai.RequestError
	if !errors.As(err, &apiErr) && !errors.As(err, &reqErr) {
		t.Fatalf("error chain missing openai error type: %v", err)
	}
}
