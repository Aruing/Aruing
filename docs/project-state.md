# 项目当前状态

> 最后更新：2026-08-14（**`0.1.0-beta15`**：Step 1 #73 + Step 2 #74 已 merged，Step 3（`--ui` 模式切换）实现中——beta15 收官步。前序：beta14 已归档）

## 当前阶段

**版本 `0.1.0` / 可追问的诊断助手**（进行中）：版本远景见笔记 `arui-note/aruing/plan/version/0.1.0.md`。

**`0.1.0-beta15` / 行内 TUI（pi 留痕风格）+ 双模式** ⏳ 进行中：Step 1 #73 行内引擎（自写 readline 多行重绘错位 → 改 **ergochat/readline**；shift+enter 软换行经 modifyOtherKeys + 序列翻译，`\` 续行兑底；非 tty 明确报错；toolchain 钉 go1.26.6 清标准库 CVE）；Step 2 #74 轮间 divider + glamour markdown + 每轮重取终端宽；Step 3：`config.TUI.Mode` + `--ui` 选模式，默认 inline，app（bubbletea）接选。

**`0.1.0-beta14` / 交互式 TUI + 启动 banner** ✅ 完成并归档（2026-08-13；#70 banner + #71 TUI 骨架+容错 + #72 glamour markdown+主题；新硬约束 #20；历程《交互式终端与可定制主题基础》《为流式留位》）。plan 在 `plan/archive/0.1.0-beta14/`。

**`0.1.0-beta13` / 工具输出 L3 腿 B（普适证据导航）** ✅ 完成并归档（2026-08-13；#67+#68）：arc《工具输出导航》Step 3b 主段。#67 纯重构：表格导航算法（PCA/覆盖/频次）抽到 `internal/tools/summary`，k8s 只留格式解析。#68：可选 `tools.Slicer` + `summary.SliceRows`；注册工具 `evidence.read`（经 Dispatcher/Policy，#17）；基线成功观察由 Tower 经 Factory 发 `evidenceId` 写入轮内 `ObservationIndex`，`Respond` 结束 `Discard`；k8s 实现 Slicer（文本表 / JSON Table）；`MaxStdoutBytes` 可配置；不可切片（describe/logs）返错误引导 re-query。导航结果不 Put 回索引。`core.Evidence` 零改。plan 在笔记 `plan/archive/0.1.0-beta13/`；arc 在 `plan/arc/tool-output-navigation.md`。未做：日志时间游标、超巨页式存储（0.2+）、formal RunLedger drill。

**`0.1.0-beta12` / 工具输出 L3 腿 A（大表中段可读）** ✅ 完成并归档（2026-08-12；#66）：arc《工具输出导航》Step 3a。大表（> 64 行）`Summary` 从「头 8 + 尾 4」升级为**三段式（列频次 + PCA 异常段 + 取值覆盖段）**：`significantColumns` 过滤低基数非清一色列 → `encodeOneHot` → Gonum `stat.PC` 算 PCA → 每行按 Hotelling's T² 排序选前 8 异常行（带 `← T²=X.XX` 标注）；覆盖段保证每个非主流取值至少 1 行代表 + 中段均匀步长补全；头/尾各 4 固定。算法依据：Pearson 1901 / Hotelling 1933 PCA、Mahalanobis 1936、Hotelling 1947 T²、Benzécri 1973 MCA 简化形态。引入 `gonum.org/v1/gonum v0.17.0`。兜底：`N<30` / 方差为 0 / 协方差奇异 → 异常段为空、全靠覆盖段。只动 `internal/tools/k8s`，`core`/`agent` 零改；`Raw` 不变。plan 在笔记 `plan/archive/0.1.0-beta12/`；arc 在 `plan/arc/tool-output-navigation.md`；历程《工具输出大表中段可读》。

