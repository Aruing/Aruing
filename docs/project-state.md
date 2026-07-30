# 项目当前状态

> 最后更新：2026-07-30（`0.1.0-beta5`：beta5-5 PR-B L2 checkpoint）

## 当前阶段

**版本 `0.1.0` / 可追问的诊断助手**（进行中）：版本远景见笔记 `arui-note/aruing/plan/version/0.1.0.md`。

**当前里程碑 `0.1.0-beta5` / 可追问 Session + Tower 智能基线**（**架构已确认 2026-07-28；beta5-1～4 已落地；beta5-5 PR-B 进行中**）：入口 `Session.Turn` → **Tower（默认总控）**；需要根因时 **escalate → 现有 Orchestrator.Execute**；CLI `aruing chat` 已接。诊断是升格专长，不是默认主轴。见笔记 `plan/milestone/0.1.0-beta5.md` 与 `plan/0.1.0-beta5/2026-7-27-session-turn-architecture.md`。

前置：

- `0.1.0-beta4` 诊断信息全景 ✅ 完成并归档（2026-07-28 关闭；交付 2026-07-26，六条完成标志全绿：集群侦察含 CRD、Verifier 拿 Query、定位证据复用、反思环节、exec 策略、真集群验证）
- `0.0.1-beta3` 调查循环 ✅（迭代取证 + pivot + 容错 + 报告调查链）
- `0.0.1-beta2` 真实闭环 ✅（五角色全接 LLM，端到端 Markdown 报告）
- `0.0.1-beta1` 最小假闭环 ✅

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
| 全景-4 | 反思环节（轻量版） | ✅ | 仅 `planner.md` prompt 强化：首轮猜想覆盖不同根因家族；后续轮新增「防确认偏误」规则（考虑替代解释 + 安排区分性取证）。三场景真集群回归：清晰单因场景不膨胀，真实歧义场景更严谨。未做结构化反思阶段 |
| 全景-5 | exec 策略 | ✅ | 新增 `DiagnosticPolicy`（exec 放行、不校验二进制）；`config.Tools.AllowDiagnosticExec` 由 `ARUING_ALLOW_DIAGNOSTIC_EXEC` 控制，默认关；wiring 按开关选策略；planner.md 补 Pod 内探针指引。逐次审批留辅助修复阶段 |
| beta5-1 | Session / Message / Turn | ✅ | PR #36：`internal/session`：Session/Message、`Service.Turn`、Echo/Diagnose；`MemoryStore`；`Run.SessionID`；CLI 未接。见笔记 `plan/0.1.0-beta5/2026-7-28-session-message.md` |
| beta5-2 | 最小 Tower | ✅ | PR #37：`agent.TowerResponder` + `FakeTower`：`GenerateJSON` 决策 reply/escalate；`session.Escalate` 共用升格；prompt `tower.md`。见笔记 `plan/0.1.0-beta5/2026-7-28-tower-minimal.md` |
| beta5-3 | 基线 tool 环 | ✅ | PR #39：`Task.RunID`/`Evidence.RunID` 可空；Dispatcher 放行空 RunID；Tower `call_tool` 轮内环（默认最多 4 次，观察不落 Message）；空 RunID 不得进 Verdict。见笔记 `plan/0.1.0-beta5/2026-7-28-tower-baseline-tool.md` |
| beta5-4 | CLI 接 Turn + Tower | ✅ | PR #40：`aruing chat` → `Session.Turn` + Tower + MemoryStore；`run` 仍直连 Execute；共用 `buildTooling` Dispatcher；无 LLM 硬失败。见笔记 `plan/0.1.0-beta5/2026-7-29-tower-cli.md` |
| beta5-5 PR-A | 解释上下文 + 预算压缩 | ✅ | 去掉 Tower last-N；`prior_diagnostics` + L0/L1 compact（#18）；`formatDiagnosticReply` 加厚；`tower.md` 解释默认 reply。见笔记 `plan/0.1.0-beta5/2026-7-29-tower-explain-prior.md` |
| beta5-5 PR-B | L2 handoff + checkpoint | 🔄 | `prepareTowerContext` L2（`compact.md`）；`ModeCheckpoint`；`RespondOutput.CheckpointContent`；Turn 先写 checkpoint 再写 assistant；Store 全量保留。见笔记 `plan/0.1.0-beta5/` |
| beta5 | Session + Tower | 🔄 | 架构 confirmed；beta5-1～4 落地；beta5-5 解释能力拆 PR-A/B。见笔记 `plan/0.1.0-beta5/` |

替换原则：一次只换一个角色，其他环节继续用假实现，假闭环始终可跑、可测（`make test` 默认无 LLM env，走 fake）。LLM 配置齐全时 wiring 同时启用 LLMParser + LLMResolver + LLMPlanner + LLMVerifier + LLMReporter。

