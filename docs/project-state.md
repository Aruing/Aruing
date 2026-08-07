# 项目当前状态

> 最后更新：2026-08-07（**`0.1.0-beta7`** 压缩后按范围回灌 进行中；步骤 1 已实现待 smoke）

## 当前阶段

**版本 `0.1.0` / 可追问的诊断助手**（进行中）：版本远景见笔记 `arui-note/aruing/plan/version/0.1.0.md`。

**`0.1.0-beta7` / 压缩后按范围回灌（PR-C）** ⏳ 进行中：全局 compact 丢细节后按用户问题从 Store 定位区间、回灌原文、必要时只压该窗，注入 `rehydrated_messages`。步骤 1（locate/rehydrate/compactRange + Tower 接线）已实现，单测全绿，待真集群 LLM smoke。plan 在笔记 `plan/0.1.0-beta7/`。

**`0.1.0-beta6` / 按 run 深解** ✅ 完成并归档（2026-08-06 关闭）：进程内 `RunLedger` 落账；Tower `prior_run_details` 注入结论+证据（#18 raw 预算）；解释默认 reply；wiring smoke 通过。plan 在笔记 `plan/archive/0.1.0-beta6/`。

**`0.1.0-beta5-fix-2` / 基线浅查与环境可见性** ✅ 完成并归档（2026-08-01 关闭 · **修复型**）：证据纪律 + 基线 `cluster_resources` recon + 默认 12 轮 tool / 触顶自动 escalate（#50/#52/#53）。plan 在笔记 `plan/archive/0.1.0-beta5-fix-2/`。

**`0.1.0-beta5-fix-1` / 基线观察回喂** ✅ 完成并归档（2026-07-30 关闭 · **修复型**）：Tower/Resolver 注入 `Evidence.Raw` + 共享预算 / `rawTruncated`（#18）；#45–#48。plan 在笔记 `plan/archive/0.1.0-beta5-fix-1/`。T-obs-3 Summary 人读仍为候选。

**`0.1.0-beta5` / Session + Tower 智能基线** ✅ 完成并归档（2026-07-30 关闭）：入口 `Session.Turn` → **Tower**；escalate → Orchestrator；`aruing chat`；L0/L1/L2 + `ModeCheckpoint`（#18）。plan 在笔记 `plan/archive/0.1.0-beta5/`。

前置：`0.1.0-beta4` / `0.0.1-beta3` / `0.0.1-beta2` / `0.0.1-beta1` 均已关闭。

## 工作单元

| # | 模块 | 状态 | 备注 |
| - | --- | --- | --- |
| 1 | LLM 客户端 | ✅ | PR #3 `internal/llm` |
| 3 | Parser | ✅ | PR #5 / #6 |
| 2 | Kubernetes 工具 | ✅ | 单一 shell-less `k8s` Tool |
| 4 | Resolver | ✅ #4a+#4b | 编排可见定位循环 |
| 5–7 | Planner / Verifier / Reporter | ✅ | 单次 LLM 调用 |
| 8 | 配置层 | ✅ | `internal/config` |
| R-1 | CLI Markdown 渲染 | ✅ | PR #19 |
| 多轮-1～4b | 调查循环 | ✅ | beta3 |
| 全景-1～5 | 诊断信息全景 | ✅ | beta4 |
| beta5 | Session + Tower | ✅ | 2026-07-30 关闭；#36–#40/#42/#43 |
| beta5-fix-1 | 基线观察回喂 | ✅ | #45–#48 |
| beta5-fix-2 | 基线浅查与环境可见性 | ✅ | #50/#52/#53 |
| beta5-5 PR-C | locate + rehydrate | 候选 | 消息窗回灌；与深解互补 |
| beta5-fix-1 T-obs-3 | k8s Summary 人读 | 候选 | 可选 |
| beta6-1 | Run 账本 + escalate 落账 | ✅ | #55；`RunLedger` + `MemoryRunLedger` |
| beta6-2 | Tower 方案 A 注入加深 | ✅ | `prior_run_details`；证据 raw 共享预算；解释默认 reply |
| beta6-3 | wiring smoke + 文档收尾 | ✅ | 空账本单测；chat 诊断→续聊依据 smoke；关里程碑 |
| beta7-1 | locate + rehydrate + compactRange + Tower 接线 | ⏳ | 已实现单测绿；规则优先+LLM 兜底；待 smoke |
| beta7-2 | smoke + 文档收尾 | 未开始 | 真 LLM+k8s：长聊压缩后追问能回灌命中 |