**`0.1.0-beta11` / 结构化工具输出（L0–L2）** ✅ 完成并归档（2026-08-11；#65）：arc《工具输出导航》Step 1-2。k8s 工具对 `get` 类表格（默认文本表 + `-o json` `Table`）产出紧凑 `Summary`（类型/条数/列/行），大表标注头尾 + 引导 narrow（#18）；`Raw` 不可变原带不变（#19）；`ToolSpec.Description` 改 narrow-first（删「优先 -o json」，教 `--field-selector`/label/`-o jsonpath`）。只动 `internal/tools/k8s`，`core`/`agent` 零改动。beta10 harness（`crashloop-bad-image`）真 LLM smoke 通过。plan 在笔记 `plan/archive/0.1.0-beta11/`；arc 在 `plan/arc/tool-output-navigation.md`；历程《工具输出从字符串到可导航双结构》。缺口：不能 narrow / 不能 time-cursorable 的巨输出在 0.1.0 仍 lossy（见 arc「已识别缺口」段）。

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
| beta11 | 结构化工具输出（L0–L2） | ✅ | #65 `summary.go` 表格投影（文本表 + JSON Table + fallback + 大表标注）+ narrow-first `Spec`；beta10 harness smoke 通过 |
| beta12 | 工具输出 L3 腿 A（大表中段可读） | ✅ | #66 三段式 Summary（列频次 + PCA 异常段 + 取值覆盖段）；`anomaly.go` one-hot + PCA + Hotelling T²；引入 gonum；core/agent 零改；Raw 不变 |
| beta13 | 工具输出 L3 腿 B（普适证据导航） | ✅ | #67 `internal/tools/summary`；#68 `Slicer` + `evidence.read` + 轮内 `ObservationIndex` + k8s Slicer + `MaxStdoutBytes` 配置；core 零改 |
| beta14 | 交互式 TUI + 启动 banner | ✅ | #70 banner + #71 TUI 骨架+容错 + #72 markdown+主题；新硬约束 #20；不触 §4 |
| beta15 | 行内 TUI + 双模式 | ⏳ | #73 Step 1（ergochat/readline + 软换行 + 容错）；#74 Step 2（divider + glamour + 每轮自适应宽）；Step 3（--ui 模式切换）实现中；不触 §4 |

  产品路径（`run`/`chat`）须 LLM 齐全；单元测试用 `agenttest`/`toolstest` 假实现，不依赖 CLI 假闭环。

> PR 与 commit 历史以 `git log` 为权威来源，不在本文件维护清单。

## 下一步

**下一项**：`0.1.0-beta15` Step 3（`--ui` 模式切换）实现中——beta15 收官步；完成后关里程碑归档并蒸馏历程。Step 1 #73、Step 2 #74 已 merged。

**0.1.0 候选**（beta14 之后）：

1. **logs / events 时间游标**（arc《工具输出导航》3b 残留；`SliceQuery` 已留位）
2. **磁盘持久化 / 超巨输出页式**（0.2+ 或重开远景）
3. **TUI 布局可配（L4）/ 组件可插拔（L5）**（arc《TUI》Step 2/3）
4. **流式响应**（arc《流式响应》，0.2+）
5. 后续阶段挂起（investigate / parse 复用 `Suspension`）

**arc《工具输出导航》延后步骤**（详见 `plan/arc/tool-output-navigation.md`）：

- Step **3a** 大表中段可读 → **`0.1.0-beta12`（done，#66）**
- Step **3b** 证据导航 / 巨输出载波 → **`0.1.0-beta13`（done 主段，#67+#68）**；logs 时间游标与超巨页式仍见 arc 缺口
- Step 4 map-reduce（0.2+）
- Step 5 子 agent 分治（0.2+，#15/#17）

**已完成、勿再当候选**：配置文件化（beta8）、`waiting_user` / 澄清挂起（beta9）、场景 harness（beta10）、结构化工具输出 L0–L2（beta11）、L3 腿 A 大表中段可读（beta12）、L3 腿 B 普适证据导航（beta13）。

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

已落地要点（beta2–13 摘要）：

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
11. **beta10**：`scenarios/` + `scripts/` + `lab-*` 可复现 kind harness；以 `chat` 验收为主
12. **beta11**：`projectSummary` 表格投影（文本表 + JSON Table + fallback）；`Evidence.Summary` 从无用占位升为可导航 L0/L1/L2；`Raw` 不可变；narrow-first `ToolSpec.Description`
13. **beta12**：大表 PCA 异常段 + 取值覆盖段（one-hot + PCA + Hotelling T²）；引入 gonum；core/agent 零改；arc Step 3a
14. **beta13**：`internal/tools/summary`；`Slicer` + `evidence.read` + 轮内 `ObservationIndex`；k8s 表格 Slicer；`MaxStdoutBytes` 配置；arc Step 3b 主段

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

公开硬约束见 `architecture.md` #15–#19。

## 当前硬约束摘要

完整清单见 [`docs/architecture.md` 硬约束段](architecture.md#硬约束)。摘要：

- `Run` 不嵌套子实体，扁平 ID 关联
- `Query` 线索必须经 Resolver 真实确认才能成为 `Target`
- 模型输出不能冒充 `Evidence`；`Verdict` 必须引用 `Evidence`
- prompt 从文件加载（`//go:embed`），不写死代码
- 工具接口不限定读写；能力按后端 Tool + Schema 开放，授权由 `Policy`
- **#18**：不得用人为上限阉割正常产品能力；触顶用压缩 / 明确失败
- **#19**：工具输出为可导航双结构（`Summary` 投影 + `Raw` 原带）；工具只做机械格式投影不做业务判断；模型经 Summary 看全貌，按需 narrow 或 `evidence.read` drill，不被逼猜（→ arc《工具输出导航》）
- **#20**：TUI 是纯展示层（不持有业务事实、不假装 Evidence/Verdict；样式经主题 token 不硬编码；Model 预留 streaming buffer 为流式留位）（→ arc《TUI》）
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
