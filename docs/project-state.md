# 项目当前状态

> 最后更新：2026-10-10（benchmark 步骤 3：场景 5→8 + probe.yaml 全场景 + chat-env 注入）

## 当前阶段

**`0.2.0` / 完备不脆弱的证据型诊断助手：🚧 进行中**（远景 2026-08-21 定稿，笔记 `plan/version/0.2.0.md`；定位与排序依据在笔记仓）。对标 codex / opencode 的工程完备度（不中断、不断崖、不截断）。版本节奏：小版本递增，每个 0.1.x = 一个可用的正式实现，完成即打 tag 发布；0.2.0 = 收口版本（含全部增量）。三创新点（代表性投影 / 主动取证决策 / 分层记忆）已随 0.1.1–0.1.3 交付；产品完备地基（持久化 / 流式 / map-reduce 全覆盖）已随 **0.1.4 交付并关闭（2026-10-09）**。**当前活跃小版本：`0.1.5` 对外完备批**（2026-10-09 立项，定位与排序依据在笔记仓）：让 aruing 可被评测、可被安装、可日常用；三功能改动面互不重叠，不碰诊断 core / 编排 / 记忆 / 投影。

| 功能 | 状态 | 摘要 |
| --- | --- | --- |
| benchmark 升格 | 进行中 | 统一跑批入口 `make bench-run` 已落（逐场景 fresh-up 起/拆集群 → 单轮诊断维 + 长会话维 → summary.md/csv + manifest）；rubric 抽样判分层已并入（逐场景池化 + 待回填 / LLM 辅助评两态）与产物开源清洗（manifest 路径归一 + scrub 自检门）；场景 5→8（+configmap-missing / oom-killed / pending-nodeselector）且 probe.yaml 全场景覆盖，chat-env 经 bench-run 注入两维单元（场景声明配置可复现），eval-sweep 提示词回退修复（cases 目录名不再假设 01-default）；余：复现文档 |
| npm 发包 | 未开始 | `npm i -g aruing` 三平台（darwin arm64/amd64 + linux amd64）干净环境装上即用；平台子包挂 release 管线，与 `aruing update` 拒绝自替换语义闭环 |
| `/` 运行时命令 | 未开始 | chat（inline + app）内 `/` 前缀命令（帮助 / 会话 / 退出类，步骤设计钉最小集）；纯 TUI 层（#20），双模式一致 |

版本级完成标志一句话：基准一条命令可复现跑批（产物无私人路径）+ npm 干净环境即用（校验 / 防降级对齐 release）+ `/` 命令双模式一致且未知命令不误发为消息 + `make check` 与 `make smoke-all` 全绿 + `v0.1.5` tag（含 npm 包）。

## 版本履历

已交付版本一行一条。里程碑细节见笔记仓 `plan/archive/`，发版说明见 GitHub Releases；PR 与 commit 历史以 `git log` 为权威，本文件不维护历史清单。

| 版本 | 主题 | tag | 归档（笔记仓） |
| --- | --- | --- | --- |
| 0.1.4 | 产品完备批：持久化（跨重启全量恢复 + 挂起快照 + 超巨输出落盘翻页 + 会话发现）、流式响应全链（token 流 → TUI 逐字渲染，中断无半截）、大表全覆盖 map-reduce 投影 | v0.1.4（2026-10-09） | `plan/archive/0.2.0/0.1.4/` |
| 0.1.3 | 分层记忆组装（信任分层记忆 + 按需回灌）；0.1.2 主动取证决策随版交付 | v0.1.3（2026-09-05） | `plan/archive/0.2.0/{0.1.2,0.1.3}/` |
| 0.1.1 | 代表性投影升级 + 评测基建 | v0.1.1（2026-08-29） | `plan/archive/0.2.0/0.1.1/` |
| 0.1.0 | 可追问的诊断助手（beta1–22：Session + Tower、工具输出导航 L0–L3、TUI、发布管线） | v0.1.0（2026-08-20） | `plan/archive/0.1.0/` |
| 0.0.1 | 最小单轮诊断闭环（beta1–3） | — | `plan/archive/0.0.1/` |

