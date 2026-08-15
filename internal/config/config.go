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
	// 终端交互主题（dark | light | auto）；空等同 auto
	TUI TUI
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
	// 集群工具标准输出写入证据前的最大字节数；零表示使用 k8s 包默认（1MiB）
	// 对应环境变量 ARUING_K8S_MAX_STDOUT_BYTES
	MaxStdoutBytes int `yaml:"max_stdout_bytes"`
}

// 终端交互层配置
//
// Theme 取 dark | light | auto（默认 auto：按终端背景检测）；对应环境变量 ARUING_TUI_THEME
// Mode 取 inline | app（默认 inline：行内留痕；app 为 bubbletea 全屏）；对应环境变量 ARUING_TUI_MODE
type TUI struct {
	// dark | light | auto；空视为 auto
	Theme string `yaml:"theme"`
	// inline | app；空或未知值按 inline 处理
	Mode string `yaml:"mode"`
	// 主题覆盖文件路径（YAML）：写明才加载，覆盖 tui 命名样式项（颜色/边框/内边距/间距）；
	// 空 = 用内置 dark/light 默认样式。仅此一处入口（无 env、无默认搜索链）
	ThemeFile string `yaml:"theme_file"`
}

// 配置文件反序列化用的根形状，仅本包内部使用
type fileConfig struct {
	// 大模型访问参数段
	LLM LLM `yaml:"llm"`
	// 工具路径与诊断执行策略段
	Tools Tools `yaml:"tools"`
	// 终端交互主题段
	TUI TUI `yaml:"tui"`
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
			MaxStdoutBytes:      parseIntEnv(getenv("ARUING_K8S_MAX_STDOUT_BYTES")),
		},
		TUI: TUI{
			Theme: strings.TrimSpace(getenv("ARUING_TUI_THEME")),
			Mode:  strings.TrimSpace(getenv("ARUING_TUI_MODE")),
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
	if v, ok := lookup("ARUING_K8S_MAX_STDOUT_BYTES"); ok {
		if n := parseIntEnv(v); n > 0 {
			out.Tools.MaxStdoutBytes = n
		}
	}
	if v, ok := lookup("ARUING_TUI_THEME"); ok {
		if t := strings.TrimSpace(v); t != "" {
			out.TUI.Theme = t
		}
	}
	if v, ok := lookup("ARUING_TUI_MODE"); ok {
		if t := strings.TrimSpace(v); t != "" {
			out.TUI.Mode = t
		}
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

// 解析正整型环境变量；空、非数字或非正返回 0
func parseIntEnv(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return n
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
