package session

import "time"

// 消息角色与展示用模式常量；Mode 只是标签，不是用户意图枚举
const (
	// 用户发出的消息
	RoleUser = "user"
	// 助手回复的消息
	RoleAssistant = "assistant"

	// 基线直接回复（未升格诊断）
	ModeBaseline = "baseline"
	// 本轮走了正式诊断管道
	ModeDiagnostic = "diagnostic"
)

// 一场用户与助手的对话容器
// 只保存会话自身属性；消息按 SessionID 在存储层关联，不嵌装消息列表
type Session struct {
	// 会话编号，格式为 sess_ + UUIDv7，创建时由 Factory 发放
	ID string
	// 会话创建时间
	CreatedAt time.Time
	// 最近一次写入消息或状态变更时间
	UpdatedAt time.Time
}

// 会话中的一条用户或助手消息
// 助手消息可选挂 RunID，表示本条回复关联的正式诊断运行
type Message struct {
	// 消息编号，格式为 msg_ + UUIDv7
	ID string
	// 所属会话编号
	SessionID string
	// 角色：user 或 assistant
	Role string
	// 面向人阅读的正文
	Content string
	// 消息创建时间
	CreatedAt time.Time
	// 可选；本条助手回复关联的诊断 Run 编号
	RunID string
	// 可选；baseline 或 diagnostic，便于展示，不是意图枚举
	Mode string
}
