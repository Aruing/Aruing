# 项目当前状态

> 最后更新：2026-08-31（0.1.2-5 B3 ReAct 对比臂 + 决策轨迹插桩落地（统一实验批前置），`agent.acquire.method` 扩 `b3-react`、`RunRecord` 增可选 `decision_trace`、eval-sweep 扩 300 单元；`0.1.3` 实现项全绿：步骤 1 C1 机械校验（#129）、步骤 2 tier-aware 组装器 + `agent.memory.method` 开关（#130）、步骤 3 分层检索回灌（λ₁/λ₂ + 证据 raw 预览，#131）与步骤 4 探针实验装置（#132）已落地；待统一实验批跑数后归档发布 v0.1.3。0.1.2 实现完成（#119/#122/#124/#126 + B3 臂），完成标志 7/8 顺延至 0.1.3 后最终版本统一实验批（2026-08-30 裁决）。0.2.0 远景与排序依据见笔记 `plan/version/0.2.0.md`）

## 当前阶段

**`0.2.0` / 完备不脆弱的证据型诊断助手：🚧 远景已定稿**（2026-08-21，笔记 `plan/version/0.2.0.md`）。定位：对标 codex / opencode 的工程完备度（不中断、不断崖、不截断）。三大支柱：持久化（地基）、记忆管理（信任分层记忆 + 按需回灌）、工具 I/O（预算约束下的代表性投影）。**版本节奏**：废止 beta 里程碑，0.1.1 / 0.1.2 … 每个小版本 = 一个可用正式实现（完成即打 tag 发布），0.2.0 收口含全部增量；里程碑归档三层（`plan/archive/0.2.0/0.1.x/`）。首个里程碑 `0.1.1`（代表性投影升级 + 评测基建）✅ 已发布（v0.1.1，2026-08-29）；`0.1.2`（主动取证决策）✅ 实现完成（实验与发布顺延：随 v0.1.3 一并交付）；当前 `0.1.3` 分层记忆组装（信任分层记忆 + 按需回灌）步骤 1–4（C1 机械校验 / tier-aware 组装器 / 分层检索回灌 / 探针实验装置）已全部落地，实现项全绿，待统一实验批（0.1.2 smoke 与跑数 + 0.1.1 遗留 C4 / 下游端到端 + 0.1.3 探针真跑数）后 0.1.2 + 0.1.3 一起归档、单 tag v0.1.3 发布；持久化随 0.1.4+ / 0.2.0 收口前交付（排序依据见笔记 `plan/version/0.2.0.md`）。

**`0.1.1` / 代表性投影升级 + 评测基建：✅ 已发布**（2026-08-29，v0.1.1 tag）。三步交付：①加权贪心代表性投影（覆盖 + T² 目标、头尾锚预置、基数折算 CELF、knapsack 对照变体）与同二进制方法开关 `tools.projection.method`（fast/greedy/greedy-knapsack/full/head-tail/uniform/llm-rerank；C4 llm-rerank 为实验对照臂须显式配置且装配重排器，产品默认 fast 不变）（#112）；②评测基建（场景 `ground_truth` 四元组、`run --eval-json` 成功/挂起/失败三路径全记、`judge` 判分子命令 ①②层全自动 + ③层抽样、llm 用量按角色记账、参数化大表生成器固定种子可复现）（#114）；③`make bench` 机械主实验（矩阵 N×预算×根因位置×种子×方法遍历 → CSV + 矩阵快照 + 出图脚本；random/simplestat 消融臂 bench 侧注入不进 config）（#116）。core / agent / Orchestrator 全程零改（#15–#17）。map-reduce 两遍分片裁决后移 0.1.4（arc Step 4）；C4 / 下游诊断 LLM 端到端矩阵为可选实验，可与 0.1.2 冒烟同批授权跑。plan 已归档至笔记 `plan/archive/0.2.0/0.1.1/`。

**`0.1.0` / 可追问的诊断助手：✅ 已发布**（2026-08-20，v0.1.0 tag）。版本文档已退役至笔记 `plan/archive/0.1.0/0.1.0.md`（版本归档总览）。发布管线（beta22）首次实战闭环：production 分支模型 + tag 驱动 GoReleaser 发版 + 一行安装（curl/irm）+ `aruing connect` 配置向导 + `aruing update` 自更新 + `go install github.com/Aruing/Aruing/cmd/aruing@v0.1.0` 渠道。

