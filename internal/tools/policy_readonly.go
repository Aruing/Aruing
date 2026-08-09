package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// 最小只读策略：对集群工具按集群命令行子命令白名单授权，写操作与未知子命令拒绝
// 非集群已注册工具默认放行（当前假闭环依赖假工具前缀），未识别名称仍拒绝
// 授权真相在此，不把「只能查询」写死进集群工具作为唯一防护
type ReadonlyPolicy struct{}

// 创建默认只读策略
func NewReadonlyPolicy() Policy {
	return ReadonlyPolicy{}
}

// 集群只读子命令白名单（参数列表首项）；不枚举资源类型
var k8sReadonlyCommands = map[string]struct{}{
	"get":           {},
	"describe":      {},
	"logs":          {},
	"top":           {},
	"api-resources": {},
	"api-versions":  {},
	"explain":       {},
	"version":       {},
	"cluster-info":  {},
	"auth":          {}, // 鉴权可否访问等只读探查
	"config":        {}, // 查看集群配置上下文；变更类仍可能被运维侧限制
	"diff":          {},
	"wait":          {},
	"events":        {}, // 部分发行版提供独立事件子命令
}

// 明确拒绝的写/交互类子命令；命中时给出更清晰的原因
var k8sDeniedCommands = map[string]struct{}{
	"apply":        {},
	"create":       {},
	"delete":       {},
	"patch":        {},
	"replace":      {},
	"edit":         {},
	"exec":         {},
	"attach":       {},
	"cp":           {},
	"port-forward": {},
	"run":          {},
	"cordon":       {},
	"uncordon":     {},
	"drain":        {},
	"taint":        {},
	"label":        {},
	"annotate":     {},
	"scale":        {},
	"autoscale":    {},
	"rollout":      {},
	"set":          {},
	"expose":       {},
	"debug":        {},
	"proxy":        {},
	"certificate":  {},
}

// 对集群工具检查参数列表只读性；对其它已知风格的工具名允许；空名称或未知规则拒绝
func (ReadonlyPolicy) Check(toolName string, args json.RawMessage) (Decision, string) {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return DecisionDeny, "tool name is required"
	}

	switch name {
	case "k8s":
		return checkK8sReadonly(args)
	default:
		// 假工具与其它后端：本阶段不拦截；真正的未知工具会在注册表查找阶段失败
		// 若名称明显像写操作专用（未来可扩展），此处保持宽松以兼容假工具前缀
		return DecisionAllow, "non-k8s tool allowed by readonly policy"
	}
}

// 解析集群工具参数中的参数列表并按子命令白名单决策
func checkK8sReadonly(args json.RawMessage) (Decision, string) {
	argv, err := extractK8sArgv(args)
	if err != nil {
		return DecisionDeny, err.Error()
	}
	if len(argv) == 0 {
		return DecisionDeny, "k8s argv must not be empty"
	}

	cmd := strings.ToLower(strings.TrimSpace(argv[0]))
	if cmd == "" {
		return DecisionDeny, "k8s command must not be empty"
	}

	if _, denied := k8sDeniedCommands[cmd]; denied {
		return DecisionDeny, fmt.Sprintf("k8s command %q is not allowed by readonly policy", cmd)
	}
	if _, ok := k8sReadonlyCommands[cmd]; !ok {
		return DecisionDeny, fmt.Sprintf("k8s command %q is not in the readonly allowlist", cmd)
	}

	// 滚动更新已在拒绝表；鉴权仅允许可否访问等只读子路径
	if cmd == "auth" {
		if len(argv) < 2 || strings.ToLower(argv[1]) != "can-i" {
			return DecisionDeny, `k8s "auth" only allows "can-i" under readonly policy`
		}
	}
	// 配置仅允许只读查看类
	if cmd == "config" {
		if len(argv) < 2 {
			return DecisionDeny, `k8s "config" requires a subcommand`
		}
		sub := strings.ToLower(argv[1])
		switch sub {
		case "view", "get-contexts", "current-context", "get-clusters", "get-users":
			// 允许
		default:
			return DecisionDeny, fmt.Sprintf("k8s config %q is not allowed by readonly policy", sub)
		}
	}

	return DecisionAllow, fmt.Sprintf("k8s command %q is readonly", cmd)
}

// 从集群工具参数中提取参数列表，不完整解析其它字段
func extractK8sArgv(args json.RawMessage) ([]string, error) {
	if len(strings.TrimSpace(string(args))) == 0 {
		return nil, fmt.Errorf("k8s arguments are required")
	}
	var payload struct {
		Argv []string `json:"argv"`
	}
	if err := json.Unmarshal(args, &payload); err != nil {
		return nil, fmt.Errorf("k8s arguments are not valid JSON: %w", err)
	}
	return payload.Argv, nil
}
