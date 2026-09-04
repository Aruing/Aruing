// 配置包集中收敛进程级运行参数
//
// 加载顺序：可选配置文件 → 环境变量覆盖 → 命令行再盖（如详细输出开关）
// 命令行入口通过解析加载拿到配置后组装编排器，代理与工具层不直接读环境变量
//
// 本地调试可用演示配置或仓库根环境文件（构建目标加载）；本包不解析环境文件
package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Aruing/Aruing/internal/llm"
)

// 进程级配置，由各类加载入口填充
//
// 字段只描述入口与工具路径；超时、重试等仍由各子包默认值或后续扩展承接
type Config struct {
	// 大模型访问参数；产品入口要求就绪
	LLM LLM
	// 集群工具相关路径
	Tools Tools
	// 诊断编排侧配置（取证决策方法与参数）
	Agent Agent
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
	// 表格投影方法与预算（同一二进制切换对比方法，实验用）；零值 = 默认 fast
	// 对应环境变量 ARUING_TOOLS_PROJECTION_*
	Projection Projection `yaml:"projection"`
}

// 诊断编排侧配置
//
// 能力开关按同模式扩展：取证决策（acquire）与记忆组装（memory）
type Agent struct {
	// 主动取证决策（调查阶段循环方法与决策参数）
	Acquire Acquire `yaml:"acquire"`
	// 记忆组装（会话历史注入视图方法开关；实验臂须显式配置）
	Memory Memory `yaml:"memory"`
}

// 主动取证决策配置：调查循环方法开关与决策参数（实验敏感性扫参用）；零值 = 默认
//
// 方法名解析与校验在装配层（启动明确失败，不静默回落，照 tools.projection 先例）；
// 数值参数由 acquire 包在消费侧归一非法值（非正/非有限/越语义域回默认），
// 配置层不做二次校验
// 对应环境变量 ARUING_AGENT_ACQUIRE_*
type Acquire struct {
	// ours | b1-serial | b2-random | b4-cheapest（后两者为实验臂：复用 ours 决策循环
	// 只换选择策略——种子随机 / 恒选最低成本）；空 = ours（产品默认，一开关可达各臂）
	Method string `yaml:"method"`
	// 调查阶段轮数预算（两循环共用口径：一轮 = 一次动作/一次规划/一次问用户）；
	// 0 = 默认 3（实验扫预算 K=1/2/3/5/8 用）
	MaxRounds int `yaml:"max_rounds"`
	// b2-random 实验臂的随机种子（选择可复现：同种子同信念必同选择）；其余方法不读
	Seed int64 `yaml:"seed"`
	// 强度更新灵敏度 α（ℓ = 2^(α·d·s)，每单位证据强度的 bit 证据权）；0 = 默认 3
	Alpha float64 `yaml:"alpha"`
	// supported 出口后验阈值 P*；0 = 默认 0.9
	PStar float64 `yaml:"p_star"`
	// SPRT 后验优势比阈值 A；0 = 默认 19
	A float64 `yaml:"a"`
	// 信息平台阈值 τ（bit）；0 = 默认 0.01
	Tau float64 `yaml:"tau"`
	// 全局意外阈值 δ；0 = 默认 0.05
	Delta float64 `yaml:"delta"`
	// refuted 出口假设空间保留质量下限；0 = 默认 0.05
	MassFloor float64 `yaml:"mass_floor"`
}

// 记忆组装配置：会话历史注入视图的组装方法开关与实验参数；零值 = 默认
//
// 方法名解析与校验在装配层（启动明确失败，不静默回落，照 tools.projection / agent.acquire 先例）
// 对应环境变量 ARUING_AGENT_MEMORY_*
type Memory struct {
	// ours | d1-last-n | d2-flat-summary（后两者为实验对照臂：纯记忆策略，
	// 无索引卡无回灌；D1 last-N 违反 #18 仅实验豁免）；空 = ours（产品默认）
	Method string `yaml:"method"`
	// D1 保留的最近消息条数；0 = 默认 20（实验扫描用）；其余方法不读
	LastN int `yaml:"last_n"`
}