**`0.1.0-beta22` / 发布管线与多渠道分发** ✅ 完成并归档（2026-08-20；#94–#106）：production 分支模型（merge-only + 11 项 status checks 分支保护）+ release-check（5 平台交叉编译 + 三系真机冒烟）+ GoReleaser tag 发版（draft → 人工 publish）+ install.sh/ps1（stable 直链免限流 + sha256 单行比对）+ aruing connect（向导/连通测试/原子写盘）+ aruing update（minio/selfupdate + 版本方向防降级 + npm 来源检测）。v0.1.0-rc1 演练 + v0.1.0 正式发布均全链验收。7 轮 pr-agent 评审（openpgp 换库 / 压缩包解包 / 降级路径 / 302 取 tag / TOCTOU 等）。npm 平台子包经维护者决策转发布后补做。plan 在 `plan/archive/0.1.0/0.1.0-beta22/`。

**`0.1.0-beta21` / TUI 留白统一与称呼可配** ✅ 完成并归档（2026-08-18；#92）：块间距从 lipgloss 样式项剥离为 spacing 显式值（根治 Render 块级 margin 与手动空行双重叠加；显式 0 合法），默认视觉基线 = 输入与 spinner/回复间恰 1 空行、轮间分割线两侧各恰 1 空行；默认消息无「你 / aruing」前缀，主题 labels 开关（默认关）开启后称呼独立一行 + 换行 + 内容；TurnProgress 协调器修复 spinner 与编排进度行同屏踩踏（beta15 起遗留：进度行落屏前清 spinner、落后重画到最新行下方）。YAML 面 `styles.<role>.margin` 不变（beta20 主题文件兼容）；core / agent / session / config 零改。收尾 self-check：静态链全绿 + 四场景 smoke 全 ok（log-time-window / svc-wrong-selector 两场景 attention 为环境网络抖动与 expect 口径，非产品回归）。plan 在笔记 `plan/archive/0.1.0/0.1.0-beta21/`。

**`0.1.0-beta18` / 工程效能与质量反射** ✅ 完成并归档（2026-08-16；#79/#81/#83/#84/#85）：非产品能力里程碑。交付：`aruing-milestone-close` 收尾外包 + PR 元数据自动化（#79）；`make smoke-all` / `self-check` + `aruing-self-check` skill，4 场景实测全绿（#81）；同集群多 case 协议 + smoke-all 改造（#83）；`aruing-cluster-smoke` 真集群测试纪律 skill + investigate 挂起强测 case（#84，顺带补上 beta19 遗留的真 LLM smoke）；`aruing-retrospective` 反思 skill（卫生扫描四分类首报 32 条全裁决 + 守规审计不设门禁，#85）。并行开发思路按维护者决定仅存档不落地。产品代码零改。plan 在 `plan/archive/0.1.0/0.1.0-beta18/`。

**`0.1.0-beta20` / 主题 YAML 完整化（TUI L3 收尾）** ✅ 完成并归档（2026-08-16；#82）：`tui.theme_file`（写明才加载）→ 基于 dark/light 基底**部分覆盖**全样式项（颜色/粗体/边框/内边距/块间距）；非法值/未知角色名启动明确报错；`tui.example.yaml` 全注释示例。交付后真终端实测暴露并修复**根因**：`config.LoadFile` 漏拷 TUI 段——config 文件的 `tui.*` 自 beta8 起从未生效（仅 env/flag 路径工作）；另修 theme_file 相对路径按 config 目录解析、user 样式项 inline 消费（提交后重印「你 」留痕）、单句模式提示。margin 最终形态：基底表只存颜色/粗体，块间距消费点显式输出（fallback 保视觉基线）。pr-agent 四轮评审协作修复 4 个渲染 bug。core/agent/session 零改。arc《TUI》Step 1 收口（L4/L5 留 0.2+）。plan 在 `plan/archive/0.1.0/0.1.0-beta20/`。

**`0.1.0-beta19` / 调查阶段挂起（investigate clarify）** ✅ 完成并归档（2026-08-15；#80）：`Plan` 增可选 `Clarify`（规划器在证据不足以继续且缺口信息用户知道时提议；与任务/猜想互斥，LLM 侧与编排侧双层校验）；`InvestigateState` 种子化（调查循环可续跑，镜像定位循环）；挂起快照带 `Stage` **与侦察产物**（`reconResult`：证据 + 资源清单 + 插位，续跑复用），`Resume` 派发：investigate 路径保留调查进度（猜想/任务/证据/判决）续跑、重置轮次预算、不重复解析/侦察；planner prompt + LLM 输出结构 + agenttest 脚本假实现。session/Tower/CLI 零改（beta9 通用抽象兑现检验）。pr-agent 两轮评审抓到并修复两个真回归（规划输入丢目标、快照丢侦察产物）。plan 在 `plan/archive/0.1.0/0.1.0-beta19/`。

