// 适配实现：把厂商无关的客户端接口桥接到常见聊天补全兼容协议
//
// 本文件是唯一导入兼容开发包的位置，供应商细节被锁在这里
// 将来新增原生协议适配器时，新增一个文件实现同一客户端接口即可，不改动本文件主体逻辑
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// 检查配置并创建一个可复用的客户端实例
//
// 返回的客户端在多次诊断运行之间应被复用，而非每次请求新建
// 配置非法时返回错误，避免运行期才暴露初始化问题
func NewClient(cfg Config) (Client, error) {
	normalized, err := cfg.normalize()
	if err != nil {
		return nil, err
	}

	// 默认配置已填入默认基址与超文本客户端，这里按用户配置覆盖
	// 超文本客户端套强制非流式传输：兼容开发包的流式字段带省略空值，
	// 假不会出现在请求体里；部分兼容网关把「缺省流式」当成真，回服务器推送事件
	// （正文以事件前缀开头）→ 开发包按整包对象解析时报非法字符
	// 官方缺省为非流式；这里显式注入流式为假对齐官方语义
	ocfg := openai.DefaultConfig(normalized.APIKey)
	ocfg.BaseURL = normalized.BaseURL
	ocfg.HTTPClient = &http.Client{
		Timeout:   normalized.Timeout,
		Transport: forceNonStreamTransport{base: http.DefaultTransport},
	}

	return &client{
		api:        openai.NewClientWithConfig(ocfg),
		model:      normalized.Model,
		maxRetries: normalized.MaxRetries,
	}, nil
}

// 在对话补全请求体缺省流式时写入假，避免兼容网关默认开流式
//
// 仅改写对象请求体；已显式带流式的请求原样转发
// 不依赖具体路径前缀以外的约定，兼容默认版本前缀与自定义基址
type forceNonStreamTransport struct {
	// 实际转发请求的底层传输，空时回退默认传输
	base http.RoundTripper
}

// 对对话补全提交请求补齐流式为假后转发
func (t forceNonStreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req.Method == http.MethodPost && req.Body != nil && strings.Contains(req.URL.Path, "/chat/completions") {
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = ensureStreamFalse(body)
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}
	return base.RoundTrip(req)
}

// 若对象未设置流式字段，则补为假；解析失败则原样返回
//
// 用原始消息映射承载其余字段，避免反序列化到任意类型时数字被转成浮点
// 造成大整数（如随机种子）丢精度，同时保留原始字节不改变序列化形态
func ensureStreamFalse(body []byte) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	if _, exists := m["stream"]; exists {
		return body
	}
	m["stream"] = json.RawMessage("false")
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

// 常见聊天补全兼容协议的适配器实现
//
// 仅持有不可变依赖（兼容客户端、模型名、重试上限），可被多个协程复用
// 重试退避时间在每次调用内独立计算，不存在跨请求共享状态
type client struct {
	// 兼容协议客户端
	api *openai.Client
	// 请求使用的模型名
	model string
	// 可恢复错误的最大重试次数
	maxRetries int
}

// 请求一次纯文本生成，直接返回模型正文
func (c *client) Generate(ctx context.Context, req Request) (Response, error) {
	content, err := c.do(ctx, req, false)
	if err != nil {
		return Response{}, err
	}
	return Response{Content: content}, nil
}

// 请求一次结构化生成，把模型输出反序列化进输出参数
//
// 输出参数必须是可写指针；模型输出被围栏包裹或前后带说明文字时也会尝试提取最外层对象
// 提取后仍无法解析时返回结构化解析哨兵错误（可按错误相等识别），并附预览便于定位
func (c *client) GenerateJSON(ctx context.Context, req Request, out any) error {
	if out == nil {
		return errors.New("llm GenerateJSON: out must be a non-nil pointer")
	}

	content, err := c.do(ctx, req, true)
	if err != nil {
		return err
	}

	raw := extractJSON(content)
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		// 只截取一段输出进错误信息，避免把超长模型回复完整塞进错误链
		preview := raw
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		return fmt.Errorf("%w: got %q (%d bytes raw): %v", ErrJSONParse, preview, len(raw), err)
	}
	return nil
}

