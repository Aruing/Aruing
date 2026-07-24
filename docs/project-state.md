# 项目当前状态

> 最后更新：2026-07-24（beta3 多轮-3 prompt 升级 + 生产预算）

## 当前阶段

**`0.0.1-beta3` / 调查循环**：让 Planner/Verifier 能基于已有证据迭代取证，像 SRE 顺着证据链找根因，而不是开局盲猜一次就交报告。

目标与完成标志见笔记 `arui-note/aruing/plan/milestone/0.0.1-beta3.md`。

前置：`0.0.1-beta2` 真实闭环已完成（五角色全接 LLM，`aruing run` 端到端产出可追溯 Markdown 报告）。`0.0.1-beta1` 最小假闭环已完成（数据流跑通，模块边界立住）。

## 工作单元

| # | 模块 | 状态 | 备注 |
| - | --- | --- | --- |
| 1 | LLM 客户端 | ✅ | PR #3 `internal/llm`，OpenAI 兼容客户端 + JSON 输出 + 重试 |
| - | PR-Agent 自动评审基建 | ✅ | PR #4 计划外插入，每个 PR 自动评审 |
| 3 | Parser | ✅ | PR #5 `dc09495` 接 LLM；PR #6 `8fad9ea` 补 ref 校验 + 业务重试 |
| - | 仓库文档规范 skill | ✅ | PR #7 `aruing-docs` skill |
| - | PR 描述规范 skill | ✅ | PR #9 `aruing-pr-description` skill |
| 2 | Kubernetes 工具 | ✅ | `ToolSpec` + `Registry.Specs` + 单一 shell-less `k8s` Tool（argv 直调 kubectl）；#4a 起 wiring 可按需注册 |
| 4 | Resolver | ✅ #4a+#4b | 方案 A：编排可见定位循环 + `ResolveDriver`；`LLMResolver`（`GenerateJSON` + `Registry.Specs`）；`FakeResolver` 按 Query 节点出 Target（关 L-1）；Policy + 可选 k8s 注册 |
| 5 | Planner | ✅ | `LLMPlanner`：单次 `Plan` + `Registry.Specs`；局部 ref 回填 Hypothesis/Task ID；业务重试；wiring 在 LLM 齐备时启用 |
| 6 | Verifier | ✅ | `LLMVerifier`：单次 `Verify`；只引用已登记 Evidence；Factory 回填 Verdict ID；业务重试；wiring 在 LLM 齐备时启用 |
| 7 | Reporter | ✅ | `LLMReporter`：单次 `Report`；结论对齐 Verdict；证据引用不得越界；Factory 回填 Report ID；业务重试；wiring 在 LLM 齐备时启用 |
| 8 | 配置层 | ✅ | `internal/config`：`Load`/`LoadFrom` 收敛 `ARUING_LLM_*` 与 `ARUING_KUBECTL_PATH`；wiring 只吃 `Config`；CLI `formatRunError` 最小 L-8 |
| R-1 | CLI Markdown 渲染 | ✅ | PR #19 `renderMarkdown` 纯函数渲染；CLI 默认 Markdown，`--format json` 保留 |
| 多轮-1 | Planner 接口扩展 | ✅ | 引入 `PlanState`（Query/Targets/Evidence/Verdicts），`Plan(ctx, PlanState)`；首轮零行为变化，锁定调查循环的输入契约 |
| 多轮-2 | 调查循环主体 | ✅ | `investigateLoop`：Plan→Execute→Verify 循环，证据不足带历史再 Plan；预算/setter 对齐 resolveLoop；默认 1 轮等价 beta2，循环能力由单测覆盖 |
| 多轮-3 | Planner prompt + 预算 | ✅ | prompt 自适应（后续轮补查 insufficient、空任务=查完）；`validatePlannerOutput` 分轮次；wiring 生产预算调到 3；真正开启迭代取证 |

替换原则：一次只换一个角色，其他环节继续用假实现，假闭环始终可跑、可测（`make test` 默认无 LLM env，走 fake）。LLM 配置齐全时 wiring 同时启用 LLMParser + LLMResolver + LLMPlanner + LLMVerifier + LLMReporter。

## 已完成 PR

- #19 feat(cli): render Markdown diagnosis report by default（`3deff24`）
- #18 feat(config): centralize env loading and minimal LLM error hints（`7462ec3`）
- #17 feat(agent): replace FakeReporter with LLMReporter（`808d1f5`）
- #16 feat(agent): replace FakePlanner and FakeVerifier with LLM implementations（`6f0e977`）
- #15 feat(agent): orchestrated resolve loop and LLMResolver（`73d015b`）
- #13 feat(tools): add readonly Policy and optional k8s wiring（`f6209e1`）
- #12 docs: record linear Orchestrator as temporary single-turn driver（`89603ce`）
- #11 feat: add shell-less Kubernetes tool and discoverable specs（`19b449a`）
- #10 fix: correct tool readonly constraint（`fcf8421`）
- #9 docs: add aruing-pr-description skill（`3775e7e`）
- #8 docs: add repo documentation per aruing-docs skill（`19b8973`）
- #7 docs: add aruing-docs skill for repo documentation conventions（`a9b74a8`）
- #6 fix(parser): validate ref uniqueness and retry on inconsistent LLM output（`8fad9ea`）
- #5 feat: replace FakeParser with LLMParser（`dc09495`）
- #4 ci: add pr-agent auto review（`39abbd4`）
- #3 feat: llm client（`924a16e`）
- #2 Feat/check action（`ddc2181`）
- #1 Feat/init mvp（`0db3650`）