**`0.1.0-beta17` / logs 时间游标（evidence.read 时间窗切片）** ✅ 完成并归档（2026-08-15；#78）：`SliceQuery` 增 `Since`/`Until`（RFC3339 闭区间，通用契约）；k8s `Slicer` 对行首 RFC3339（`logs --timestamps` 产物）机械过滤再开窗；无时间戳/表格遇时间窗明确报错引导（#18）；meta 回填窗内首末时间戳；Spec / tower prompt 教学同步（取 logs 加 `--timestamps`）。core / 编排零改；新增 `scenarios/log-time-window` smoke 场景，kind + 真 LLM 正/负路径 smoke 通过。arc Step 1–3b 在 0.1.0 承诺全部兑现。plan 在 `plan/archive/0.1.0/0.1.0-beta17/`。

**`0.1.0-beta16` / 巨输出分页（非表格输出行级页式）** ✅ 完成并归档（2026-08-14；#77）：arc《工具输出导航》Step 3b 残留收尾。k8s `Slicer` 行级兜底（非表格 stdout 按物理行拆单列、空行保留、`Columns=nil`）；`evidence.read` 行模式渲染（绝对行号 + 单行 240 runes 截断标注）；`fallbackSummary` 首尾行预览 + 翻页提示；Spec / tower prompt 教学同步。core / 编排 / 协议零改。仍 lossy 残留：超 `MaxStdoutBytes` 截断（0.2+ 落盘页式）；logs 时间游标 → beta17。plan 在 `plan/archive/0.1.0/0.1.0-beta16/`。

**`0.1.0-beta15` / 行内 TUI（pi 留痕风格）+ 双模式** ✅ 完成并归档（2026-08-14；#73 行内引擎（自写翻车→ ergochat/readline + shift+enter 软换行）；#74 轮间 divider + glamour + 每轮自适应宽；#76 `--ui`/`tui.mode` 双模式接选 + app 模式 tty 预检/AltScreen/渲染绑真终端 hotfix ×2；toolchain go1.26.6 清标准库 CVE）。历程《行内 TUI 与双模式》。plan 在 `plan/archive/0.1.0/0.1.0-beta15/`。

**`0.1.0-beta14` / 交互式 TUI + 启动 banner** ✅ 完成并归档（2026-08-13；#70 banner + #71 TUI 骨架+容错 + #72 glamour markdown+主题；新硬约束 #20；历程《交互式终端与可定制主题基础》《为流式留位》）。plan 在 `plan/archive/0.1.0/0.1.0-beta14/`。

**`0.1.0-beta13` / 工具输出 L3 腿 B（普适证据导航）** ✅ 完成并归档（2026-08-13；#67+#68）：arc《工具输出导航》Step 3b 主段。#67 纯重构：表格导航算法（PCA/覆盖/频次）抽到 `internal/tools/summary`，k8s 只留格式解析。#68：可选 `tools.Slicer` + `summary.SliceRows`；注册工具 `evidence.read`（经 Dispatcher/Policy，#17）；基线成功观察由 Tower 经 Factory 发 `evidenceId` 写入轮内 `ObservationIndex`，`Respond` 结束 `Discard`；k8s 实现 Slicer（文本表 / JSON Table）；`MaxStdoutBytes` 可配置；不可切片（describe/logs）返错误引导 re-query。导航结果不 Put 回索引。`core.Evidence` 零改。plan 在笔记 `plan/archive/0.1.0/0.1.0-beta13/`；arc 在 `plan/arc/tool-output-navigation.md`。未做：日志时间游标、超巨页式存储（0.2+）、formal RunLedger drill。

**`0.1.0-beta12` / 工具输出 L3 腿 A（大表中段可读）** ✅ 完成并归档（2026-08-12；#66）：arc《工具输出导航》Step 3a。大表（> 64 行）`Summary` 从「头 8 + 尾 4」升级为**三段式（列频次 + PCA 异常段 + 取值覆盖段）**：`significantColumns` 过滤低基数非清一色列 → `encodeOneHot` → Gonum `stat.PC` 算 PCA → 每行按 Hotelling's T² 排序选前 8 异常行（带 `← T²=X.XX` 标注）；覆盖段保证每个非主流取值至少 1 行代表 + 中段均匀步长补全；头/尾各 4 固定。算法依据：Pearson 1901 / Hotelling 1933 PCA、Mahalanobis 1936、Hotelling 1947 T²、Benzécri 1973 MCA 简化形态。引入 `gonum.org/v1/gonum v0.17.0`。兜底：`N<30` / 方差为 0 / 协方差奇异 → 异常段为空、全靠覆盖段。只动 `internal/tools/k8s`，`core`/`agent` 零改；`Raw` 不变。plan 在笔记 `plan/archive/0.1.0/0.1.0-beta12/`；arc 在 `plan/arc/tool-output-navigation.md`；历程《工具输出大表中段可读》。

