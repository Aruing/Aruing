# 02-investigate-clarify — 验收（chat 路径 · 多轮）

> 强测 investigate 阶段挂起链（beta19）：第 1 轮歧义提问（已明确要求澄清）→ 挂起反问 → 第 2 轮答复 → 续查出报告。人/AI 对照勾选，非自动评分。

## 应出现

- 第 1 轮：agent 挂起并向用户反问（问哪个 ns / 哪个 demo-api），**不**在无答复时直接下唯一根因
- 第 2 轮（答复 team-b）：agent 接收澄清后继续调查，最终结论锁定 team-b/demo-api 的**镜像拉取失败**
- 调查链含 team-b 侧证据痕迹（get/describe/events 类）

## 不应出现

- 第 1 轮不反问、静默只查一个 ns 就下唯一根因
- 答复后不续查、凭第 1 轮旧观察直接编报告
- 编造 Pod 名 / 事件而不调用工具
