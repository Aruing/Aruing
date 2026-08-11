# 项目当前状态

> 最后更新：2026-08-11（**0.1.0-beta11 进行中** step 1 已交付：结构化工具输出 L0–L2 落地硬约束 #19；新增 `internal/tools/k8s/summary.go` 表格投影 + narrow-first Spec，core/agent 零改动。step 2 = beta10 harness LLM smoke + 关里程碑。前序：**0.1.0-beta10 已归档** #64：kind 可复现场景 harness；新增跨版本技术全景层 arc + 硬约束 #19；首份 arc《工具输出导航》，T-obs-3 升格为其 Step 1-2 = beta11）

## 当前阶段

**版本 `0.1.0` / 可追问的诊断助手**（进行中）：版本远景见笔记 `arui-note/aruing/plan/version/0.1.0.md`。

**`0.1.0-beta11` / 结构化工具输出（L0–L2）** 🔨 进行中（2026-08-11 起；step 1 已交付）：arc《工具输出导航》Step 1-2。k8s 工具对 `get` 类表格（默认文本表 + `-o json` `Table`）产出紧凑 `Summary`（类型/条数/列/行），大表标注头尾 + 引导 narrow（#18）；`Raw` 不可变原带不变（#19）；`ToolSpec.Description` 改 narrow-first（删「优先 -o json」，教 `--field-selector`/label/`-o jsonpath`）。只动 `internal/tools/k8s`，`core`/`agent` 零改动。step 2 = beta10 harness LLM smoke 验收 + 关里程碑归档 plan。plan 在笔记 `plan/0.1.0-beta11/`；arc 在 `plan/arc/tool-output-navigation.md`。

**`0.1.0-beta10` / 可复现场景 harness（kind）** ✅ 完成并归档（2026-08-11；#64）：`scenarios/` + `scripts/` + `lab-*` Make 目标（up/down/list/chat/kube）+ 三场景（crashloop-bad-image / svc-wrong-selector / same-name-multi-ns）；验收**以 `chat` 为主**（`run` 不单独验收）；LLM chat smoke 通过（crashloop / same-name-multi-ns）。不进 `make test`。plan 在笔记 `plan/archive/0.1.0-beta10/`；历程《可复现的故障台架》。

**`0.1.0-beta9` / 澄清挂起（waiting_user）** ✅ 完成并归档（2026-08-10；#63）：`core.Suspension`/`Outcome` 通用抽象；`ResolveActionClarify` + `Clarifications`；`Orchestrator.Execute` 返 `Outcome` + `Resume`/`FindSuspended`；`session.SuspendedRunner`（可选）+ `session.Resume` + `ModeClarify`；Tower 入口挂起恢复优先；CLI `run` 遇挂起打印问题并退出。plan 在笔记 `plan/archive/0.1.0-beta9/`；历程《挂起与恢复的第一锤》。#15–#17 兑现的第一锤。

**`0.1.0-beta8` / 配置文件化** ✅ 完成并归档（2026-08-10；#61 YAML + env 覆盖 + 路径链 + CLI 须 LLM；#62 config banner / playground ignore）。示例：`aruing.example.yaml`；`--config` / `ARUING_CONFIG` / 默认搜索路径。plan 在笔记 `plan/archive/0.1.0-beta8/`。

**`0.1.0-beta7` / 压缩后按范围回灌（PR-C）** ✅ 完成并归档（2026-08-08）：`locateRange` → `rehydrateRange` → `compactRange`；`rehydrated_messages`（#59）；LLM smoke 路径 B。plan 在 `plan/archive/0.1.0-beta7/`。

**`0.1.0-beta6-fix-1` / 网关空响应 · Tower 可恢复重试** ✅ 完成并归档（2026-08-07 · **修复型**）：#58。plan 在 `plan/archive/0.1.0-beta6-fix-1/`。

**`0.1.0-beta6` / 按 run 深解** ✅ 完成并归档（2026-08-06）：`RunLedger` + `prior_run_details`。plan 在 `plan/archive/0.1.0-beta6/`。

**`0.1.0-beta5-fix-2` / 基线浅查与环境可见性** ✅ 完成并归档（2026-08-01 · **修复型**）：#50/#52/#53。

**`0.1.0-beta5-fix-1` / 基线观察回喂** ✅ 完成并归档（2026-07-30 · **修复型**）：#45–#48。T-obs-3 Summary 人读仍为候选。

**`0.1.0-beta5` / Session + Tower 智能基线** ✅ 完成并归档（2026-07-30）：`Session.Turn` + Tower；`aruing chat`；L0/L1/L2。

前置：`0.1.0-beta4` / `0.0.1-beta3` / `0.0.1-beta2` / `0.0.1-beta1` 均已关闭。

## 工作单元

