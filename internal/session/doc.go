// 会话包放用户侧多轮对话的壳：Session、Message、Turn 入口与可替换的 Responder
//
// 对话层不承担诊断证据账本；正式诊断仍走 core.Run 与 Orchestrator.Execute
// 助手回复可通过 Message.RunID 引用某次 Run，不嵌套证据链
//
// 本包只定义 Store 接口；内存实现在 internal/store，便于以后换成持久化而不改 Turn
// Responder 决定「本轮怎么答」；当前 Echo / Diagnose 为脚手架，后续由 Tower 替换
package session
