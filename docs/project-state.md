# 项目当前状态

> 最后更新：2026-07-26（beta4 诊断全景-5 exec 策略；beta4 六条完成标志全绿）

## 当前阶段

**版本 `0.1.0` / 可追问的诊断助手**（进行中）：版本远景见笔记 `arui-note/aruing/plan/version/0.1.0.md`。

**当前里程碑 `0.1.0-beta4` / 诊断信息全景**（**六条完成标志全绿，待关闭**）：让单轮诊断从「盲猜 + 窄框」变为「侦察集群 → 有上下文判断 → 反思多解释」。三根柱子：集群侦察（事实层）、Verifier 拿 Query + 定位证据复用（上下文层）、反思环节（推理层）；exec 策略作为配套。目标与完成标志见笔记 `arui-note/aruing/plan/milestone/0.1.0-beta4.md`。

前置：`0.0.1-beta3` 调查循环已完成（迭代取证 + pivot + 容错 + 报告调查链）。`0.0.1-beta2` 真实闭环已完成。`0.0.1-beta1` 最小假闭环已完成。

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
| 多轮-4a | 工具失败容错 | ✅ | `executeTask` 工具失败透传 `errToolFailed` + 合成 error evidence；编排层（定位+调查共用）容忍继续，仅 ctx 取消传播；未来改暂停问用户只改编排层 |
| 多轮-4b | 报告调查链可见 | ✅ | `Execute` 返回 `(Report, []Evidence, error)`；`renderMarkdown` 加「证据明细」表（证据编号/命令视图/摘要+失败标记）；仅 markdown，json 不变 |
| 全景-1 | Verifier 拿 Query | ✅ | `Verify` 加 `Query` 入参；FakeVerifier 忽略、LLMVerifier payload 带 query（goal+节点文本）；prompt 补「比对证据与用户提问现象/对象」 |
| 全景-2 | 定位证据复用 | ✅ | `resolveLoop` 透出定位阶段证据；`investigateLoop` 加 `seedEvidence` 入参，作为首轮 `PlanState.Evidence` 复用，不白查已取信息 |
| 全景-3 | 集群侦察 | ✅ | `reconCluster` 走 `executeTask`（Factory 发 Task ID），跑一次只读 `kubectl api-resources` 发现集群资源类型（含 CRD）；精简 `ClusterResources` 喂 Planner payload（`cluster_resources`）；侦察 Evidence 进报告链（透明、失败也落 error evidence）但**不进 Verifier 输入**；`parseAPIResources` 锚定 NAMESPACED 列解析；`reconEnabled` 由 wiring 在 k8s 注册时开启，无集群环境静默跳过 |
| 全景-4 | 反思环节（轻量版） | ✅ | 仅 `planner.md` prompt 强化：首轮猜想覆盖不同根因家族；后续轮新增「防确认偏误」规则（考虑替代解释 + 安排区分性取证）。三场景真集群回归：清晰单因场景不膨胀（多查替代路由层即收尾），真实歧义场景更严谨（不再从相关性证据自信下结论）。未做结构化反思阶段（边际价值在 checkpoint 看不出） |
| 全景-5 | exec 策略 | ✅ | 新增 `DiagnosticPolicy`（exec 放行、不校验二进制，避免适配镜像内容的陷阱 #2）；`config.Tools.AllowDiagnosticExec` 由 `ARUING_ALLOW_DIAGNOSTIC_EXEC` 控制，默认关；wiring 按开关选 `DiagnosticPolicy`/`ReadonlyPolicy`；planner.md 补 Pod 内探针（curl/nslookup/nc）指引。逐次审批（RequireApproval 接线）留辅助修复阶段 |

替换原则：一次只换一个角色，其他环节继续用假实现，假闭环始终可跑、可测（`make test` 默认无 LLM env，走 fake）。LLM 配置齐全时 wiring 同时启用 LLMParser + LLMResolver + LLMPlanner + LLMVerifier + LLMReporter。

