# 项目当前状态

> 最后更新：2026-07-20（PR #7）

## 当前阶段

`0.0.1-beta2` / 真实闭环：把假角色逐个换成真实现，目标 `aruing run` 在真实 Kubernetes 集群 + 真实 LLM 下端到端产出可追溯的 Markdown 诊断报告。

前置：`0.0.1-beta1` 最小假闭环已完成（Run → Query → Target → Hypothesis → Task → Evidence → Verdict → Report 数据流跑通，所有模块边界立住）。

## 工作单元

| # | 模块 | 状态 | 备注 |
| - | --- | --- | --- |
| 1 | LLM 客户端 | ✅ | PR #3 `internal/llm`，OpenAI 兼容客户端 + JSON 输出 + 重试 |
| - | PR-Agent 自动评审基建 | ✅ | PR #4 计划外插入，每个 PR 自动评审 |
| 3 | Parser | ✅ | PR #5 `dc09495` 接 LLM；PR #6 `8fad9ea` 补 ref 校验 + 业务重试 |
| - | 仓库文档规范 skill | ✅ | PR #7 `aruing-docs` skill |
| 2 | Kubernetes 工具 | 未开始 | `internal/tools/k8s` 接 client-go |
| 4 | Resolver | 未开始 | 多轮协议，最复杂，可能拆 2 PR |
| 5 | Planner | 未开始 | 依赖 #1 #2 |
| 6 | Verifier | 未开始 | 依赖 #1 |
| 7 | Reporter | 未开始 | 依赖 #1 |
| 8 | 配置层 | 未开始 | `internal/config` 集中收敛 env |

替换原则：一次只换一个角色，其他环节继续用假实现，假闭环始终可跑、可测（`make test` 默认无 LLM env，走 fake）。

## 已完成 PR

- #7 docs: add aruing-docs skill for repo documentation conventions（`a9b74a8`）
- #6 fix(parser): validate ref uniqueness and retry on inconsistent LLM output（`8fad9ea`）
- #5 feat: replace FakeParser with LLMParser（`dc09495`）
- #4 ci: add pr-agent auto review（`39abbd4`）
- #3 feat: llm client（`924a16e`）
- #2 Feat/check action（`ddc2181`）
- #1 Feat/init mvp（`0db3650`）

## 下一步

`feat/k8s-tools`：接入 client-go 实现 `internal/tools/k8s`，提供第一批只读工具（`list_pods` / `get_service` / `get_endpoints` / `get_events` / `get_logs` 等）。

理由：`#4 Resolver` 真实化需要真实 K8s 工具查集群确认 `Target`；先有工具，才能在 Resolver 多轮协议设计中真正端到端验证，避免设计了协议却没法跑。

## 当前硬约束摘要

完整清单见 [`docs/architecture.md` 硬约束段](architecture.md#硬约束)。摘要：

- `Run` 不嵌套子实体，扁平 ID 关联
- `Query` 线索必须经 Resolver 真实确认才能成为 `Target`
- 模型输出不能冒充 `Evidence`
- `Verdict` 必须引用 `Evidence`
- prompt 从文件加载（`//go:embed`），不写死代码
- 工具只读

## 预留问题入口

详细表格位于 `arui-note/aruing/plan/0.0.1-beta2/2026-7-18.md` §4。索引：

- **P-1 / P-2 / P-3**：pr-agent 自身 review 提出但未修（concurrency group 跨 PR 互相取消、OPENAI_KEY 走第三方代理暴露面、pr-agent 锁死单模型）
- **L-1 ~ L-9**：LLMParser 相关；L-6 / L-7 已在 PR #6 关闭；L-1（下游 fake 角色与新节点 ID 不匹配）等 #4 Resolver 解决；L-8（CLI 翻译 LLM error）等 #8 config；L-9（模型能力作为 Report 指标）留正式版之后
- **C-1**：env 散在 `main.go`，等 #8 `internal/config` 收敛
- **S-1**：曾贴出的 key 仍建议撤换
