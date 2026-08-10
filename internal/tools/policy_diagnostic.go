package tools

import (
	"encoding/json"
	"strings"
)

// 诊断策略：在只读策略基础上放行集群内执行，用于在容器内跑连通性与域名解析探针
//
// 与只读策略一样挂在调度器执行前（能力与授权分开）。
// 不枚举容器内二进制——开启即预批准执行类动作，由规划提示词引导只做诊断探针。
// 避免耦合目标镜像内容、掉进适配某命令的陷阱。
// 其余集群子命令仍按只读策略：读类放行、写类拒绝。
type DiagnosticPolicy struct{}

// 创建诊断策略实例
func NewDiagnosticPolicy() Policy {
	return DiagnosticPolicy{}
}

// 集群内执行放行（不校验二进制）；其余委托给只读策略保持读与写区分
// 非集群工具走只读策略的既有宽松（假工具等允许）
func (DiagnosticPolicy) Check(toolName string, args json.RawMessage) (Decision, string) {
	if strings.TrimSpace(toolName) == "k8s" {
		if argv, err := extractK8sArgv(args); err == nil && len(argv) > 0 && strings.ToLower(argv[0]) == "exec" {
			return DecisionAllow, "k8s exec permitted by diagnostic policy"
		}
	}
	return ReadonlyPolicy{}.Check(toolName, args)
}
