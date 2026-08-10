package main

import (
	"errors"
	"fmt"
	"strings"

	"aruing/internal/agent"
	"aruing/internal/llm"
)

// 为单次运行失败补充可操作的人话提示
//
// 保留原始错误链，仅在识别到大模型配置或输出类问题时追加建议
// 无法分类时原样返回，避免误导
func formatRunError(err error) error {
	if err == nil {
		return nil
	}
	hint := runErrorHint(err)
	if hint == "" {
		return err
	}
	return fmt.Errorf("%w\n提示: %s", err, hint)
}

// 按错误链与文案特征给出处置建议；无匹配时返回空串
func runErrorHint(err error) string {
	if errors.Is(err, agent.ErrLLMOutputInconsistent) {
		return "模型输出多次不合规；可更换能力更强的模型，或检查 prompt / 工具规格是否与当前阶段一致；chat 可加 --verbose 看 Tower 重试细节"
	}
	if errors.Is(err, llm.ErrEmptyResponse) {
		return "供应商返回空正文（已自动重试耗尽）；可换模型/端点，或确认网关未截断输出；chat 可加 --verbose"
	}
	if errors.Is(err, llm.ErrJSONParse) {
		return "模型输出不是合法 JSON（已尝试重试）；可换模型或检查 prompt；chat 可加 --verbose"
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "build llm client"),
		strings.Contains(msg, "llm config"),
		strings.Contains(msg, "llm configuration incomplete"):
		return "检查配置文件 llm.* 或 ARUING_LLM_BASE_URL / API_KEY / MODEL（见 aruing.example.yaml）"
	case strings.Contains(msg, "invalid api key"),
		strings.Contains(msg, "authentication"),
		strings.Contains(msg, "unauthorized"),
		strings.Contains(msg, "status code: 401"),
		strings.Contains(msg, " 401"):
		return "鉴权失败；检查 API Key 是否有效、是否有权访问该 BaseURL"
	case strings.Contains(msg, "deadline exceeded"),
		strings.Contains(msg, "context deadline"),
		strings.Contains(msg, "client.timeout"):
		return "请求超时；可换更快端点/模型，或确认网络可达（默认请求超时见 internal/llm）"
	case strings.Contains(msg, "llm empty response"),
		strings.Contains(msg, "no choices"):
		return "供应商返回空响应；可换模型/端点或稍后重试；chat 可加 --verbose"
	case strings.Contains(msg, "llm generate"):
		return "大模型调用失败；检查 BaseURL/Key/Model、网络与供应商状态"
	default:
		return ""
	}
}
