# 项目当前状态

> 最后更新：2026-10-17（保留 0.1.6 规划，同步 npm 功能分支集成及已发布验收状态）

## 当前阶段

**`0.2.0` / 完备不脆弱的证据型诊断助手：🚧 进行中**（远景 2026-08-21 定稿，笔记 `plan/version/0.2.0.md`；定位与排序依据在笔记仓）。对标 codex / opencode 的工程完备度（不中断、不断崖、不截断）。版本节奏：小版本递增，每个 0.1.x = 一个可用的正式实现，完成即打 tag 发布；0.2.0 = 收口版本（含全部增量）。三创新点（代表性投影 / 主动取证决策 / 分层记忆）已随 0.1.1–0.1.3 交付；产品完备地基（持久化 / 流式 / map-reduce 全覆盖）已随 0.1.4 交付；对外完备批（评测基准 + 高难场景 + `/` 运行时命令）已随 **0.1.5 交付（2026-10-16）**。**当前活跃小版本：`0.1.6` 对外完备批·续**（2026-10-16 立项；npm 自 0.1.5 挪版——owner 分支推进节奏拉长，维护者裁决 0.1.5 三功能先行发布；功能清单活段，后续可增补）：补齐可安装 + 升级行内命令体验；不碰诊断 core / 编排 / 记忆 / 投影。

| 功能 | 状态 | 摘要 |
| --- | --- | --- |
| npm 发包 | ⏳ 集成与验收中 | owner Go-Go-Zeppelin：四个步骤已合入功能主分支 `feature/0.1.5-npm`，汇总 PR #194 待合入 `main`；`@aruing` 四包已发布 0.1.4，macOS arm64 的 registry 安装、版本、更新保护与帮助入口通过，三平台候选安装 CI 通过；完整产品验收、其他平台 registry 安装及自动发布认证待完成 |
| `/` 行内命令体验 | 未开始 | pi 式命令面板：输入 `/` 即菜单（命令 + 用途，与 `/help` 同源）、随输入过滤、键位导航选择/补全/执行；inline / app 双模式一致；`internal/tui` 纯展示层（#20） |

版本级完成标志一句话：npm 干净环境即用（校验 / 防降级对齐 release）+ 命令面板双模式一致且与流式共存无半截 + `make check` 与 `make smoke-all` 全绿 + `v0.1.6` tag（含 npm 包）。

## 版本履历

已交付版本一行一条。里程碑细节见笔记仓 `plan/archive/`，发版说明见 GitHub Releases；PR 与 commit 历史以 `git log` 为权威，本文件不维护历史清单。

| 版本 | 主题 | tag | 归档（笔记仓） |
| --- | --- | --- | --- |
| 0.1.5 | 对外完备批：可复现评测基准（bench-run 统一跑批 + rubric 抽样 + 开源清洗 + 复现手册）、4 个高难诊断场景（症状与根因拉开因果距离 + 诱饵须证据排除）、chat 内 `/` 运行时命令五命令最小集 | v0.1.5（2026-10-16） | `plan/archive/0.2.0/0.1.5/` |
| 0.1.4 | 产品完备批：持久化（跨重启全量恢复 + 挂起快照 + 超巨输出落盘翻页 + 会话发现）、流式响应全链（token 流 → TUI 逐字渲染，中断无半截）、大表全覆盖 map-reduce 投影 | v0.1.4（2026-10-09） | `plan/archive/0.2.0/0.1.4/` |
| 0.1.3 | 分层记忆组装（信任分层记忆 + 按需回灌）；0.1.2 主动取证决策随版交付 | v0.1.3（2026-09-05） | `plan/archive/0.2.0/{0.1.2,0.1.3}/` |
| 0.1.1 | 代表性投影升级 + 评测基建 | v0.1.1（2026-08-29） | `plan/archive/0.2.0/0.1.1/` |
| 0.1.0 | 可追问的诊断助手（beta1–22：Session + Tower、工具输出导航 L0–L3、TUI、发布管线） | v0.1.0（2026-08-20） | `plan/archive/0.1.0/` |
| 0.0.1 | 最小单轮诊断闭环（beta1–3） | — | `plan/archive/0.0.1/` |

## 工作单元

| # | 模块 | 状态 | 备注 |
| - | --- | --- | --- |
| 1 | npm 包源 + `.github/workflows/npm-publish.yml` publish 门禁 | ⏳ | 主包 `@aruing/aruing` + 三平台精确版本 optionalDeps，复用官方 Release 资产与 checksums；安装校验、npm 更新保护及先平台后主包发布已实现，未改 `internal/`；汇总集成与剩余验收待完成 |
| 2 | `internal/tui` 命令面板 + `cmd` chat 交互接线 | 未开始 | pi 式：`/` 即菜单、随输入过滤、键位导航；双模式一致；纯展示层（#20）；功能头 draft 在笔记仓 `plan/0.1.6/inline-ux/` |

产品路径（`run`/`chat`）须 LLM 齐全；单元测试用 `agenttest`/`toolstest` 假实现，不依赖 CLI 假闭环。

## 下一步

**下一项**：v0.1.5 发版收口——release/sync 快照 → PR to production → tag。npm 剩余验收（macOS Intel / Linux x64 真实 registry 安装、真实 LLM 与集群诊断、四包自动发布认证）归 owner Go-Go-Zeppelin，记录见笔记仓功能头活段。

**待认领**：`/` 行内命令体验（0.1.6，功能头 draft 已立笔记仓；认领时更新 owner）。

**版本收口清单**（0.1.6 功能全绿后）：`make check` + `make smoke-all`（12 场景）集成验证 → README 双语 Current stage / Install 收尾同改 → `v0.1.6` tag。

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