**`0.1.0-beta11` / 结构化工具输出（L0–L2）** ✅ 完成并归档（2026-08-11；#65）：arc《工具输出导航》Step 1-2。k8s 工具对 `get` 类表格（默认文本表 + `-o json` `Table`）产出紧凑 `Summary`（类型/条数/列/行），大表标注头尾 + 引导 narrow（#18）；`Raw` 不可变原带不变（#19）；`ToolSpec.Description` 改 narrow-first（删「优先 -o json」，教 `--field-selector`/label/`-o jsonpath`）。只动 `internal/tools/k8s`，`core`/`agent` 零改动。beta10 harness（`crashloop-bad-image`）真 LLM smoke 通过。plan 在笔记 `plan/archive/0.1.0/0.1.0-beta11/`；arc 在 `plan/arc/tool-output-navigation.md`；历程《工具输出从字符串到可导航双结构》。缺口：不能 narrow / 不能 time-cursorable 的巨输出在 0.1.0 仍 lossy（见 arc「已识别缺口」段）。

**`0.1.0-beta10` / 可复现场景 harness（kind）** ✅ 完成并归档（2026-08-11；#64）：`scenarios/` + `scripts/` + `lab-*` Make 目标（up/down/list/chat/kube）+ 三场景（crashloop-bad-image / svc-wrong-selector / same-name-multi-ns）；验收**以 `chat` 为主**（`run` 不单独验收）；LLM chat smoke 通过（crashloop / same-name-multi-ns）。不进 `make test`。plan 在笔记 `plan/archive/0.1.0/0.1.0-beta10/`；历程《可复现的故障台架》。

**`0.1.0-beta9` / 澄清挂起（waiting_user）** ✅ 完成并归档（2026-08-10；#63）：`core.Suspension`/`Outcome` 通用抽象；`ResolveActionClarify` + `Clarifications`；`Orchestrator.Execute` 返 `Outcome` + `Resume`/`FindSuspended`；`session.SuspendedRunner`（可选）+ `session.Resume` + `ModeClarify`；Tower 入口挂起恢复优先；CLI `run` 遇挂起打印问题并退出。plan 在笔记 `plan/archive/0.1.0/0.1.0-beta9/`；历程《挂起与恢复的第一锤》。#15–#17 兑现的第一锤。

**`0.1.0-beta8` / 配置文件化** ✅ 完成并归档（2026-08-10；#61 YAML + env 覆盖 + 路径链 + CLI 须 LLM；#62 config banner / playground ignore）。示例：`aruing.example.yaml`；`--config` / `ARUING_CONFIG` / 默认搜索路径。plan 在笔记 `plan/archive/0.1.0/0.1.0-beta8/`。

**`0.1.0-beta7` / 压缩后按范围回灌（PR-C）** ✅ 完成并归档（2026-08-08）：`locateRange` → `rehydrateRange` → `compactRange`；`rehydrated_messages`（#59）；LLM smoke 路径 B。plan 在 `plan/archive/0.1.0/0.1.0-beta7/`。

**`0.1.0-beta6-fix-1` / 网关空响应 · Tower 可恢复重试** ✅ 完成并归档（2026-08-07 · **修复型**）：#58。plan 在 `plan/archive/0.1.0/0.1.0-beta6-fix-1/`。

