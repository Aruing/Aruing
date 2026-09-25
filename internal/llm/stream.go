// 流式能力独立于阻塞客户端；增量只用于传输，摘要不代表领域对象已通过校验
package llm

import (
	"context"
	"errors"
)

// 流未正常结束或出现不支持的增量；已对外发射内容的调用方不得自动重新生成
var ErrStreamIncomplete = errors.New("llm stream incomplete")

// 正文超过接收上限；明确失败，不截断后作为成功结果
var ErrStreamLimit = errors.New("llm stream content limit exceeded")

const maxStreamBytes = 8 << 20

// 流式请求复用现有提示词与标签，并显式选择结构化输出
type StreamRequest struct {
	Request
	// 为真时请求供应商返回结构化对象
	JSONMode bool
}

// 供应商报告的用量；未提供时通过摘要中的空指针表示未知
type Usage struct {
	// 提示词 token 数
	PromptTokens int
	// 补全 token 数
	CompletionTokens int
}

// 一次流式请求已收到的元信息；失败时仍可读取，不能据此提交正文
type StreamSummary struct {
	// 供应商结束原因
	FinishReason string
	// 未提供统计时为空
	Usage *Usage
}

// 同步顺序调用增量消费者；消费者必须及时返回并自行响应上下文取消
// 消费者返回错误立即停止，只有完整协议结束且正文非空才返回成功
type Streamer interface {
	Stream(context.Context, StreamRequest, func(string) error) (StreamSummary, error)
}

// 流式尝试统计，成功调用与已有用量快照重叠，不能将两份快照相加
type StreamTotals struct {
	// 发出的请求尝试数
	Attempts int
	// 正常完成的请求数
	Completed int
	// 失败尝试数
	Failed int
	// 未收到供应商用量的尝试数
	UsageMissing int
	// 包括失败尝试在内的已知提示词用量
	PromptTokens int64
	// 包括失败尝试在内的已知补全用量
	CompletionTokens int64
}

// 读取流式统计副本；未知用量不等于零消耗
type StreamUsageTracker interface {
	StreamUsageSnapshot() map[string]StreamTotals
}
