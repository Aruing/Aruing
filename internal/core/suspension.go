package core

// 运行挂起：编排某阶段需要用户澄清时产出
//
// Stage 标明挂起发生在哪一阶段，当前仅 resolve 使用；
// 后续 investigate / parse 等复用本结构，只需在 Resume 派发加 case。
// Report 与挂起互斥：Outcome 中恰一非空。
type Suspension struct {
	// 挂起的运行编号
	RunID string `json:"runId"`
	// 所属会话编号；升格路径才有，单次 run 可空
	SessionID string `json:"sessionId,omitempty"`
	// 挂起阶段，如 resolve；未来 investigate 等复用
	Stage string `json:"stage"`
	// 面向用户的澄清问题
	Question string `json:"question"`
	// 可选候选（如多个命名空间），可空
	Options []string `json:"options,omitempty"`
}

// 编排一次执行的结果：完成报告或挂起问用户
//
// 不变式：Report 与 Suspension 恰一非空；均空为编程错误。
// Evidence 在完成时填充（供链渲染）；挂起时通常为空。
type Outcome struct {
	// 完成时非空
	Report *Report `json:"report,omitempty"`
	// 完成时的证据链（定位→侦察→调查），供命令行渲染
	Evidence []Evidence `json:"evidence,omitempty"`
	// 挂起时非空
	Suspension *Suspension `json:"suspension,omitempty"`
}

// 定位阶段挂起时 Stage 取值
const StageResolve = "resolve"
