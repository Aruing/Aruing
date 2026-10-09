// 调用方标签装饰器：给透传的请求补上自己的标签后转发底层客户端
//
// 用途在装配层：给解析、规划、验证、报告等角色各注入一个带角色标签的包装，
// 客户端侧即可按角色聚合 token 用量（Request.Label，见 llm.go）
// 角色只依赖 Client 接口，不感知标签存在——评测记账对角色零侵入

package llm

import "context"

// 标签装饰客户端；转发全部调用，转发前给空标签的请求补上自己的标签
type labelingClient struct {
	// 被包装的底层客户端
	inner Client
	// 本包装注入的调用方标签（如角色名）
	label string
}

// NewLabelingClient 返回给请求补标签的客户端包装
// label 为空时等同直接透传（不再补标签）
func NewLabelingClient(inner Client, label string) Client {
	base := &labelingClient{inner: inner, label: label}
	if stream, ok := inner.(Streamer); ok {
		return &labelingStreamer{labelingClient: base, stream: stream}
	}
	return base
}

// Generate 转发纯文本生成，转发前补标签
func (c *labelingClient) Generate(ctx context.Context, req Request) (Response, error) {
	if req.Label == "" {
		req.Label = c.label
	}
	return c.inner.Generate(ctx, req)
}

// GenerateJSON 转发结构化生成，转发前补标签
func (c *labelingClient) GenerateJSON(ctx context.Context, req Request, out any) error {
	if req.Label == "" {
		req.Label = c.label
	}
	return c.inner.GenerateJSON(ctx, req, out)
}

// 仅在底层确实支持流式时暴露该能力
type labelingStreamer struct {
	*labelingClient
	// 底层流式能力，标签在调用前补齐
	stream Streamer
}

// 逐块透传并保留显式标签，不增加缓存或重试
func (c *labelingStreamer) Stream(ctx context.Context, req StreamRequest, emit func(string) error) (StreamSummary, error) {
	if req.Label == "" {
		req.Label = c.label
	}
	return c.stream.Stream(ctx, req, emit)
}
