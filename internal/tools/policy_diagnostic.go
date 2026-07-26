package tools

import (
	"encoding/json"
	"strings"
)

// 诊断策略：在只读策略基础上放行 kubectl exec，用于在 Pod 内跑连通性/DNS 探针
//
// 与 ReadonlyPolicy 一样挂在 Dispatcher.Execute 前（#12 能力/授权分开）。
// 不枚举容器内二进制——开启即预批准 exec 这类动作，由 Planner prompt 引导只做诊断
// 探针（curl/nslookup/nc/...）。避免耦合目标镜像内容、掉进适配某命令的陷阱（#2）。
// 其余 kubectl 子命令仍按 ReadonlyPolicy：读类放行、写类（apply/delete/...）拒绝。
type DiagnosticPolicy struct{}

// 创建诊断策略实例
func NewDiagnosticPolicy() Policy {
	return DiagnosticPolicy{}
}

// k8s exec 放行（不校验二进制）；其余委托给 ReadonlyPolicy 保持读/写区分
// 非 k8s 工具走 ReadonlyPolicy 的既有宽松（fake.* 等允许）
func (DiagnosticPolicy) Check(toolName string, args json.RawMessage) (Decision, string) {
	if strings.TrimSpace(toolName) == "k8s" {
		if argv, err := extractK8sArgv(args); err == nil && len(argv) > 0 && strings.ToLower(argv[0]) == "exec" {
			return DecisionAllow, "k8s exec permitted by diagnostic policy"
		}
	}
	return ReadonlyPolicy{}.Check(toolName, args)
}