> PR 与 commit 历史以 `git log` 为权威来源，不在本文件维护清单；某能力由哪个 PR 交付见上方工作单元表的备注。

## 下一步

**beta5-5 PR-B（本分支）**：L2 handoff compact + `ModeCheckpoint` 落库。其余：按 run 深解、配置文件化、辅助修复、持久化 / `waiting_user`。

已确认（2026-07-28 + #18）：

1. 入口 `Session.Turn`；**Tower** 每轮必经（智能基线）；诊断 = escalate → 现有 Orchestrator
2. Run = 正式证据账本；非每句必有 Run；调查追问倾向新 Run + SessionContext
3. 扩展能力/工具，禁止 core 意图枚举；助手回答 vs 正式诊断报告可区分
4. **`Task.RunID` 可空**（beta5-3 ✅）：基线 tool 经同一 Dispatcher；空 RunID 观察不得当 Verdict 证据
5. **CLI**（beta5-4 ✅）：`aruing chat` 接 Turn+Tower；`run` 保留；进程内 MemoryStore
6. **#18**：Store 全量；注入模型预算内尽量全给，超预算 L0/L1/L2 compact，禁止 last-N 静默截肢

候选（不预排）：

1. 按 run 深解（Store 拉 Report/证据）
2. 配置文件化
3. 辅助修复（RequireApproval + 写工具）
4. 持久化 / `waiting_user` / 同 Run 续查路径

阶段计划与设计推理记录在笔记 `arui-note/aruing/plan/`。

已落地要点（beta2–5 摘要）：

1. **#4a/#4b**：Policy + 可选 k8s；编排可见定位循环；Target/定位 Task/Evidence ID 由编排发放
2. **#5–#7**：LLMPlanner / LLMVerifier / LLMReporter 单次调用 + Factory 回填 + 业务重试
3. **#8 / R-1**：`internal/config`；CLI 默认 Markdown
4. **beta3**：`investigateLoop` + 工具失败容错 + 报告证据明细
5. **beta4**：Verifier 拿 Query、定位证据复用、集群侦察、反思 prompt、DiagnosticPolicy
6. **beta5-1～4**：Session/Turn；Tower reply/call_tool/escalate；空 RunID 基线观察；`aruing chat`
7. **beta5-5 PR-A**：去掉 last-N；`prior_diagnostics` + L0/L1 预算压缩；诊断摘要加厚；解释默认 reply
8. **beta5-5 PR-B**：L2 handoff（`compact.md`）+ `ModeCheckpoint`；Turn 先写 checkpoint

## 编排与多轮

| 项 | 结论 |
| --- | --- |
| 诊断管道 | 线性 `Orchestrator.Execute` 仍是**诊断升格路径**的实现；#15–#17 不变 |
| 用户侧多轮 | **beta5**：`Session.Turn` + Tower + `aruing chat` 已落地（O-1） |
| 会否推倒 core/tools | **否**；主要加 Session/Tower + 编排入口，复用 Dispatcher 与扁平 Run 链 |
| 单轮期禁止事项 | 仍适用于动编排/工具/角色时对照（见笔记 `plan/archive/0.0.1-beta2/2026-7-22.md` §4） |

公开硬约束见 `architecture.md` #15–#18。

## 当前硬约束摘要

完整清单见 [`docs/architecture.md` 硬约束段](architecture.md#硬约束)。摘要：

- `Run` 不嵌套子实体，扁平 ID 关联
- `Query` 线索必须经 Resolver 真实确认才能成为 `Target`
- 模型输出不能冒充 `Evidence`；`Verdict` 必须引用 `Evidence`
- prompt 从文件加载（`//go:embed`），不写死代码
- 工具接口不限定读写；能力按后端 Tool + Schema 开放，授权由 `Policy`；不按资源类型拆工具（#2/#12/#13）
- **#18**：不得用人为上限阉割正常产品能力；物理预算触顶用压缩 / 剪枝 / 明确失败，禁止固定条数静默丢历史等截肢式「最小闭环」
- 线性 Orchestrator 是单轮临时驱动器 / 诊断升格实现；角色不私自多轮调 Tool；多轮升级保留扁平模型与 Dispatcher（#15–#17）
- 编号与执行：Tool 只经 Dispatcher；各阶段 ID 经 `Factory` 发放

## 预留问题入口

详细表在笔记 `arui-note/aruing/plan/`（含 P/L/C/S/O）。公开侧一句话：

| 编号 | 一句话 |
| --- | --- |
| L-8 | CLI 已有最小 `formatRunError`；更细分类可随配置扩展再补 |
| C-1 | ✅ 已收敛到 `internal/config`（#8） |
| O-1 | ✅ 用户侧多轮 / Session：beta5-1～4 落地；`aruing chat`；beta5-5 PR-A/B 解释上下文 + L2 checkpoint |
| R-1 | ✅ CLI 默认 Markdown，`--format json` 保留 |

更多条目与关闭条件见笔记仓 plan。
