# 智能体提示词

大模型角色的系统 prompt，经 `//go:embed` 加载，不散落在业务源码字符串里。

当前文件：

- `parser.md`：把用户问题转成结构化范围和故障现象
- `resolver.md`：定位阶段提议工具调用或提交目标
- `planner.md`：生成故障猜想和白名单取证任务
- `verifier.md`：只根据已登记证据验证猜想
- `reporter.md`：生成不编造证据的诊断报告
- `tower.md`：会话总控 reply / escalate（本步无基线 tool）

提示词不要散落在源码中，除非后续有明确的工程理由
