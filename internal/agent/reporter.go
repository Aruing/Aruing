// 报告模块负责把验证结果和证据引用整理为最终用户输出
//
// 生产实现为 LLMReporter；测试替身见 agenttest.FakeReporter
// 调用方必须提供同一运行中的判断和证据，报告不能改变已有判断结果
package agent