// 执行一次带重试的生成请求，返回模型正文
//
// 结构化模式为真时要求供应商返回对象；
// 为假时由模型自由返回文本
// 重试仅针对可恢复错误（网络错误、限流、服务端错误），调用方取消或整体超时不重试
func (c *client) do(ctx context.Context, req Request, jsonMode bool) (string, error) {
	request := openai.ChatCompletionRequest{
		Model: c.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: req.System},
			{Role: openai.ChatMessageRoleUser, Content: req.User},
		},
		// 诊断需要稳定可复现的输出，固定使用最低温度
		Temperature: 0,
	}
	if jsonMode {
		request.ResponseFormat = &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		}
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		// 上游已取消或整体超时，立即停止，不再发起请求
		if err := ctx.Err(); err != nil {
			return "", err
		}

		resp, err := c.api.CreateChatCompletion(ctx, request)
		if err == nil {
			if len(resp.Choices) == 0 {
				// 无候选项与空正文同类：可恢复时退避重试
				lastErr = fmt.Errorf("%w: response has no choices", ErrEmptyResponse)
				if attempt < c.maxRetries {
					if waitErr := sleep(ctx, backoff(attempt)); waitErr != nil {
						return "", waitErr
					}
					continue
				}
				return "", lastErr
			}
			content := resp.Choices[0].Message.Content
			if strings.TrimSpace(content) == "" {
				// 兼容网关偶发空正文；与网络抖动同样走退避重试
				lastErr = ErrEmptyResponse
				if attempt < c.maxRetries {
					if waitErr := sleep(ctx, backoff(attempt)); waitErr != nil {
						return "", waitErr
					}
					continue
				}
				return "", ErrEmptyResponse
			}
			return content, nil
		}

		lastErr = err
		if !retryable(ctx, err) {
			return "", fmt.Errorf("llm generate: %w", err)
		}
		// 还有下一次尝试时才退避，最后一次失败后直接跳出走统一错误返回
		if attempt < c.maxRetries {
			if waitErr := sleep(ctx, backoff(attempt)); waitErr != nil {
				return "", waitErr
			}
		}
	}
	return "", fmt.Errorf("llm generate failed after %d attempts: %w", c.maxRetries+1, lastErr)
}

// 判断一次失败是否值得重试
//
// 调用方主动取消或整体截止时间到达时不重试，否则退避只会白白浪费时间
// 供应商返回的错误按状态码分类：限流与服务端错误视为临时故障可重试，其余客户端错误直接放弃
// 兼容开发包依据响应体格式返回接口错误或请求错误，两者都带状态码，需要分别匹配
// 非供应商错误（连接被重置、域名解析抖动等网络层错误）按可重试处理
func retryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if code, ok := statusCodeOf(err); ok {
		switch code {
		case http.StatusTooManyRequests,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return true
		}
		return false
	}
	return true
}

// 从兼容开发包的错误链里提取超文本状态码
//
// 供应商按响应体是否为标准错误对象决定返回接口错误还是请求错误
// 调用方不应关心是哪一种，这里统一抽取状态码供重试判断
func statusCodeOf(err error) (int, bool) {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.HTTPStatusCode, true
	}
	var reqErr *openai.RequestError
	if errors.As(err, &reqErr) {
		return reqErr.HTTPStatusCode, true
	}
	return 0, false
}

// 计算第几次失败后的退避时长（次数从零开始）
//
// 指数增长并设上限，避免在长时间故障下退避到分钟级
func backoff(attempt int) time.Duration {
	d := time.Duration(200<<(attempt)) * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// 在退避期间尊重上下文取消，避免重试等待阻塞已经放弃的诊断
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// 从模型输出中提取可解析的结构化文本
//
// 模型偶尔会把对象包在代码围栏里，或前后加上说明文字
// 这里依次处理：去围栏 → 取最外层花括号内容，保证反序列化能拿到完整对象
// 找不到花括号时原样返回，让反序列化报出可读的解析错误
func extractJSON(content string) string {
	s := strings.TrimSpace(content)

	// 去除标记语言代码围栏：去掉首行的围栏标记，再去掉结尾围栏
	if strings.HasPrefix(s, "```") {
		if nl := strings.Index(s, "\n"); nl >= 0 {
			s = strings.TrimSpace(s[nl+1:])
		}
		if fence := strings.LastIndex(s, "```"); fence >= 0 {
			s = strings.TrimSpace(s[:fence])
		}
	}

	// 即便残留说明文字，也取最外层花括号之间的内容
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}
