// 兼容协议流式适配：保留 SDK 增量解析，显式区分协议结束与连接断开
package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// 仅为本次流式调用标记响应体处理方式，不承载业务参数
type streamContextKey struct{}

// 底层 EOF 必须是错误；正常结束由 SDK 识别协议终结标记后独立返回 EOF
type streamBody struct{ io.ReadCloser }

// 保留读取字节，避免 SDK 将未收到终结标记的断流误判成功
func (b streamBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

// 只在流式请求成功响应上安装断流检测，非流式和 HTTP 错误体保持原样
func protectStreamBody(req *http.Request, resp *http.Response) {
	if req.Context().Value(streamContextKey{}) == true && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		resp.Body = streamBody{ReadCloser: resp.Body}
	}
}

// 在统一超时内顺序读取增量；只重试建立连接前的可恢复错误
func (c *client) Stream(ctx context.Context, req StreamRequest, emit func(string) error) (StreamSummary, error) {
	if emit == nil {
		return StreamSummary{}, errors.New("llm stream: consumer is required")
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	ctx = context.WithValue(ctx, streamContextKey{}, true)
	request := openai.ChatCompletionRequest{
		Model: c.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: req.System},
			{Role: openai.ChatMessageRoleUser, Content: req.User},
		},
		Temperature:   0,
		StreamOptions: &openai.StreamOptions{IncludeUsage: true},
	}
	if req.JSONMode {
		request.ResponseFormat = &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject}
	}
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return StreamSummary{}, err
		}
		stream, err := c.api.CreateChatCompletionStream(ctx, request)
		if err == nil {
			summary, readErr := receiveStream(ctx, stream, emit)
			c.recordStream(req.Label, summary, readErr)
			return summary, readErr
		}
		c.recordStream(req.Label, StreamSummary{}, err)
		if attempt >= c.maxRetries || !retryableStreamOpen(ctx, err) {
			return StreamSummary{}, fmt.Errorf("llm stream open: %w", err)
		}
		if err = sleep(ctx, backoff(attempt)); err != nil {
			return StreamSummary{}, err
		}
	}
}

// 单个响应拥有读取和关闭；结束后仍读取用量包，但拒绝新的正文或工具调用
func receiveStream(ctx context.Context, stream *openai.ChatCompletionStream, emit func(string) error) (summary StreamSummary, err error) {
	defer func() { err = errors.Join(err, stream.Close()) }()
	size := 0
	hasText := false
	for {
		if err = ctx.Err(); err != nil {
			return summary, err
		}
		chunk, readErr := stream.Recv()
		if errors.Is(readErr, io.EOF) {
			if summary.FinishReason != "stop" {
				return summary, ErrStreamIncomplete
			}
			if !hasText {
				return summary, ErrEmptyResponse
			}
			return summary, nil
		}
		if readErr != nil {
			return summary, fmt.Errorf("%w: %w", ErrStreamIncomplete, readErr)
		}
		if chunk.Usage != nil {
			summary.Usage = &Usage{PromptTokens: chunk.Usage.PromptTokens, CompletionTokens: chunk.Usage.CompletionTokens}
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 || summary.FinishReason != "" {
				return summary, ErrStreamIncomplete
			}
			delta := choice.Delta
			if len(delta.ToolCalls) != 0 || delta.FunctionCall != nil || delta.Refusal != "" {
				return summary, ErrStreamIncomplete
			}
			if delta.Content != "" {
				size += len(delta.Content)
				if size > maxStreamBytes {
					return summary, ErrStreamLimit
				}
				hasText = hasText || strings.TrimSpace(delta.Content) != ""
				if err = emit(delta.Content); err != nil {
					return summary, fmt.Errorf("llm stream consumer: %w", err)
				}
			}
			if choice.FinishReason != "" {
				summary.FinishReason = string(choice.FinishReason)
				if choice.FinishReason != openai.FinishReasonStop {
					return summary, fmt.Errorf("%w: finish reason %s", ErrStreamIncomplete, choice.FinishReason)
				}
			}
		}
	}
}

// 每次尝试只记录一次；成功请求同时进入旧快照，保证既有评测仍可见 Reporter 用量
func (c *client) recordStream(label string, summary StreamSummary, err error) {
	if label == "" {
		label = "unknown"
	}
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	if c.streamUsage == nil {
		c.streamUsage = make(map[string]StreamTotals)
	}
	totals := c.streamUsage[label]
	totals.Attempts++
	if err == nil {
		totals.Completed++
	} else {
		totals.Failed++
	}
	if summary.Usage == nil {
		totals.UsageMissing++
	} else {
		totals.PromptTokens += int64(summary.Usage.PromptTokens)
		totals.CompletionTokens += int64(summary.Usage.CompletionTokens)
	}
	c.streamUsage[label] = totals
	if err == nil {
		old := c.usage[label]
		old.Calls++
		if summary.Usage != nil {
			old.PromptTokens += int64(summary.Usage.PromptTokens)
			old.CompletionTokens += int64(summary.Usage.CompletionTokens)
		}
		c.usage[label] = old
	}
}

// 返回副本，避免统计消费者改写共享状态
func (c *client) StreamUsageSnapshot() map[string]StreamTotals {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	out := make(map[string]StreamTotals, len(c.streamUsage))
	for label, totals := range c.streamUsage {
		out[label] = totals
	}
	return out
}

// 只重试明确的网络错误或供应商临时状态，配置和 SDK 校验错误直接返回
func retryableStreamOpen(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if _, ok := statusCodeOf(err); ok {
		return retryable(ctx, err)
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}
