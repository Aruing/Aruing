// 配置包集中收敛进程级运行参数
//
// 加载顺序：可选配置文件 → 环境变量覆盖 → 命令行再盖（如详细输出开关）
// 命令行入口通过解析加载拿到配置后组装编排器，代理与工具层不直接读环境变量
//
// 本地调试可用演示配置或仓库根环境文件（构建目标加载）；本包不解析环境文件
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"aruing/internal/llm"
)

// 进程级配置，由各类加载入口填充
//
// 字段只描述入口与工具路径；超时、重试等仍由各子包默认值或后续扩展承接
type Config struct {
	// 大模型访问参数；产品入口要求就绪
	LLM LLM
	// 集群工具相关路径
	Tools Tools
	// 是否输出基线塔与编排调试进度到标准错误（调试环境变量或命令行详细开关）
	Debug bool
}

// 大模型相关配置
//
// 基址、密钥、模型均非空（去空白后）时视为就绪
// 超时与重试本阶段不从文件或环境变量读，零值交给大模型客户端默认
type LLM struct {
	// 兼容端点，应含版本前缀
	BaseURL string `yaml:"base_url"`
	// 访问凭证；本地兼容端点若要求占位密钥可填任意非空串
	APIKey string `yaml:"api_key"`
	// 模型名，由部署方决定
	Model string `yaml:"model"`
}

// 工具侧路径配置
//
// 集群命令路径为空时由装配层在系统路径中查找
type Tools struct {
	// 集群命令可执行文件绝对路径
	KubectlPath string `yaml:"kubectl_path"`
	// 是否放行容器内诊断探针（连通性、域名解析）
	// 默认关闭（只读策略拒绝）；对应诊断执行环境变量
	// 开启后装配层改用诊断策略，由规划提示词引导只做诊断探针
	AllowDiagnosticExec bool `yaml:"allow_diagnostic_exec"`
}

// 配置文件反序列化用的根形状，仅本包内部使用
type fileConfig struct {
	// 大模型访问参数段
	LLM LLM `yaml:"llm"`
	// 工具路径与诊断执行策略段
	Tools Tools `yaml:"tools"`
	// 是否输出调试进度
	Debug bool `yaml:"debug"`
}

// 从当前进程环境加载配置（无文件）
func Load() Config {
	return LoadFrom(os.Getenv)
}

// 用注入的环境读取函数加载配置，便于单测不依赖真实环境
//
// 读取函数为空时视为全部键缺失
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
			KubectlPath:         strings.TrimSpace(getenv("ARUING_KUBECTL_PATH")),
			AllowDiagnosticExec: parseBoolEnv(getenv("ARUING_ALLOW_DIAGNOSTIC_EXEC")),
		},
		Debug: parseBoolEnv(getenv("ARUING_DEBUG")),
	}
}

// 用查找环境风格覆盖基底：字符串键仅非空时覆盖；布尔键只要存在即覆盖（含假）
//
// 查找函数为空时返回基底副本语义上的原值
func MergeEnvLookup(base Config, lookup func(string) (string, bool)) Config {
	if lookup == nil {
		return base
	}
	out := base
	if v, ok := lookup("ARUING_LLM_BASE_URL"); ok {
		if t := strings.TrimSpace(v); t != "" {
			out.LLM.BaseURL = t
		}
	}
	if v, ok := lookup("ARUING_LLM_API_KEY"); ok {
		if t := strings.TrimSpace(v); t != "" {
			out.LLM.APIKey = t
		}
	}
	if v, ok := lookup("ARUING_LLM_MODEL"); ok {
		if t := strings.TrimSpace(v); t != "" {
			out.LLM.Model = t
		}
	}
	if v, ok := lookup("ARUING_KUBECTL_PATH"); ok {
		if t := strings.TrimSpace(v); t != "" {
			out.Tools.KubectlPath = t
		}
	}
	if v, ok := lookup("ARUING_ALLOW_DIAGNOSTIC_EXEC"); ok {
		out.Tools.AllowDiagnosticExec = parseBoolEnv(v)
	}
	if v, ok := lookup("ARUING_DEBUG"); ok {
		out.Debug = parseBoolEnv(v)
	}
	return out
}

// 校验大模型三件套是否齐全；缺任一项返回错误并列出缺项
func ValidateLLM(cfg Config) error {
	if cfg.LLM.Ready() {
		return nil
	}
	var missing []string
	if cfg.LLM.BaseURL == "" {
		missing = append(missing, "llm.base_url / ARUING_LLM_BASE_URL")
	}
	if cfg.LLM.APIKey == "" {
		missing = append(missing, "llm.api_key / ARUING_LLM_API_KEY")
	}
	if cfg.LLM.Model == "" {
		missing = append(missing, "llm.model / ARUING_LLM_MODEL")
	}
	return fmt.Errorf("LLM configuration incomplete: missing %s (config file, ARUING_CONFIG, or env; see aruing.example.yaml)", strings.Join(missing, ", "))
}

// 解析布尔环境变量；空或非布尔值返回否，避免误开执行这类高自由度动作
func parseBoolEnv(v string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	return err == nil && b
}

// 判断大模型端点、密钥与模型名是否均非空
func (c LLM) Ready() bool {
	return c.BaseURL != "" && c.APIKey != "" && c.Model != ""
}

// 映射为大模型客户端配置；超时与最大重试保持零值以使用客户端默认
func (c LLM) ToClientConfig() llm.Config {
	return llm.Config{
		BaseURL: c.BaseURL,
		APIKey:  c.APIKey,
		Model:   c.Model,
	}
}
