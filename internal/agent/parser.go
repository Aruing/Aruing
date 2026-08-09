// 解析模块负责把运行中的原始问题转换为未验证的问题结构
//
// 当前生产实现为 LLMParser（parser_llm.go）；测试替身见 agenttest.FakeParser
// 调用方必须传入有效的运行数据和可取消上下文，返回结果只供后续定位阶段使用
package agent
