// 定位模块负责把问题中的未验证线索转换为后续诊断可以使用的已确认目标
//
// 真实定位由 LLMResolver 通过编排可见循环提议工具并消费证据
// Target 的系统编号由编排边界发放，本模块只产出 ProposedTarget 内容
// 测试替身见 agenttest.FakeResolver
package agent
