// 判断模块负责把候选猜想、执行任务和已有证据转换为可回溯的验证结果
//
// 生产实现为 LLMVerifier；测试替身见 agenttest.FakeVerifier
// 调用方必须提供同一轮规划的猜想、任务和证据，所有引用都要属于同一次运行
package agent