| # | 模块 | 状态 | 备注 |
| - | --- | --- | --- |
| 1 | LLM 客户端 | ✅ | PR #3 `internal/llm` |
| 3 | Parser | ✅ | PR #5 / #6 |
| 2 | Kubernetes 工具 | ✅ | 单一 shell-less `k8s` Tool |
| 4 | Resolver | ✅ #4a+#4b | 编排可见定位循环 |
| 5–7 | Planner / Verifier / Reporter | ✅ | 单次 LLM 调用 |
| 8 | 配置层（env） | ✅ | `internal/config` 初版 |
| R-1 | CLI Markdown 渲染 | ✅ | PR #19 |
| 多轮-1～4b | 调查循环 | ✅ | beta3 |
| 全景-1～5 | 诊断信息全景 | ✅ | beta4 |
| beta5 | Session + Tower | ✅ | #36–#40/#42/#43 |
| beta5-fix-1 | 基线观察回喂 | ✅ | #45–#48 |
| beta5-fix-2 | 基线浅查与环境可见性 | ✅ | #50/#52/#53 |
| beta5-5 PR-C | locate + rehydrate | ✅ → beta7 | #59 |
| beta5-fix-1 T-obs-3 | k8s Summary 人读 | 候选 | 可选 |
| beta6-1～3 | Run 账本 + 深解 + smoke | ✅ | #55–#57 |
| beta6-fix-1 | 网关空响应 / Tower 可恢复重试 | ✅ | #58 |
| beta7-1～2 | 压缩后按范围回灌 + smoke | ✅ | #59–#60 |
| beta8 | 配置文件化 | ✅ | #61 YAML/路径链/merge；#62 banner；Fake* → agenttest/toolstest；CLI 无假闭环 |
| beta9 | 澄清挂起（waiting_user） | ✅ | #63 `Suspension`/`Outcome`/`RunStatusWaitingUser`；`ResolveActionClarify`；`Execute`→`Outcome` + `Resume`/`FindSuspended`；`SuspendedRunner` + `session.Resume` + `ModeClarify`；Tower 入口优先 Resume；CLI run 遇挂起退出 |
| beta10 | 可复现场景 harness（kind） | ✅ | #64 `scenarios/` + `scripts/` + `lab-*`（up/down/list/chat/kube）+ 三场景；验收以 `chat` 为主；LLM smoke 通过；不进 `make test` |
| beta11 | 结构化工具输出（L0–L2） | 🔨 | step 1 交付：`summary.go` 表格投影（文本表 + JSON Table + fallback + 大表标注）+ narrow-first `Spec`；step 2 = smoke + 关里程碑待跑 |

  产品路径（`run`/`chat`）须 LLM 齐全；单元测试用 `agenttest`/`toolstest` 假实现，不依赖 CLI 假闭环。

> PR 与 commit 历史以 `git log` 为权威来源，不在本文件维护清单。

## 下一步

**下一项**：`0.1.0-beta11` 收尾——step 1 已交付并 commit（`feat/beta11-structured-tool-output` `0b443e6`，`make check` 全绿）；beta10 harness（`crashloop-bad-image`）真 LLM smoke 通过（`kubectl get pods` 的 Summary 实为结构化投影，`describe pod` 走 fallback，2 轮工具出报告对照 `expect.md` 满足）。剩：开 PR 合并 → 关里程碑归档 `plan/0.1.0-beta11/`，然后从候选选下一项或评估开 `0.2.0`。

**0.1.0 候选**（本里程碑之后）：

1. **CLI banner 加 kubectl/kubeconfig 路径**（小 polish；beta10 smoke 期间提出：在 `writeConfigBanner` 加打印 `cfg.Tools.KubectlPath`（空则 LookPath）与 `$KUBECONFIG`（空则 `~/.kube/config`），改 `cmd/aruing` + `main_test.go`，独立小 PR）
2. **chat 交互循环容错**（`cmd/aruing/main.go:269-271` 单轮 `chatTurn` 报错即 `return err` 退出整个会话；应继续下一轮而非杀会话。beta10 smoke 发现，暂不修）
3. **结构化工具输出**（= T-obs-3 升格；arc《工具输出导航》Step 1-2） 🔨 **进行中（beta11 step 1 已交付）**：k8s 工具产机读 `Summary` + narrow-first Spec；step 2 smoke 待跑 → `plan/arc/tool-output-navigation.md`
4. **磁盘持久化**（与 0.1.0 远景「仍内存」冲突，宜 0.2+ 或单独重开远景）
5. 后续阶段挂起（investigate / parse 等复用 `Suspension`，按 `Stage` 派发）

**arc《工具输出导航》延后步骤**（跨 0.1.0 → 0.2+，详见 `plan/arc/tool-output-navigation.md`）：

- Step 3 日志/事件游标翻页（0.1.0-beta12+，需日志扫描场景驱动）
- Step 4 全覆盖 map-reduce 扫描（0.2+）
- Step 5 子 agent 分治（0.2+，动编排 #15/#17）

**已完成、勿再当候选**：配置文件化（beta8）、`waiting_user` / 澄清挂起（beta9）、场景 harness（beta10）。

已确认（beta5–9 交付后仍有效）：

