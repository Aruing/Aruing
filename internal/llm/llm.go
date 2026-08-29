// 大模型接入包隔离供应商细节
//
// 智能体（解析、规划、验证、报告）只依赖本包暴露的客户端接口，不感知具体厂商、服务地址、鉴权和重试
// 这样后续替换模型供应商或新增适配器时，诊断流程和核心模型不受影响
//
// 本包只负责运输：发提示词、收文本或结构化结果
// 提示词的内容、组装和来源（包括未来可能的技能注入）不属于本包职责，由智能体层把组装结果填进请求的系统字段
//
// 底层适配基于常见聊天补全兼容协议，通过更换基址可对接深度求索、双子星兼容端点、通义、本地模型等任意兼容供应商
package llm

import (
	"context"
	"errors"
	"strings"
	"time"
)

// 空正文：供应商返回成功但选项内容为空（兼容网关或截断常见）
// 调用方可按错误相等识别；客户端在仍有重试次数时会自动重试
var ErrEmptyResponse = errors.New("llm empty response")

// 模型正文无法解析为结构化对象（含提取后仍非法）
// 结构化生成包装此哨兵错误，便于基线塔等业务层对可恢复解析失败做重试
var ErrJSONParse = errors.New("llm json parse")

// 一次生成请求，承载组装好的提示词
//
// 系统字段放角色约束、输出格式说明等稳定指令；用户字段放待处理的用户输入或证据数据
// 字段刻意保持最小，未来如需注入工具定义或上下文片段，走加字段方式扩展，不修改方法签名
type Request struct {
	// 系统指令，通常来自智能体的提示词模板，可由调用方在喂入前动态组装
	System string
	// 用户输入或待模型处理的数据
	User string
	// 调用方标签（如角色名），仅用于客户端侧按调用方聚合 token 用量；空时归入 unknown
	// 由 LabelingClient 装饰器在装配层填充，角色自身不感知
	Label string
}

// 一次纯文本生成的结果
type Response struct {
	// 模型返回的正文，可能是标记语言报告或其他自由文本
	Content string
}

// 厂商无关的大模型客户端接口
//
// 文本生成用于需要自由文本输出的角色（如报告角色出标记语言）
// 结构化生成用于需要结构化结果的角色（如解析、规划、验证），结果反序列化进输出参数
// 两个方法共用底层请求与重试逻辑，仅在是否要求结构化输出和结果解析上有差异
type Client interface {
	// 请求一次纯文本生成，返回模型正文
	Generate(ctx context.Context, req Request) (Response, error)

	// 请求一次结构化生成，把模型输出反序列化进输出参数
	// 输出参数必须是可写指针；模型输出不符合约定结构时返回解析错误
	GenerateJSON(ctx context.Context, req Request, out any) error
}

// 一个调用方标签的累计 token 用量
// 由客户端在成功请求处聚合；评测记录按角色快照消费
type UsageTotals struct {
	// 提示词侧 token 累计
	PromptTokens int64
	// 补全侧 token 累计
	CompletionTokens int64
	// 成功请求次数
	Calls int
}

// 用量快照的可选接口；具体客户端实现，装配层按需断言取用
// 不并入 Client 接口：避免测试假实现与第三方适配器被迫实现记账
type UsageTracker interface {
	// 返回按调用方标签聚合的用量快照（副本），并发安全
	UsageSnapshot() map[string]UsageTotals
}

// 构造客户端所需的配置
//
// 各字段语义：
//   - 基址：供应商兼容端点，应包含版本前缀
//   - 密钥：访问凭证，本地模型可留空
//   - 模型：模型名，由调用方决定，不在本包内置模型列表
//   - 超时：整体请求超时，零值表示使用默认值
//   - 最大重试：可重试错误（网络错误、限流、服务端错误）的最大重试次数，零值表示使用默认值
type Config struct {
	// 供应商兼容端点，应含版本前缀
	BaseURL string
	// 访问凭证，本地模型可留空
	APIKey string
	// 模型名，由调用方决定，本包不内置模型列表
	Model string
	// 整体请求超时，零值表示使用默认超时
	Timeout time.Duration
	// 可重试错误的最大重试次数，零值表示使用默认重试次数
	MaxRetries int
}

// 默认整体请求超时，覆盖配置超时零值
//
// 强制非流式后，带推理的兼容模型常需数十秒才返回完整对象；
// 过短超时会导致首包未到就客户端超时，诊断在规划阶段误失败
const defaultTimeout = 120 * time.Second

// 默认最大重试次数，覆盖配置重试零值，即最多发起三次请求
const defaultMaxRetries = 2

// 校验配置并填充默认值，返回对后续重试与请求逻辑稳定可用的内部配置
//
// 基址和模型是必要项，缺失时无法定位供应商和模型，直接返回错误
// 超时和最大重试接受零值，分别回退到默认超时和默认重试次数
// 负数的最大重试视为零，避免构造出会无限退避的客户端
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