替换原则：一次只换一个角色，其他环节继续用假实现，假闭环始终可跑、可测（`make test` 默认无 LLM env，走 fake）。LLM 配置齐全时 wiring 同时启用 LLM 角色链。

> PR 与 commit 历史以 `git log` 为权威来源，不在本文件维护清单。

## 下一步

**下一项**：beta7 步骤 2——真集群 LLM smoke（长聊压缩后追问「之前某步为什么」能回灌命中原文要点、不 re-escalate），通过后关里程碑。

**0.1.0 候选**（beta7 关闭后仍有效）：

1. **PR-C** rehydrate（与深解互补，刻意留下一刀）
2. T-obs-3 / 配置文件化 / 磁盘持久化 / `waiting_user`

已确认（beta5–6 交付后仍有效）：

1. 入口 `Session.Turn`；**Tower** 每轮必经；诊断 = escalate → Orchestrator
2. Run = 正式证据链；**进程内 `RunLedger` 为 Report/Evidence 读回权威源**（非 Message 字符串）
3. 扩展能力/工具，禁止 core 意图枚举
4. **`Task.RunID` 可空**：基线 tool 经同一 Dispatcher；空 RunID 不得当 Verdict 证据、不进 `RunLedger`
5. **CLI**：`aruing chat` + `run`；进程内 MemoryStore + MemoryRunLedger
6. **#18**：Store / Ledger 进程内可全量；注入触顶用压缩，禁止 last-N / 只留最近 N 次诊断当能力墙
7. **深解**：`prior_run_details` 结构化结论+证据；解释既有诊断默认 reply、不 re-escalate

阶段计划与设计推理记录在笔记 `arui-note/aruing/plan/`（活跃）与 `plan/archive/`（已关）。

已落地要点（beta2–6 摘要）：

1. **#4a/#4b～#8 / R-1**：Policy + 可选 k8s；LLM 角色链；config；Markdown CLI
2. **beta3**：`investigateLoop` + 工具失败容错 + 报告证据明细
3. **beta4**：Verifier 拿 Query、定位证据复用、集群侦察、反思 prompt、DiagnosticPolicy
4. **beta5**：Session/Turn；Tower reply/call_tool/escalate；`aruing chat`；prior + L0–L2 + checkpoint
5. **beta5-fix-1**：基线/定位观察注入 Raw + 共享预算 #18（#45–#47）
6. **beta5-fix-2**：基线 recon + 证据纪律 + 默认 12 轮/触顶 escalate（#50/#52/#53）
7. **beta6**：`RunLedger` 落账（#55）+ Tower `prior_run_details` 深解 + smoke/文档收尾

## 编排与多轮

| 项 | 结论 |
| --- | --- |
| 诊断管道 | 线性 `Orchestrator.Execute` 仍是**诊断升格路径**的实现；#15–#17 不变 |
| 用户侧多轮 | **已落地**：`Session.Turn` + Tower + `aruing chat`（O-1） |
| 正式诊断读回 | **已落地（beta6）**：`RunLedger` 进程内 Put/Get/ListBySession；不固化 `Execute→Report` 为对外唯一契约 |
| 深解注入 | **已落地（beta6）**：`prior_run_details` 结构化结论+证据；Message 侧 `prior_diagnostics` 保留 |
| 会否推倒 core/tools | **否**；Session/Tower + 编排入口，复用 Dispatcher 与扁平 Run 链 |
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
| O-1 | ✅ 用户侧多轮 / Session：beta5 已关；深解 beta6 已关；`aruing chat`；L0–L2 + checkpoint；PR-C rehydrate 进行中（beta7，步骤 1 已实现待 smoke） |
| R-1 | ✅ CLI 默认 Markdown，`--format json` 保留 |

更多条目与关闭条件见笔记仓 plan。