## 下一步

**下一项：多轮-3 真集群验收**（`make run-llm`，确认报告体现多轮取证、insufficient 猜想被补齐或合理收尾）。验收后进入 多轮-4：工具失败容错 + 报告调查链可见。设计见笔记 `arui-note/aruing/plan/0.0.1-beta3/`。

候选（未排期，做到时再转下一项）：
- 多轮-4 工具失败容错 + 报告调查链可见

阶段计划与设计推理记录在笔记 `arui-note/aruing/plan/`。

已落地要点：

1. **#4a**：`tools.Policy` + `ReadonlyPolicy` 挂在 `Dispatcher.Execute` 前；`wiring` 在 kubectl 可用时可选注册 `k8s`
2. **#4b**：`Orchestrator.resolveTargets` 循环；`ResolveDriver` / `LLMResolver` / 按节点的 `FakeResolver`；Target ID 与定位阶段 Task/Evidence ID 由编排发放；L-1 关闭
3. **#5**：`LLMPlanner` 单次 `Plan` + `Registry.Specs`；局部 ref 回填 Hypothesis/Task ID；业务重试；不在规划阶段多轮调 Tool（#15–#16）
4. **#6**：`LLMVerifier` 单次 `Verify`；每条 Hypothesis 恰好一条 Verdict；`evidence_ids` 必须属于输入 Evidence；Factory 回填 Verdict ID；业务重试
5. **#7**：`LLMReporter` 单次 `Report`；结论覆盖每条 Verdict 且 `result` 一致；`evidence_ids` ⊆ 对应 Verdict 证据集；Factory 回填 Report ID；业务重试
6. **#8**：`internal/config` 唯一读 `ARUING_*`；`cmd` 不直接 `os.Getenv` 业务键；L-8 最小 `formatRunError`
7. **R-1**：`renderMarkdown` 纯函数渲染；CLI 默认 Markdown，`--format json` 保留

## 编排与多轮（2026-7-22 已确认）

| 项 | 结论 |
| --- | --- |
| 当前目标 | 最小**单轮**真实诊断；线性 `Orchestrator` 可继续用 |
| 定位阶段 | 编排内小循环（`ResolveDriver`），非用户 Session |
| 会否大规模重构 | **否（非必然）**；主要改编排 + 角色调用方式，core/tools 多半保留 |
| 延后成本量级 | 系统内多轮约数人日；完整 Session 对话约 1–3 周；若把直线冻成全局契约则更高 |
| 现在是否开多轮大改 | **否**；多轮是编排升级，不是推翻领域模型/工具层 |

单轮期禁止事项与详细推理见笔记 `arui-note/aruing/plan/0.0.1-beta2/2026-7-22.md`；公开硬约束见 `architecture.md` #15–#17。

## 当前硬约束摘要

完整清单见 [`docs/architecture.md` 硬约束段](architecture.md#硬约束)。摘要：

- `Run` 不嵌套子实体，扁平 ID 关联
- `Query` 线索必须经 Resolver 真实确认才能成为 `Target`
- 模型输出不能冒充 `Evidence`
- `Verdict` 必须引用 `Evidence`
- prompt 从文件加载（`//go:embed`），不写死代码
- 工具接口不限定读写；能力按后端 Tool + Schema 开放，授权由 `Policy`（挂在 Dispatcher 执行前）与注册控制；当前默认 `ReadonlyPolicy`
- 不枚举 K8s 资源类型或子命令；`k8s` Tool 用 argv 表达完整 kubectl 能力
- 线性 Orchestrator 是单轮临时驱动器；角色不私自多轮调 Tool；多轮升级保留扁平模型与 Dispatcher（见 architecture #15–#17）
- 编号与执行：Tool 只经 Dispatcher；定位阶段 `ResolveDriver` 只返回意图，`Target`/定位 `Task`/`Evidence` ID 由编排发放；规划阶段 `Hypothesis`/`Task` ID 由 `LLMPlanner` 经 `Factory` 回填；验证阶段 `Verdict` ID 由 `LLMVerifier` 经 `Factory` 回填，且只能引用已登记 Evidence；报告阶段 `Report` ID 由 `LLMReporter` 经 `Factory` 回填，结论对齐 Verdict 且证据引用不得越界

## 预留问题入口

详细表在笔记 `arui-note/aruing/plan/`（含 P/L/C/S/O）。公开侧一句话：

| 编号 | 一句话 |
| --- | --- |
| L-8 | CLI 已有最小 `formatRunError`；更细分类可随配置扩展再补 |
| C-1 | ✅ 已收敛到 `internal/config`（#8） |
| O-1 | 用户侧多轮 / Session 未开 |
| R-1 | ✅ CLI 默认 Markdown，`--format json` 保留 |

更多条目与关闭条件见笔记仓 plan。