**`0.1.0-beta6` / 按 run 深解** ✅ 完成并归档（2026-08-06）：`RunLedger` + `prior_run_details`。plan 在 `plan/archive/0.1.0/0.1.0-beta6/`。

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
| beta15 | 行内 TUI + 双模式 | ✅ | #73（ergochat/readline + 软换行 + 容错）；#74（divider + glamour + 每轮自适应宽）；#76（--ui 双模式 + hotfix）；不触 §4 |
| beta16 | 巨输出分页（非表格行级页式） | ✅ | #77 k8s `Slicer` 行级兜底 + `evidence.read` 行渲染（行号/截断标注）+ `fallbackSummary` 首尾预览 + 翻页提示；core 零改；不触 §4 |
| beta17 | logs 时间游标（时间窗切片） | ✅ | #78 `SliceQuery.Since/Until`（RFC3339 闭区间，通用契约）；k8s 行首 RFC3339 机械过滤再开窗；无时间戳/表格遇时间窗明确报错引导；窗内首末时间戳 meta；`scenarios/log-time-window`；不触 §4 |
| beta19 | 调查阶段挂起（investigate clarify） | ✅ | #80 `Plan.Clarify` + `InvestigateState` 种子化 + `Resume` 按 Stage 派发（保留进度续跑）；挂起快照携带侦察产物；session/Tower/CLI 零改 |
| beta20 | 主题 YAML 完整化（TUI L3 收尾） | ✅ | #82 `tui.theme_file` 部分覆盖 + 全样式项 + 示例文件；根因修复 LoadFile 漏拷 TUI 段（beta8 起潜伏）；#20 全量兑现 |
| beta21 | TUI 留白统一与称呼可配 | ✅ | #92 spacing 归一（margin 剥离样式项）+ labels 称呼开关（默认关）+ spinner 归入助手块；默认无称呼前缀；TurnProgress 修复 spinner 与进度行同屏踩踏 |
| beta22 | 发布管线与多渠道分发 | ✅ | #94–#106；分支模型 + GoReleaser 发版 + 安装脚本 + connect + update；**v0.1.0 已发布**（7 项真机验收）；npm 子包遗留转 0.2.0 候选 1 |
| beta22-1 | 版本注入（ldflags） | ✅ | `var version/commit/date`（源码默认 dev）+ Makefile LDFLAGS；`make version` 改为构建后跑；version 输出三行（版本/commit/构建时间） |
| beta22-2 | 模块路径替换 | ✅ | `aruing` → `github.com/Aruing/Aruing`（go.mod + 70 文件 152 行 import，纯机械）；`go install` 渠道随 v0.1.0 tag 生效 |
| beta22-3 | production 分支 + 发布检查 | ✅ | #96/#98 release-check（PR to production：5 平台交叉编译 + 三系真机冒烟，macOS SIGPIPE 修复）+ #97 首个同步 PR 15 项 checks 全绿合并；分支保护（ruleset merge-only + 11 项 status checks）已配 |
| beta22-4 | GoReleaser + release workflow | ✅ | #99/#100 + **v0.1.0-rc1 全链真跑验收**（tag 校验→5 平台构建→draft Release→publish；实测：releases/latest 不含 pre-release、checksums 按规范名匹配） |
| beta22-5 | 安装脚本 + README 安装段 | ✅ | #101；stable 直链优先（零 API 免限流）+ 单行校验和比对 + 双语 Install 段；macOS 真装验证 |
| beta22-6 | aruing connect 配置向导 | ✅ | #102+#105：交互向导（隐藏输入/预读竞态修复）+ 连通测试 + SaveLLM 原子写（0600 临时文件 rename）+ 未知段保留 |
| beta22-7 | aruing update 自更新 | ✅ | #103+#105：minio/selfupdate + stable 直链 302 取 tag + 版本方向防降级 + checksums 校验 + 解包替换 + npm 来源检测；rc1→0.1.0 真升级验收 |
| beta18 | 工程效能与质量反射 | ✅ | #79/#81/#83/#84/#85 收尾/自检/纪律/反思 skills + cases 协议 + smoke-all；产品代码零改 |
| 0.1.3-4 | 探针实验装置 | ✅ | #132；eval 侧探针装置（probe.yaml 规格 + 种子脚本生成器：问答/诊断 3–5 间隔 + 尾部探针；from_ledger 期望展开 kth_run_pods / kth_run_commands，k=-1 末次，判分侧纯①层包含）+ 函数注入 ProbeRunner 组装会话级记录（探针轮无 Run，与 RunRecord 并列；逐诊断内嵌完整 RunRecord）+ judge --probe（no_diagnosis / no_facts 不进分母单列）+ `aruing probe` 子命令（chat 同源 wiring，--dry-run 零 LLM）+ `make probe-sweep`（2 场景 × 3 方法 × 2 轮数 × 3 重复 = 36 会话，DRYRUN 干跑）+ 两场景 probe.yaml；agent 侧 `TowerResponder.LastMemoryStats` 只读观测（定位层 / λ₂ 调用 / 回灌条目数）；真跑数推迟统一实验批 |
| 0.1.3-3 | 分层检索回灌 | ✅ | #131；λ₁ `locateByAddress` 确定性寻址每轮必跑（ID 族正则 + 会话级资源名词典 `buildEntityDict`：诊断账本证据 CommandView/Summary 机械抽 DNS-1123 token，flag/镜像/选择器噪声不进词典）∩ 单元地址集（消息 = 正文地址 + RunID；证据 = e_ 编号 + 词典实体），锚点类 recall = 1；λ₂ `llmLocateRange` 原样兑底（λ₁ 空 + 视图压缩丢细节 + 语义指涉时一次）；证据命中必回灌 raw 预览（`mode=evidence` 合成条目并入 `rehydrated_messages`，idx=-1、C1 保地址），消息命中仅视图丢细节时回灌原文；迭代口径 = 轮内单遍、预算不足预览跨轮收敛（定理 1 对齐）；退役 `ruleLocateRange`/`relevanceScore` 族；白话走查 60 轮 3 诊断入单测；core/session/tools/config 零改 |
| 0.1.3-2 | tier-aware 组装器 + 记忆方法开关 | ✅ | #130；`internal/agent` 新增 `memory_cards.go`（R 层索引卡：`buildMemoryCards`，字段钳 c_max=200、不带 raw、地址不钳；κ₂ 接口预留占位）+ `assembler.go`（`assembleTieredView` ours：R 锁定 + W 最近 w 轮 LRU + C 中段压缩每段过 C1，预算 R 先取满；`assembleLastN`/`assembleFlatSummary` D1/D2 纯对照臂）；Respond 按 `agent.memory.method` 分派（config `Agent.Memory` method+last_n，env ARUING_AGENT_MEMORY_*，未知值启动报错；D1/D2 无卡片无回灌）；`buildPriorRunDetails` 退役（ours 下 prior_run_details 由带 raw 深材料改为卡片，深细节归回灌/evidence.read）；payload 契约与 prompt 零改 |
| 0.1.3-1 | C1 机械校验（压缩出口地址无损） | ✅ | #129；`internal/agent` 新增 `addrcheck.go`：ID 族正则（Factory 全部前缀）抽取 + `ensureAddrCoverage` 缺号以 `[addr_refs]` 行补附（幂等；词典实体参数留面、无权威词典前传 nil）；接线全部有损变换出口——`truncateContentPreview` / `foldLine` / `forceCompactMark` 内联（签名不变，复用路径自动覆盖含 beta7 回灌窗）+ L2 checkpoint 落库正文机械兑底（不信任模型 run_ids，RunID 字段纳入比对源）；footer 计入既有预算估算；core / session / tools / config 零改 |
| 0.1.2-1 | acquire 决策计算内核 | ✅ | #119（含 6 轮 pr-agent 评审：前 5 条真缺陷按根因修复——Action 封闭不变量 + Options 全参数域校验；第 6 轮 1.5 条采纳 1 条证伪钉板）； `internal/agent/acquire` 纯函数包（零接线零依赖）：对数域贝叶斯更新 + EIG（bit）+ argmax EIG/c + MSPRT 三出口 + 强度更新 ℓ = 2^(α·d·s)（对数线性，思考文档 §3.4 随走查修正）+ §7 数值算例复算单测（EIG=0.71 bit / 后验 0.902/0.988 / 两次动作收敛）；core/编排零改 |
| 0.1.2-2 | Planner 决策输出结构与 prompt 改造 | ✅ | #122；`core.Hypothesis.Confidence`（[0,1] 单字段，语义随写入方：决策规划写先验 / 决策循环写后验；architecture 数据表同步）+ `agent.PlanDecision` / `ActionProposal` / `StrengthJudgement`（动作级容错：非法动作丢弃并计数，全坏才报错；问用户成本固定 10）+ `LLMDecisionPlanner`（prompt `planner-decision.md`，{{TOOL_SPECS}} 注入，计划级违规重试）+ `LLMVerifier.JudgeStrength`（prompt `strength.md`，逐假设恰好一条 (d,s)，Verify 原语义不动）+ agenttest `FakeDecisionPlanner` / `FakeVerifier.JudgeStrength`（关键词表回放）；prompt 示例契约测试；旧 planner.md / LLMPlanner / investigateLoop 零改（B1 保真）；零编排接线（步骤 3） |
| 0.1.2-3 | 取证决策循环接线 | ✅ | #124；`acquireLoop`（ours 路径）与旧 `investigateLoop` 并行分派（`SetAcquireMethod`，config `agent.acquire.method` 空 = ours，b1-serial 零改保真）：argmax EIG/c 选择 → 动作映射 Task 全经 Dispatcher（#16）→ 机械归类优先（Summary 唯一命中）/ 零多命中走 JudgeStrength 强度更新 → MSPRT 三出口（supported → 正式 Verify + 后验回写 Confidence；refuted → abduction 重规划旧假设保留压低；insufficient → 平台/预算尽带缺口进 LastRunStats）→ 问用户统一建模（高成本动作，复用 investigate clarify 挂起，Resume 答复按结果类别归类更新，决策状态随 InvestigateState.Acquire 进快照）；config `agent.acquire` 段（method + 全套参数，env 覆盖）+ 装配层校验（未知 method / 缺角色启动报错）；顺手修存量 bug：LoadFile 此前漏拷 Tools.Projection（YAML 投影配置从未生效，beta20 TUI 同族）；agenttest 假实现端到端 + 三出口 + ask 挂起恢复 + 分派防御测试 |
| 0.1.2-4 | 实验矩阵基建（选择策略实验臂 + eval record 扩展 + eval-sweep 驱动器） | ✅ | #126；`agent.acquire.method` 枚举扩 `b2-random`（种子随机：(seed,信念) 哈希纯函数可复现，种子 config `agent.acquire.seed`）/ `b4-cheapest`（argmin cost 并列取索引小）——两臂复用 acquireLoop 全部机制只换选择策略（修订步骤 3 裁决 1：实验须走 run 真路径，config 是唯一开关面）；`RunRecord` 增 acquire_method / acquire_max_rounds / acquire_seed / acquire_exit / acquire_gap（schema v1 向后兼容加字段；judge 结果透传 method/K/实测 rounds 供分组）；`scripts/eval-sweep.sh` + `make eval-sweep`（4 场景 × 4 方法 × 5 K × 3 重复 = 240 单元；DRYRUN 干跑核对、逐场景独立 kind 集群 KUBECONFIG、单元失败不中断全量报告、逐场景 judge 汇总 → CSV 联结观测量）；`bench-plot.py --mode acquire`（实测 rounds 预算曲线 + 名义 K 均值±std 汇总表）；真跑数与 smoke 待授权（完成标志 7/8） |
| 0.1.2-5 | B3 ReAct 对比臂 + 决策轨迹插桩（统一实验批前置） | ✅ | #134；`agent.acquire.method` 枚举扩 `b3-react`（ReAct 式 LLM 自由决策强对照：`LLMReActSelector` 每轮一次选择调用，prompt `react-select.md`，信息面与 ours 同构只少数学层（同菜单/成本/假设证据摘要，不喂矩阵与后验）；无贝叶斯/EIG/MSPRT，声明足够 → 正式 Verify 不回写 `Confidence`、预算尽/动作空间平台 → insufficient；复用 acquireLoop 骨架：池尽重规划（假设列表合并不重建信念）/ ask 挂起（答复进澄清累积不归类）/ 出口管线同构；装配层缺选择器启动报错）；决策轨迹插桩：`RunStats.DecisionTrace` 逐轮快照（ours/b2/b4 臂候选 EIG/c 得分 + 选择 + 信念前后，B3 臂选择与理由原文；`Selection.Scores` 只读观测列；非有限得分封顶）+ `RunRecord` 可选 `decision_trace`（schema v1 兼容，b1-serial 臂为空；eval 镜像结构经 cmd 桥接转换）+ probe 诊断内嵌记录同构携带；eval-sweep METHODS 加 b3-react（4×5×5×3=300 单元，DRYRUN 核对）；跑数随统一实验批 |
| 0.1.1-3 | bench 遍历 + 消融对照臂 + 主实验 | ✅ | #116；`eval` bench runner（BenchMatrix YAML / 内置默认 2400 单元 → RunBench → CSV + 矩阵快照）+ RandomPick 随机消融臂 + `summary` SimpleStat 简单统计量消融臂（bench 注入不进 config）+ `RenderWithStats` 观测量 + C4 llm-rerank 方法（`RerankFunc` 注入，k8s `NewReranker` + go:embed prompt，装配层校验 LLM）；`aruing bench` 子命令 + `make bench` + `scripts/bench-plot.py`（位置柱状 / 预算曲线 / 消融表）；core/agent 零改 |
| 0.1.1-2 | 评测基建 A（ground_truth + eval-json + 判分 CLI） | ✅ | #114；含两轮 pr-agent 评审采纳（evidence_ids 空切片归一、rubric 单元格净化）；`internal/eval` 新包（大表生成器 / run 记录 / 判分①②③抽样）；llm 用量记账（Request.Label + LabelingClient + UsageTracker，角色零改）；Orchestrator LastRunStats 只读统计；`run --eval-json`（成功/挂起/失败三路径全记）；`judge` 子命令；4 场景 ground_truth 回填；core 零改 |
| 0.1.1-1 | 加权贪心代表性投影 + 方法开关 | ✅ | #112；`summary` 新增 greedy（覆盖 + T² 目标，锚预置，基数折算 CELF；knapsack 对照变体）+ RenderWithOptions 方法分发（full/head-tail/uniform 基线）+ config `tools.projection` 开关 + k8s 透传；T² 修正为平方口径（总体方差）；core/agent 零改 |

  产品路径（`run`/`chat`）须 LLM 齐全；单元测试用 `agenttest`/`toolstest` 假实现，不依赖 CLI 假闭环。