> PR 与 commit 历史以 `git log` 为权威来源，不在本文件维护清单（避免重复与滞后）；某能力由哪个 PR 交付见上方工作单元表的备注。

## 下一步

**beta4 六条完成标志全绿（集群侦察 / Verifier 拿 Query / 定位证据复用 / 反思环节 / exec 配套 / 真集群验证），待关闭并规划下一里程碑。**

候选方向（不预排，定下里程碑时再析出）：
1. 用户侧多轮 / Session（O-1，版本 0.1.0 北极星「可追问」的核心缺口；编排升级，保留扁平模型与 Dispatcher）
2. 配置文件化（版本 0.1.0 想要能力；从 env 扩展到配置文件）
3. 辅助修复（RequireApproval 接线 + 写工具，承接全景-5 的 exec 安全模型）
4. 持久化（`internal/store` 占位；评估体系留后续）

推进方向（beta4 内，已完成）：
1. Verifier 拿 Query + 定位证据复用 ✅
2. 集群侦察（事实层）✅
3. 真集群验证 checkpoint ✅
4. 反思环节（推理层）✅
5. exec 策略（配套）✅

阶段计划与设计推理记录在笔记 `arui-note/aruing/plan/`。

已落地要点：

1. **#4a**：`tools.Policy` + `ReadonlyPolicy` 挂在 `Dispatcher.Execute` 前；`wiring` 在 kubectl 可用时可选注册 `k8s`
2. **#4b**：`Orchestrator.resolveTargets` 循环；`ResolveDriver` / `LLMResolver` / 按节点的 `FakeResolver`；Target ID 与定位阶段 Task/Evidence ID 由编排发放；L-1 关闭
3. **#5**：`LLMPlanner` 单次 `Plan` + `Registry.Specs`；局部 ref 回填 Hypothesis/Task ID；业务重试；不在规划阶段多轮调 Tool（#15–#16）
4. **#6**：`LLMVerifier` 单次 `Verify`；每条 Hypothesis 恰好一条 Verdict；`evidence_ids` 必须属于输入 Evidence；Factory 回填 Verdict ID；业务重试
5. **#7**：`LLMReporter` 单次 `Report`；结论覆盖每条 Verdict 且 `result` 一致；`evidence_ids` ⊆ 对应 Verdict 证据集；Factory 回填 Report ID；业务重试
6. **#8**：`internal/config` 唯一读 `ARUING_*`；`cmd` 不直接 `os.Getenv` 业务键；L-8 最小 `formatRunError`
7. **R-1**：`renderMarkdown` 纯函数渲染；CLI 默认 Markdown，`--format json` 保留
8. **全景-3**：`reconCluster` 经 `executeTask` 跑只读 `kubectl api-resources`（Factory 发 Task ID），发现集群资源类型（含 CRD）注入 Planner 的 `cluster_resources`；侦察 Evidence 进报告链透明可追溯、失败也落 error evidence，但**不进 Verifier 输入**（是 context 不是 verdict 依据）；`reconEnabled` 由 wiring 在 k8s 注册时开启
9. **全景-4**：仅 `planner.md` prompt 强化反思（首轮猜想覆盖不同根因家族；后续轮防确认偏误，考虑替代解释 + 区分性取证）；无结构化阶段、无新输出字段
10. **全景-5**：`DiagnosticPolicy`（exec 放行、不枚举二进制）+ `ARUING_ALLOW_DIAGNOSTIC_EXEC` 开关；wiring 按开关选策略；planner.md 补 Pod 内探针指引；逐次审批留辅助修复阶段

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
- 不按资源类型或子命令**拆工具**（`k8s` 单一工具吃任意 argv，完整 kubectl 能力）；授权层另有 kubectl 子命令读/写白名单（见 `Policy`），做粗粒度读/写区分，与「不拆工具」不冲突
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
