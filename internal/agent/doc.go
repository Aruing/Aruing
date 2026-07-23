// 智能体包放诊断过程中的推理角色
//
// 这里的智能体先只是进程内的角色边界，不代表独立服务
// 解析器负责理解问题；定位器通过编排可见循环确认目标；规划器生成猜想和取证任务；
// 验证器只基于证据判断；报告器把过程整理成人能读的报告
//
// Parser / Resolver / Planner / Verifier 已可接 LLM（prompt 经 //go:embed）；Reporter 当前仍为假实现
// 定位阶段遵守 architecture #16：角色只提议意图，工具经 Dispatcher 统一执行，ID 由编排发放
// 规划阶段为单次 Plan 调用，不在角色内多轮调 Tool；Hypothesis/Task 编号经 Factory 在规划器内回填
// 验证阶段为单次 Verify 调用；Verdict 只能引用已登记 Evidence，编号经 Factory 回填
package agent