## 工作单元

| # | 模块 | 状态 | 备注 |
| - | --- | --- | --- |
| 1 | `scenarios/` + `internal/eval` + `cmd` bench/judge/probe 接线 | ⏳ | 步骤 1–3 已落（bench-run 统一入口 + rubric 抽样并入 + 开源清洗自检门 + 8 场景全 probe.yaml + chat-env 注入）；余：复现文档；不进 CI 必绿 |
| 2 | npm 包源 + `.github/workflows/release.yml` publish 门禁 | 未开始 | 平台子包（主包 + optionalDeps）复用 release 资产与 checksums；预期零 `internal/` 改动 |
| 3 | `internal/tui` + `cmd` chat 交互接线 | 未开始 | `/` 命令解析与执行、双模式一致、与流式共存语义（步骤设计钉） |

产品路径（`run`/`chat`）须 LLM 齐全；单元测试用 `agenttest`/`toolstest` 假实现，不依赖 CLI 假闭环。

## 下一步

**下一项**：benchmark 步骤 4 推进（复现文档 + 授权小矩阵，维护者现场点）；npm 与 runtime-commands 待认领并行。

**候选方向**（远景与排序依据见笔记 `plan/version/0.2.0.md`；遗留清单见笔记 `plan/archive/0.2.0/0.1.3/2026-8-31-open-issues.md`）：

1. **实验扩点**：断崖曲线 100/200 轮（20/50 两点不可判，P-1）；synthesis 类探针短板的产品侧跟进（P-2，记忆 arc 候选）
2. **产品完备（降位）**：TUI L4 布局可配（arc《TUI》Step 2）等 codex 对齐项
3. **0.2.0 收口**：全部增量交付后收口大版本

**版本节奏**（0.2.0 起）：小版本递增，完成即打 tag；0.2.0 收口（含全部增量）；归档三层（`plan/archive/0.2.0/0.1.x/`）。

## 编排与多轮

| 项 | 结论 |
| --- | --- |
| 诊断管道 | `Orchestrator.Execute` 返 `Outcome`（`Report` 或 `Suspension`）；`Resume` 恢复挂起；#15–#17 不变 |
| 用户侧多轮 | **已落地**：`Session.Turn` + Tower + `aruing chat`（O-1）；挂起时 Tower 入口优先 Resume |
| 正式诊断读回 | **已落地**：`RunLedger`（进程内 + 磁盘） |
| 深解 / 回灌 | **已落地**：索引卡 + 分层检索回灌 |
| 配置文件 | **已落地**：文件 → env → CLI |
| 澄清挂起 | **已落地**：`Suspension`/`Outcome` + resolve/investigate clarify + `Resume`；快照落盘 `suspended/`，跨进程恢复（重启后同会话首条回复即 Resume） |
| 会否推倒 core/tools | **否** |
| 单轮期禁止事项 | 动编排/工具/角色时仍对照笔记 `plan/archive/0.0.1/0.0.1-beta2/2026-7-22.md` §4 |

公开硬约束见 `architecture.md` #15–#20。

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

详细表在笔记仓 `plan/`（含 P/L/C/S/O）。公开侧一句话：

| 编号 | 一句话 |
| --- | --- |
| L-8 | CLI 已有最小 `formatRunError`；更细分类可随配置扩展再补 |
| C-1 | ✅ env 收敛到 `internal/config`；文件化 ✅ |
| O-1 | ✅ 用户侧多轮 / 深解 / 回灌 / 澄清挂起已关 |
| R-1 | ✅ CLI 默认 Markdown，`--format json` 保留 |

更多条目与关闭条件见笔记仓 plan。
