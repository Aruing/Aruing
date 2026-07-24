package config

import (
	"os"
	"strings"

	"aruing/internal/llm"
)

// 进程级配置，由 Load / LoadFrom 从环境变量填充
//
// 字段只描述入口与工具路径；超时、重试等仍由各子包默认值或后续扩展承接
type Config struct {
	// 大模型访问参数；三件套未齐时 wiring 走全 fake
	LLM LLM
	// 集群工具相关路径
	Tools Tools
}

// 大模型相关环境配置
//
// BaseURL / APIKey / Model 均非空（trim 后）时 Ready 为 true
// Timeout 与 MaxRetries 本阶段不从 env 读，零值交给 llm 客户端默认
type LLM struct {
	// OpenAI 兼容端点，应含版本前缀（如 https://api.openai.com/v1）
	BaseURL string
	// 访问凭证；本地兼容端点若要求占位 key 可填任意非空串
	APIKey string
	// 模型名，由部署方决定
	Model string
}

// 工具侧路径配置
//
// KubectlPath 为空时由 wiring 在 PATH 中查找 kubectl
type Tools struct {
	// kubectl 可执行文件绝对路径；对应 ARUING_KUBECTL_PATH
	KubectlPath string
}

// 从当前进程环境加载配置
func Load() Config {
	return LoadFrom(os.Getenv)
}

// 用注入的 getenv 加载配置，便于单测不依赖真实环境
//
// getenv 为 nil 时视为全部键缺失
func LoadFrom(getenv func(string) string) Config {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return Config{
		LLM: LLM{
			BaseURL: strings.TrimSpace(getenv("ARUING_LLM_BASE_URL")),
			APIKey:  strings.TrimSpace(getenv("ARUING_LLM_API_KEY")),
			Model:   strings.TrimSpace(getenv("ARUING_LLM_MODEL")),
		},
		Tools: Tools{
			KubectlPath: strings.TrimSpace(getenv("ARUING_KUBECTL_PATH")),
		},
	}
}

// 判断 LLM 三件套是否齐全，齐全时 wiring 启用真角色
func (c LLM) Ready() bool {
	return c.BaseURL != "" && c.APIKey != "" && c.Model != ""
}

// 映射为 llm 客户端配置；Timeout / MaxRetries 保持零值以使用客户端默认
func (c LLM) ToClientConfig() llm.Config {
	return llm.Config{
		BaseURL: c.BaseURL,
		APIKey:  c.APIKey,
		Model:   c.Model,
	}
}
