// 智能体包放诊断过程中的推理角色，以及会话总控 Tower
//
// 这里的智能体先只是进程内的角色边界，不代表独立服务
// 解析器负责理解问题；定位器通过编排可见循环确认目标；规划器生成猜想和取证任务；
// 验证器只基于证据判断；报告器把过程整理成人能读的报告
// Tower 实现 session.Responder：本轮 reply 或 escalate（正式诊断），不在本包写 Message
//
// Parser / Resolver / Planner / Verifier / Reporter / Tower 已可接 LLM（prompt 经 //go:embed）
// 定位阶段遵守 architecture #16：角色只提议意图，工具经 Dispatcher 统一执行，ID 由编排发放
// 规划阶段为单次 Plan 调用，不在角色内多轮调 Tool；Hypothesis/Task 编号经 Factory 在规划器内回填
// 验证阶段为单次 Verify 调用；Verdict 只能引用已登记 Evidence，编号经 Factory 回填
// 报告阶段为单次 Report 调用；结论对齐 Verdict，证据引用不得越界，Report 编号经 Factory 回填
// Tower 本步无基线 tool 环；escalate 经 session.Escalate 建 Run 并调 Execute
package agent