> PR 与 commit 历史以 `git log` 为权威来源，不在本文件维护清单。

## 下一步

**下一项**：**统一实验批**（0.1.3 实现项全绿后启动，须维护者授权真跑）：①0.1.2 完成标志 7 真 LLM smoke + 8 实验数据（eval-sweep 300 单元，含 b3-react 臂）；②0.1.1 遗留 C4 llm-rerank / 下游诊断 LLM 端到端矩阵；③0.1.3 探针真跑数（probe-sweep 36 会话 + 断崖曲线出图 `bench-plot.py --mode probe` 随批补）；跑数全绿后 0.1.2 + 0.1.3 完成标志置绿归档、单 tag v0.1.3 发布。**实验批次调整（2026-08-30 裁决）**：0.1.2 完成标志 7（真 LLM smoke）/ 8（实验数据）、0.1.1 遗留 C4 / 下游 LLM 端到端、0.1.3 探针实验——统一推迟到 0.1.3 完成后的最终版本上同批跑（实验数据一律以最终版本为装置，不做中途增量跑数）；届时 0.1.2 完成标志全绿归档（不独立发版，随 v0.1.3 一并交付——发布裁决 2026-08-30）。排序依据见笔记 `plan/version/0.2.0.md`。

**0.2.0 候选**（0.1.1 之后；远景与排序依据见笔记 `plan/version/0.2.0.md`）：

