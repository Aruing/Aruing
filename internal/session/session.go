// 会话包放用户侧多轮对话的壳：会话、消息、轮次入口与可替换的应答器
//
// 对话层消息不嵌证据链；正式诊断仍走核心运行与编排器执行
// 助手回复可引用某次运行编号；报告与证据权威源为进程内诊断账本
//
// 本包定义存储与诊断账本接口；内存实现在存储包
// 应答器决定「本轮怎么答」；产品路径为基线塔（直接回复 / 调工具 / 升格诊断）
// 回显实现便于长期可测；临时诊断应答器为强制每轮诊断的脚手架，非默认产品脸
// 升格建运行见升级接口（成功路径写诊断账本），供基线塔与临时诊断应答器共用
package session

import "time"

// 消息角色与展示用模式常量；模式字段只是标签，不是用户意图枚举
const (
	// 用户发出的消息
	RoleUser = "user"
	// 助手回复的消息
	RoleAssistant = "assistant"

	// 基线直接回复（未升格诊断）
	ModeBaseline = "baseline"
	// 本轮走了正式诊断管道
	ModeDiagnostic = "diagnostic"
	// 上下文压缩检查点（模型视图用，权威历史仍全量保留）
	ModeCheckpoint = "checkpoint"
)

// 一场用户与助手的对话容器
// 只保存会话自身属性；消息按会话编号在存储层关联，不嵌装消息列表
type Session struct {
	// 会话编号，格式为会话前缀加时间有序全局唯一标识，创建时由编号工厂发放
	ID string
	// 会话创建时间
	CreatedAt time.Time
	// 最近一次写入消息或状态变更时间
	UpdatedAt time.Time
}

// 会话中的一条用户或助手消息
// 助手消息可选挂运行编号，表示本条回复关联的正式诊断运行
type Message struct {
	// 消息编号，格式为消息前缀加时间有序全局唯一标识
	ID string
	// 所属会话编号
	SessionID string
	// 角色：用户或助手
	Role string
	// 面向人阅读的正文
	Content string
	// 消息创建时间
	CreatedAt time.Time
	// 可选；本条助手回复关联的诊断运行编号
	RunID string
	// 可选；基线、诊断或检查点，便于展示，不是意图枚举
	Mode string
}