// 表格投影配置：方法开关与预算口径，透传给投影层
//
// 产品默认 fast（三段式快路径）；greedy 系与各基线方法供对比实验切换
// 方法名解析与校验在装配层（启动明确失败，不静默回落）
type Projection struct {
	// fast | greedy | greedy-knapsack | full | head-tail | uniform；空 = fast
	Method string `yaml:"method"`
	// 投影实例行 rune 预算；0 = 投影层默认（4096）
	Budget int `yaml:"budget"`
	// 贪心 f_anom 权重 λ；0 = 默认 1
	Lambda float64 `yaml:"lambda"`
	// 覆盖权重均匀化（对照开关）；默认 log2 稀有度加权
	UniformWeight bool `yaml:"uniform_weight"`
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
	// 诊断编排侧配置段
	Agent Agent `yaml:"agent"`
	// 终端交互主题段
	TUI TUI `yaml:"tui"`
	// 是否输出调试进度
	Debug bool `yaml:"debug"`
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
			Projection: Projection{
				Method:        strings.TrimSpace(getenv("ARUING_TOOLS_PROJECTION_METHOD")),
				Budget:        parseIntEnv(getenv("ARUING_TOOLS_PROJECTION_BUDGET")),
				Lambda:        parseFloatEnv(getenv("ARUING_TOOLS_PROJECTION_LAMBDA")),
				UniformWeight: parseBoolEnv(getenv("ARUING_TOOLS_PROJECTION_UNIFORM_WEIGHT")),
			},
		},
		Agent: Agent{
			Acquire: Acquire{
				Method:    strings.TrimSpace(getenv("ARUING_AGENT_ACQUIRE_METHOD")),
				MaxRounds: parseIntEnv(getenv("ARUING_AGENT_ACQUIRE_MAX_ROUNDS")),
				Seed:      parseInt64Env(getenv("ARUING_AGENT_ACQUIRE_SEED")),
				Alpha:     parseFloatEnv(getenv("ARUING_AGENT_ACQUIRE_ALPHA")),
				PStar:     parseFloatEnv(getenv("ARUING_AGENT_ACQUIRE_P_STAR")),
				A:         parseFloatEnv(getenv("ARUING_AGENT_ACQUIRE_A")),
				Tau:       parseFloatEnv(getenv("ARUING_AGENT_ACQUIRE_TAU")),
				Delta:     parseFloatEnv(getenv("ARUING_AGENT_ACQUIRE_DELTA")),
				MassFloor: parseFloatEnv(getenv("ARUING_AGENT_ACQUIRE_MASS_FLOOR")),
			},
			Memory: Memory{
				Method: strings.TrimSpace(getenv("ARUING_AGENT_MEMORY_METHOD")),
				LastN:  parseIntEnv(getenv("ARUING_AGENT_MEMORY_LAST_N")),
			},
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
	if v, ok := lookup("ARUING_TOOLS_PROJECTION_METHOD"); ok {
		if t := strings.TrimSpace(v); t != "" {
			out.Tools.Projection.Method = t
		}
	}
	if v, ok := lookup("ARUING_TOOLS_PROJECTION_BUDGET"); ok {
		if n := parseIntEnv(v); n > 0 {
			out.Tools.Projection.Budget = n
		}
	}
	if v, ok := lookup("ARUING_TOOLS_PROJECTION_LAMBDA"); ok {
		if f := parseFloatEnv(v); f > 0 {
			out.Tools.Projection.Lambda = f
		}
	}
	if v, ok := lookup("ARUING_TOOLS_PROJECTION_UNIFORM_WEIGHT"); ok {
		out.Tools.Projection.UniformWeight = parseBoolEnv(v)
	}
	if v, ok := lookup("ARUING_AGENT_ACQUIRE_METHOD"); ok {
		if t := strings.TrimSpace(v); t != "" {
			out.Agent.Acquire.Method = t
		}
	}
	if v, ok := lookup("ARUING_AGENT_ACQUIRE_MAX_ROUNDS"); ok {
		if n := parseIntEnv(v); n > 0 {
			out.Agent.Acquire.MaxRounds = n
		}
	}
	// 种子允许任意 int64（含负值）；0 视为未设置
	if v, ok := lookup("ARUING_AGENT_ACQUIRE_SEED"); ok {
		if n := parseInt64Env(v); n != 0 {
			out.Agent.Acquire.Seed = n
		}
	}
	if v, ok := lookup("ARUING_AGENT_ACQUIRE_ALPHA"); ok {
		if f := parseFloatEnv(v); f > 0 {
			out.Agent.Acquire.Alpha = f
		}
	}
	if v, ok := lookup("ARUING_AGENT_ACQUIRE_P_STAR"); ok {
		if f := parseFloatEnv(v); f > 0 {
			out.Agent.Acquire.PStar = f
		}
	}
	if v, ok := lookup("ARUING_AGENT_ACQUIRE_A"); ok {
		if f := parseFloatEnv(v); f > 0 {
			out.Agent.Acquire.A = f
		}
	}
	if v, ok := lookup("ARUING_AGENT_ACQUIRE_TAU"); ok {
		if f := parseFloatEnv(v); f > 0 {
			out.Agent.Acquire.Tau = f
		}
	}
	if v, ok := lookup("ARUING_AGENT_ACQUIRE_DELTA"); ok {
		if f := parseFloatEnv(v); f > 0 {
			out.Agent.Acquire.Delta = f
		}
	}
	if v, ok := lookup("ARUING_AGENT_ACQUIRE_MASS_FLOOR"); ok {
		if f := parseFloatEnv(v); f > 0 {
			out.Agent.Acquire.MassFloor = f
		}
	}
	if v, ok := lookup("ARUING_AGENT_MEMORY_METHOD"); ok {
		if t := strings.TrimSpace(v); t != "" {
			out.Agent.Memory.Method = t
		}
	}
	if v, ok := lookup("ARUING_AGENT_MEMORY_LAST_N"); ok {
		if n := parseIntEnv(v); n > 0 {
			out.Agent.Memory.LastN = n
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
	return fmt.Errorf("LLM configuration incomplete: missing %s — run \"aruing connect\" to configure interactively, or set config file / ARUING_CONFIG / env (see aruing.example.yaml)", strings.Join(missing, ", "))
}

// 解析布尔环境变量；空或非布尔值返回否，避免误开执行这类高自由度动作
func parseBoolEnv(v string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	return err == nil && b
}

// 解析正浮点环境变量；空、非数字或非正返回 0
// 与整型同口径静默归零：非法值的效果等同未设置，交给后续默认值兼底
func parseFloatEnv(v string) float64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return 0
	}
	return f
}

// 解析正整型环境变量；空、非数字或非正返回 0
func parseInt64Env(v string) int64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

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
