# 项目当前状态

> 最后更新：2026-08-10（**文档对齐代码**：`0.1.0-beta8` 配置文件化已由 #61/#62 落地；候选与 architecture 同步）

## 当前阶段

**版本 `0.1.0` / 可追问的诊断助手**（进行中）：版本远景见笔记 `arui-note/aruing/plan/version/0.1.0.md`。

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

产品路径（`run`/`chat`）须 LLM 齐全；单元测试用 `agenttest`/`toolstest` 假实现，不依赖 CLI 假闭环。

> PR 与 commit 历史以 `git log` 为权威来源，不在本文件维护清单。

## 下一步

**下一项**：从 `0.1.0` 候选里选下一里程碑（beta8 已归档）。

**0.1.0 候选**（仍有效）：

1. **`waiting_user`**（澄清/挂起）
2. **T-obs-3**（k8s Summary 人读，可选 polish）
3. **磁盘持久化**（与 0.1.0 远景「仍内存」冲突，宜 0.2+ 或单独重开远景）

**已完成、勿再当候选**：配置文件化（beta8 / #61/#62）。

已确认（beta5–8 交付后仍有效）：

1. 入口 `Session.Turn`；**Tower** 每轮必经；诊断 = escalate → Orchestrator
2. Run = 正式证据链；**进程内 `RunLedger` 为 Report/Evidence 读回权威源**
3. 扩展能力/工具，禁止 core 意图枚举
4. **`Task.RunID` 可空**：基线 tool 经同一 Dispatcher；空 RunID 不得当 Verdict 证据
5. **CLI**：`aruing chat` + `run`；均须 LLM；进程内 MemoryStore + MemoryRunLedger
6. **配置**：可选 YAML + env 覆盖 + `--config`；`config.Config` 为 wiring 唯一入口
7. **#18**：Store / Ledger 进程内可全量；注入触顶用压缩，禁止 last-N
8. **深解 / 回灌**：`prior_run_details`；`rehydrated_messages`

阶段计划与设计推理记录在笔记 `arui-note/aruing/plan/`（活跃）与 `plan/archive/`（已关）。

已落地要点（beta2–8 摘要）：

1. **#4a/#4b～#8 / R-1**：Policy + 可选 k8s；LLM 角色链；config env；Markdown CLI
2. **beta3**：`investigateLoop` + 工具失败容错 + 报告证据明细
3. **beta4**：Verifier 拿 Query、定位证据复用、集群侦察、反思 prompt、DiagnosticPolicy
4. **beta5**：Session/Turn；Tower reply/call_tool/escalate；`aruing chat`；prior + L0–L2 + checkpoint
5. **beta5-fix-1/2**：观察 Raw 预算；基线 recon + 证据纪律 + 触顶 escalate
6. **beta6**：`RunLedger` + `prior_run_details` 深解
7. **beta6-fix-1**：空响应/JSON 可恢复重试（#58）
8. **beta7**：压缩后按范围回灌（#59）
9. **beta8**：YAML 配置文件 + 路径链 + env 覆盖；CLI 去假闭环（#61/#62）

## 编排与多轮

| 项 | 结论 |
| --- | --- |
| 诊断管道 | 线性 `Orchestrator.Execute` 仍是**诊断升格路径**的实现；#15–#17 不变 |
| 用户侧多轮 | **已落地**：`Session.Turn` + Tower + `aruing chat`（O-1） |
| 正式诊断读回 | **已落地（beta6）**：`RunLedger` 进程内 |
| 深解 / 回灌 | **已落地（beta6/7）** |
| 配置文件 | **已落地（beta8）**：文件 → env → CLI |
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
- 线性 Orchestrator 是单轮临时驱动器 / 诊断升格实现；角色不私自多轮调 Tool（#15–#17）
- 编号与执行：Tool 只经 Dispatcher；各阶段 ID 经 `Factory` 发放

## 预留问题入口

详细表在笔记 `arui-note/aruing/plan/`（含 P/L/C/S/O）。公开侧一句话：

| 编号 | 一句话 |
| --- | --- |
| L-8 | CLI 已有最小 `formatRunError`；更细分类可随配置扩展再补 |
| C-1 | ✅ env 收敛到 `internal/config`；**文件化 ✅ beta8** |
| O-1 | ✅ 用户侧多轮 / 深解 / 回灌已关；`waiting_user` 仍开放 |
| R-1 | ✅ CLI 默认 Markdown，`--format json` 保留 |

更多条目与关闭条件见笔记仓 plan。
