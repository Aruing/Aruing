package tools

import (
	"encoding/json"
	"fmt"
)

// 授权决策结果。能力开放由 Tool/Registry 表达，是否允许调用由 Policy 决定。
// RequireApproval 预留给后续产品路径，本阶段只实现 Allow / Deny。
type Decision int

const (
	// 允许执行工具
	DecisionAllow Decision = iota
	// 拒绝执行工具，不调用 Tool.Execute
	DecisionDeny
	// 需要用户确认后再执行（本阶段未接线，枚举预留）
	DecisionRequireApproval
)

// 返回稳定字符串，便于日志与错误信息
func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionDeny:
		return "deny"
	case DecisionRequireApproval:
		return "require_approval"
	default:
		return fmt.Sprintf("decision(%d)", int(d))
	}
}

// 在调度器执行工具前做授权判断
// 输入为工具名称与原始参数 JSON；Policy 不负责枚举资源类型，只判断是否允许本次调用
type Policy interface {
	// 返回决策与可读原因；Deny 时 reason 应说明拒绝依据
	Check(toolName string, args json.RawMessage) (Decision, string)
}

// 始终允许，用于测试或明确关闭策略的场景
type AllowAllPolicy struct{}

// 对任意工具与参数返回 Allow
func (AllowAllPolicy) Check(string, json.RawMessage) (Decision, string) {
	return DecisionAllow, "allow-all policy"
}

// 始终允许的策略实例
func NewAllowAllPolicy() Policy {
	return AllowAllPolicy{}
}