1. **主动取证决策（0.1.2）**：调查循环内证据信息增益（EIG）/ 贝叶斯信念 / 序贯检验停止准则决策模块（新包，编排侧）
2. **分层记忆组装（0.1.3）**：信任分层记忆 + 按需回灌（beta7 回灌雏形升格），长会话跨压缩追问不失忆
3. **持久化 + 落盘页式（0.1.4+ / 收口前）**：Session / Message / RunLedger / Evidence / 挂起 Run 落盘 + 超 `MaxStdoutBytes` 落盘分页（arc 遗留缺口）+ map-reduce 两遍分片（0.1.1 收口裁决后移，arc Step 4）
4. **benchmark 升格（延后）**：kind 场景 harness → 可复现评测基准（0.1.1 评测基建之上扩场景）；产品完备（流式 / TUI L4 / npm 子包 / `/` 运行时命令）

**版本节奏**（0.2.0 起）：废止 beta 里程碑；0.1.1 / 0.1.2 … 每个小版本 = 一个可用正式实现，完成即打 tag 发布；0.2.0 收口（含全部增量）；里程碑归档三层（`plan/archive/0.2.0/0.1.x/`）。

**发布运维备忘**（0.1.0 起生效）：发布 = main 快照分支 → PR to production（15 项检查）→ merge commit 合并 → production 打 `v*` tag → draft Release 人工 publish；流程固化在 `CONTRIBUTING.md` Releasing 段。用户升级路径：`aruing update`（脚本安装）/ `npm update -g aruing`（npm 安装，待 npm 渠道上线）。

