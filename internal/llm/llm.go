// 大模型接入包隔离供应商细节
//
// 智能体（解析、规划、验证、报告）只依赖本包暴露的 Client 接口，不感知具体厂商、服务地址、鉴权和重试
// 这样后续替换模型供应商或新增适配器时，诊断流程和核心模型不受影响
//
// 本包只负责运输：发 prompt、收文本或结构化 JSON
// prompt 的内容、组装和来源（包括未来可能的 skill 注入）不属于本包职责，由 agent 层把组装结果填进 Request.System
//
// 底层适配基于 OpenAI 兼容协议，通过更换 BaseURL 可对接 DeepSeek、Gemini 兼容端点、Qwen、Ollama 等任意兼容供应商
package llm

import (
	"context"
	"errors"
	"strings"
	"time"
)

// 一次生成请求，承载组装好的 prompt
//
// System 放角色约束、输出格式说明等稳定指令；User 放待处理的用户输入或证据数据
// 字段刻意保持最小，未来如需注入工具定义或上下文片段，走加字段方式扩展，不修改方法签名
type Request struct {
	// 系统指令，通常来自 agent 的 prompt 模板，可由调用方在喂入前动态组装
	System string
	// 用户输入或待模型处理的数据
	User string
}

// 一次纯文本生成的结果
type Response struct {
	// 模型返回的正文，可能是 Markdown（Reporter）或其他自由文本
	Content string
}

// 厂商无关的大模型客户端接口
//
// Generate 用于需要自由文本输出的角色（如 Reporter 出 Markdown 报告）
// GenerateJSON 用于需要结构化结果的角色（如 Parser、Planner、Verifier），结果反序列化进 out
// 两个方法共用底层请求与重试逻辑，仅在是否要求 JSON 输出和结果解析上有差异
type Client interface {
	// 请求一次纯文本生成，返回模型正文
	Generate(ctx context.Context, req Request) (Response, error)

	// 请求一次结构化生成，把模型输出的 JSON 反序列化进 out
	// out 必须是可写指针；模型输出不符合 JSON 时返回解析错误
	GenerateJSON(ctx context.Context, req Request, out any) error
}

// 构造客户端所需的配置
//
// 各字段语义：
//   - BaseURL：供应商兼容端点，应包含版本前缀（如 https://api.openai.com/v1）
//   - APIKey：访问凭证，本地模型（如 Ollama）可留空
//   - Model：模型名，由调用方决定，不在本包内置模型列表
//   - Timeout：整体请求超时，零值表示使用默认值
//   - MaxRetries：可重试错误（网络错误、429、5xx）的最大重试次数，零值表示使用默认值
type Config struct {
	// 供应商兼容端点，应含版本前缀（如 https://api.openai.com/v1）
	BaseURL string
	// 访问凭证，本地模型（如 Ollama）可留空
	APIKey string
	// 模型名，由调用方决定，本包不内置模型列表
	Model string
	// 整体请求超时，零值表示使用 defaultTimeout
	Timeout time.Duration
	// 可重试错误的最大重试次数，零值表示使用 defaultMaxRetries
	MaxRetries int
}

// 默认整体请求超时，覆盖 Config.Timeout 零值
//
// 强制非流式后，带 reasoning 的兼容模型常需数十秒才返回完整 JSON；
// 30s 会导致首包未到就 Client.Timeout，诊断在 Planner 阶段误失败
const defaultTimeout = 120 * time.Second

// 默认最大重试次数，覆盖 Config.MaxRetries 零值，即最多发起 3 次请求
const defaultMaxRetries = 2

// 校验配置并填充默认值，返回对后续重试与请求逻辑稳定可用的内部配置
//
// BaseURL 和 Model 是必要项，缺失时无法定位供应商和模型，直接返回错误
// Timeout 和 MaxRetries 接受零值，分别回退到默认超时和默认重试次数
// 负数的 MaxRetries 视为 0，避免构造出会无限退避的客户端
func (c Config) normalize() (Config, error) {
	if strings.TrimSpace(c.BaseURL) == "" {
		return Config{}, errors.New("llm config: BaseURL is required")
	}
	if strings.TrimSpace(c.Model) == "" {
		return Config{}, errors.New("llm config: Model is required")
	}

	normalized := c
	if normalized.Timeout <= 0 {
		normalized.Timeout = defaultTimeout
	}
	if normalized.MaxRetries < 0 {
		normalized.MaxRetries = 0
	} else if normalized.MaxRetries == 0 {
		normalized.MaxRetries = defaultMaxRetries
	}
	return normalized, nil
}
