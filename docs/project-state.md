# 项目当前状态

> 最后更新：2026-07-30（**`0.1.0-beta5-fix-1`** 已关闭并归档）

## 当前阶段

**版本 `0.1.0` / 可追问的诊断助手**（进行中）：版本远景见笔记 `arui-note/aruing/plan/version/0.1.0.md`。

**`0.1.0-beta5-fix-1` / 基线观察回喂** ✅ 完成并归档（2026-07-30 关闭 · **修复型**）：Tower/Resolver 注入 `Evidence.Raw` + 共享预算 / `rawTruncated`（#18）；#45–#48；smoke 通过。plan 在笔记 `plan/archive/0.1.0-beta5-fix-1/`。T-obs-3 Summary 人读仍为候选。

**`0.1.0-beta5` / Session + Tower 智能基线** ✅ 完成并归档（2026-07-30 关闭）：入口 `Session.Turn` → **Tower（默认总控）**；需要根因时 **escalate → Orchestrator.Execute**；CLI `aruing chat`；L0/L1/L2 compact + `ModeCheckpoint`（#18）。plan 在笔记 `plan/archive/0.1.0-beta5/`。

前置：

- `0.1.0-beta4` 诊断信息全景 ✅ 完成并归档（2026-07-28 关闭）
- `0.0.1-beta3` 调查循环 ✅
- `0.0.1-beta2` 真实闭环 ✅
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
| 4 | Resolver | ✅ #4a+#4b | 方案 A：编排可见定位循环 + `ResolveDriver`；`LLMResolver`；`FakeResolver`；Policy + 可选 k8s 注册 |
| 5 | Planner | ✅ | `LLMPlanner`：单次 `Plan` + `Registry.Specs`；业务重试 |
| 6 | Verifier | ✅ | `LLMVerifier`：单次 `Verify`；只引用已登记 Evidence |
| 7 | Reporter | ✅ | `LLMReporter`：单次 `Report`；结论对齐 Verdict |
| 8 | 配置层 | ✅ | `internal/config`：`Load`/`LoadFrom` 收敛 env；CLI `formatRunError` 最小 L-8 |
| R-1 | CLI Markdown 渲染 | ✅ | PR #19 `renderMarkdown`；默认 Markdown，`--format json` 保留 |
| 多轮-1～4b | 调查循环 | ✅ | `PlanState` + `investigateLoop` + 容错 + 证据明细 |
| 全景-1～5 | 诊断信息全景 | ✅ | Query/复用/侦察/反思 prompt/DiagnosticPolicy（beta4） |
| beta5-1 | Session / Message / Turn | ✅ | PR #36：`internal/session`；`MemoryStore`；`Run.SessionID` |
| beta5-2 | 最小 Tower | ✅ | PR #37：`TowerResponder`；reply/escalate；`tower.md` |
| beta5-3 | 基线 tool 环 | ✅ | PR #39：空 RunID + `call_tool` 轮内环 |
| beta5-4 | CLI 接 Turn + Tower | ✅ | PR #40：`aruing chat`；`run` 仍直连 Execute |
| beta5-5 PR-A | 解释上下文 + 预算压缩 | ✅ | PR #42：prior + L0/L1；去掉 last-N |
| beta5-5 PR-B | L2 handoff + checkpoint | ✅ | PR #43：`compact.md` + `ModeCheckpoint` |
| beta5-5 PR-C | locate + rehydrate | 候选 | 压缩后 Store 范围回灌；关 beta5 时未做 |
| beta5 | Session + Tower | ✅ | 六条完成标志全绿；2026-07-30 关闭 |
| beta5-fix-1 T-obs-1 | 基线观察回喂 Raw | ✅ #45 | 内存全量 `Evidence.Raw`；注入按预算截断并 `rawTruncated`（#18） |
| beta5-fix-1 T-obs-2 | 多观察预算治理 | ✅ #46 | 全部 raw 共享预算、优先保新 |
| beta5-fix-1 T-obs-4 | Resolver raw 预算 | ✅ #47 | 去掉固定 2000 预览，共享预算优先保新（#18） |
| beta5-fix-1 | 基线观察回喂（fix） | ✅ | 五条完成标志全绿；2026-07-30 关闭；#45–#48 |
| beta5-fix-1 T-obs-3 | k8s Summary 人读 | 候选 | 可选；关后仍可做 |

替换原则：一次只换一个角色，其他环节继续用假实现，假闭环始终可跑、可测（`make test` 默认无 LLM env，走 fake）。LLM 配置齐全时 wiring 同时启用 LLM 角色链。

> PR 与 commit 历史以 `git log` 为权威来源，不在本文件维护清单。

## 下一步

**下一项**：尚未立新里程碑；从 0.1.0 北极星与下列候选现场析出（优先关注「基线浅查 vs escalate」若要做域名/访问类根因）。

**功能向候选（不预排）**：

1. **基线浅查误判 / escalate 策略**（已知洞 · 2026-07-30）：域名访问类问题常停在 `call_tool`+`reply`，只查标准 Ingress 空列表即下结论，不 escalate、不发现 Traefik IngressRoute 等 CRD；与 Raw 回喂无关。细节见笔记 `plan/archive/0.1.0-beta5-fix-1/`「关后已知洞」与历程《基线观察回喂与预算对齐》
2. **PR-C** locate + rehydrate（压缩后保真解释旧步）
3. T-obs-3：k8s Summary 人读增强
4. 按 run 深解（Store / 领域侧拉 Report、证据）
5. 配置文件化 / 辅助修复 / 持久化 / `waiting_user`

已确认（beta5 交付后仍有效）：

1. 入口 `Session.Turn`；**Tower** 每轮必经；诊断 = escalate → Orchestrator
2. Run = 正式证据账本；非每句必有 Run
3. 扩展能力/工具，禁止 core 意图枚举；助手回答 vs 正式诊断报告可区分
4. **`Task.RunID` 可空**：基线 tool 经同一 Dispatcher；空 RunID 不得当 Verdict 证据
5. **CLI**：`aruing chat` + `run`；进程内 MemoryStore
6. **#18**：Store 全量；L0/L1/L2 compact + checkpoint；禁止 last-N 静默截肢

阶段计划与设计推理记录在笔记 `arui-note/aruing/plan/`（活跃）与 `plan/archive/`（已关）。

已落地要点（beta2–5 摘要）：

1. **#4a/#4b～#8 / R-1**：Policy + 可选 k8s；LLM 角色链；config；Markdown CLI
2. **beta3**：`investigateLoop` + 工具失败容错 + 报告证据明细
3. **beta4**：Verifier 拿 Query、定位证据复用、集群侦察、反思 prompt、DiagnosticPolicy
4. **beta5**：Session/Turn；Tower reply/call_tool/escalate；`aruing chat`；prior + L0–L2 + checkpoint
5. **beta5-fix-1**：基线/定位观察注入 Raw + 共享预算 #18（#45–#47）；不修 escalate 策略

## 编排与多轮

| 项 | 结论 |
| --- | --- |
| 诊断管道 | 线性 `Orchestrator.Execute` 仍是**诊断升格路径**的实现；#15–#17 不变 |
| 用户侧多轮 | **已落地**：`Session.Turn` + Tower + `aruing chat`（O-1） |
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
| O-1 | ✅ 用户侧多轮 / Session：beta5 已关；`aruing chat`；L0–L2 + checkpoint；PR-C rehydrate 候选 |
| R-1 | ✅ CLI 默认 Markdown，`--format json` 保留 |

更多条目与关闭条件见笔记仓 plan。