**arc《工具输出导航》延后步骤**（详见 `plan/arc/tool-output-navigation.md`）：

- Step **3a** 大表中段可读 → **`0.1.0-beta12`（done，#66）**
- Step **3b** 证据导航 / 巨输出载波 → **`0.1.0-beta13`（done 主段，#67+#68）**；非表格输出的页式内存变体（行级兜底）→ **`0.1.0-beta16`（done，#77）**；logs 时间游标 → **`0.1.0-beta17`（done，#78）**；落盘页式（超 `MaxStdoutBytes` 留存）→ `0.1.4+`（随持久化）
- Step 4 map-reduce → `0.1.4`（0.1.1 收口裁决后移，2026-08-29；两遍分片，片内异常判定用 Pass 1 全局基准）
- Step 5 子 agent 分治（0.2+，看余量再定，#15/#17）

**已完成、勿再当候选**：配置文件化（beta8）、`waiting_user` / 澄清挂起（beta9）、场景 harness（beta10）、结构化工具输出 L0–L2（beta11）、L3 腿 A 大表中段可读（beta12）、L3 腿 B 普适证据导航（beta13）、非表格行级分页（beta16）、logs 时间游标（beta17）。

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
| 单轮期禁止事项 | 动编排/工具/角色时仍对照笔记 `plan/archive/0.0.1/0.0.1-beta2/2026-7-22.md` §4 |

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