1. 入口 `Session.Turn`；**Tower** 每轮必经；诊断 = escalate → Orchestrator
2. Run = 正式证据链；**进程内 `RunLedger` 为 Report/Evidence 读回权威源**
3. 扩展能力/工具，禁止 core 意图枚举
4. **`Task.RunID` 可空**：基线 tool 经同一 Dispatcher；空 RunID 不得当 Verdict 证据
5. **CLI**：`aruing chat` + `run`；均须 LLM；进程内 MemoryStore + MemoryRunLedger
6. **配置**：可选 YAML + env 覆盖 + `--config`；`config.Config` 为 wiring 唯一入口
7. **#18**：Store / Ledger 进程内可全量；注入触顶用压缩，禁止 last-N
8. **深解 / 回灌**：`prior_run_details`；`rehydrated_messages`
9. **挂起 / 恢复**：`Suspension`（通用，`Stage` 留位）；`Outcome` 三态；`Resume` 重跑（非 checkpoint）；挂起态进程内、退出即丢；澄清次数不限（#18），触顶明确失败

阶段计划与设计推理记录在笔记 `arui-note/aruing/plan/`（活跃）与 `plan/archive/`（已关）。

已落地要点（beta2–9 摘要）：

1. **#4a/#4b～#8 / R-1**：Policy + 可选 k8s；LLM 角色链；config env；Markdown CLI
2. **beta3**：`investigateLoop` + 工具失败容错 + 报告证据明细
3. **beta4**：Verifier 拿 Query、定位证据复用、集群侦察、反思 prompt、DiagnosticPolicy
4. **beta5**：Session/Turn；Tower reply/call_tool/escalate；`aruing chat`；prior + L0–L2 + checkpoint
5. **beta5-fix-1/2**：观察 Raw 预算；基线 recon + 证据纪律 + 触顶 escalate
6. **beta6**：`RunLedger` + `prior_run_details` 深解
7. **beta6-fix-1**：空响应/JSON 可恢复重试（#58）
8. **beta7**：压缩后按范围回灌（#59）
9. **beta8**：YAML 配置文件 + 路径链 + env 覆盖；CLI 去假闭环（#61/#62）
10. **beta9**：`Suspension`/`Outcome` 通用挂起抽象；resolve clarify 端到端；`Execute`→`Outcome` + `Resume`/`FindSuspended`；`SuspendedRunner` + `session.Resume` + `ModeClarify`；Tower 入口挂起恢复优先

## 编排与多轮

| 项 | 结论 |
| --- | --- |
| 诊断管道 | `Orchestrator.Execute` 返 `Outcome`（`Report` 或 `Suspension`）；`Resume` 恢复挂起；#15–#17 不变 |
| 用户侧多轮 | **已落地**：`Session.Turn` + Tower + `aruing chat`（O-1）；挂起时 Tower 入口优先 Resume |
| 正式诊断读回 | **已落地（beta6）**：`RunLedger` 进程内 |
| 深解 / 回灌 | **已落地（beta6/7）** |
| 配置文件 | **已落地（beta8）**：文件 → env → CLI |
| 澄清挂起 | **已落地（beta9）**：`Suspension`/`Outcome` + resolve clarify + `Resume`；进程内、退出即丢 |
| 会否推倒 core/tools | **否** |
| 单轮期禁止事项 | 动编排/工具/角色时仍对照笔记 `plan/archive/0.0.1-beta2/2026-7-22.md` §4 |

公开硬约束见 `architecture.md` #15–#18。

## 当前硬约束摘要

完整清单见 [`docs/architecture.md` 硬约束段](architecture.md#硬约束)。摘要：

- `Run` 不嵌套子实体，扁平 ID 关联
- `Query` 线索必须经 Resolver 真实确认才能成为 `Target`
- 模型输出不能冒充 `Evidence`；`Verdict` 必须引用 `Evidence`
- prompt 从文件加载（`//go:embed`），不写死代码
- 工具接口不限定读写；能力按后端 Tool + Schema 开放，授权由 `Policy`
- **#18**：不得用人为上限阉割正常产品能力；触顶用压缩 / 明确失败
- **#19**：工具输出为可导航双结构（`Summary` 投影 + `Raw` 原带）；工具只做机械格式投影不做业务判断；模型经 Summary 看全貌按需 drill，不被逼猜（→ arc《工具输出导航》）
- 线性 Orchestrator 是单轮临时驱动器 / 诊断升格实现；角色不私自多轮调 Tool（#15–#17）
- 编号与执行：Tool 只经 Dispatcher；各阶段 ID 经 `Factory` 发放

## 预留问题入口

详细表在笔记 `arui-note/aruing/plan/`（含 P/L/C/S/O）。公开侧一句话：

| 编号 | 一句话 |
| --- | --- |
| L-8 | CLI 已有最小 `formatRunError`；更细分类可随配置扩展再补 |
| C-1 | ✅ env 收敛到 `internal/config`；**文件化 ✅ beta8** |
| O-1 | ✅ 用户侧多轮 / 深解 / 回灌 / 澄清挂起已关（beta9） |
| R-1 | ✅ CLI 默认 Markdown，`--format json` 保留 |

更多条目与关闭条件见笔记仓 plan。
