// 定位阶段的编排可见循环类型
//
// 定位不再是一次性 Resolve 黑盒：编排层持有 ResolveState，每轮调用 ResolveDriver.Next
// 角色只返回意图（提议工具、提交目标或失败），工具执行与 ID 发放仍走编排边界
// 这样满足 architecture 硬约束 #16，并为后续多轮升级保留同一信任模型
package agent

import (
	"context"
	"encoding/json"

	"aruing/internal/core"
)

// 定位阶段默认最多执行的工具调用次数，防止模型空转
// 编排在即将执行超出预算的调用时失败；submit_targets 不受此计数拦截
const defaultResolveMaxRounds = 8

// 定位循环的动作类型，由 ResolveDriver 每轮返回其一
type ResolveActionKind string

const (
	// 提议一次或多次工具调用，由编排串行执行并回填证据
	ResolveActionCallTool ResolveActionKind = "call_tool"
	// 提交已确认目标，校验通过后结束定位阶段
	ResolveActionSubmitTargets ResolveActionKind = "submit_targets"
	// 无法确认目标时显式失败，编排中止本次运行
	ResolveActionFail ResolveActionKind = "fail"
)

// 编排持有并每轮回喂给定位驱动的只读视图
// 进程内结构，不作为持久化实体；未来 store 若需恢复过程态再提升字段
type ResolveState struct {
	// 当前运行的问题结构，含已回填系统编号的节点
	Query core.Query
	// 本阶段已执行的任务，含编排发放的编号与参数
	Tasks []core.Task
	// 本阶段已登记的证据，含编排发放的编号与工具返回内容
	Evidence []core.Evidence
	// 已完成的工具调用次数，用于预算控制
	Round int
	// 允许的工具调用上限
	MaxRounds int
}

// 定位驱动提议的一次工具调用，尚未分配任务编号
// 编排负责发 Task ID、经 Dispatcher 执行并登记 Evidence
type ProposedToolCall struct {
	// 白名单工具名称，必须存在于 Registry
	ToolName string
	// 传给工具的结构化参数，执行前由 Policy 与工具自身校验
	Arguments json.RawMessage
	// 本次取证目的，便于审计与回喂摘要
	Purpose string
	// 关联的问题节点或其他已知数据编号
	Refs []string
}

// 定位驱动提议确认的目标内容，尚未分配 Target 编号
// 编排负责校验 NodeID / EvidenceIDs 并发放 Target ID
type ProposedTarget struct {
	// 必须指向当前 Query 内已有节点
	NodeID string
	// 开放的目标类型，由领域定义
	Type string
	// 已确认的身份属性
	Attrs map[string]string
	// 支撑该确认的本阶段证据编号；假驱动可为空，真路径应引用实际查询证据
	EvidenceIDs []string
}

// 定位驱动单轮输出的意图，由编排解释并执行副作用
type ResolveAction struct {
	// 本轮动作类型
	Action ResolveActionKind
	// 面向日志与回喂的简短说明
	Reason string
	// call_tool 时至少一条；编排按顺序串行执行
	ToolCalls []ProposedToolCall
	// submit_targets 时至少一条
	Targets []ProposedTarget
	// fail 时的错误说明，优先于 Reason 展示给调用方
	Error string
}

// 定位阶段每轮驱动接口：只提议下一步，不执行工具、不自造领域编号
// Fake 与 LLM 实现共享该边界，Orchestrator 只依赖此接口
type ResolveDriver interface {
	// 根据当前状态返回下一动作；ctx 取消时返回错误
	Next(ctx context.Context, state ResolveState) (ResolveAction, error)
}
