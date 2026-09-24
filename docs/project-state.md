# 项目当前状态

> 最后更新：2026-09-24（结构收敛：历史版本块压缩为「版本履历」一行一版，工作单元表只留当前版本，规范见 `docs/skills/aruing-docs`；beta21 收尾观察迁至笔记仓归档）

## 当前阶段

**`0.2.0` / 完备不脆弱的证据型诊断助手：🚧 进行中**（远景 2026-08-21 定稿，笔记 `plan/version/0.2.0.md`；定位与排序依据在笔记仓）。对标 codex / opencode 的工程完备度（不中断、不断崖、不截断）。版本节奏：小版本递增，每个 0.1.x = 一个可用的正式实现，完成即打 tag 发布；0.2.0 = 收口版本（含全部增量）。三创新点（代表性投影 / 主动取证决策 / 分层记忆）已随 0.1.1–0.1.3 全部交付：形式化 + 实现 + 同装置对比数据齐备，实验数据归档笔记仓 `gproject/`（维护者本地）。

**`0.1.4`（产品完备批：持久化 + 流式 + map-reduce）🚧 进行中**（2026-09-10 立项，plan 见笔记 `plan/0.1.4/`）：

| 功能 | 状态 | 摘要 |
| --- | --- | --- |
| persistence | ✅ 关闭（2026-09-18） | 磁盘存储地基 / 挂起快照跨进程 Resume / 超巨输出 spill 双写 + 盘读翻页 / `aruing sessions` 会话发现（#145 #146 #149 #150 #151；集成分支经 #157 回归 main）；专项真集群冒烟通过 |
| map-reduce | ✅ 关闭（2026-09-24） | 大表全覆盖两遍分片投影（#155 #158 #159 #161）；完成标志三绿：真场景真 LLM 不漏 / bench 机械对照断崖 vs 平坦 / 默认路径逐字节不变；bigtable-fleet 场景进例行 smoke-all |
| streaming | 🚧 收线中 | 随版交付已裁决（2026-09-24）；S1–S4 在分支 `feature/stream-out`（#148 #152 #154 #156）未进 main，待集成 PR 回归 + 功能头三标志验证（全链逐字渲染 / 中断无半截消息 / 真 LLM 正负路径 smoke） |

版本级完成标志：三功能全关 + `make check` / `make smoke-all` 场景全绿 → 打 tag `v0.1.4` 发布（production 流程）。

## 版本履历

已交付版本一行一条。里程碑细节见笔记仓 `plan/archive/`，发版说明见 GitHub Releases；PR 与 commit 历史以 `git log` 为权威，本文件不维护历史清单。

| 版本 | 主题 | tag | 归档（笔记仓） |
| --- | --- | --- | --- |
| 0.1.3 | 分层记忆组装（信任分层记忆 + 按需回灌）；0.1.2 主动取证决策随版交付 | v0.1.3（2026-09-05） | `plan/archive/0.2.0/{0.1.2,0.1.3}/` |
| 0.1.1 | 代表性投影升级 + 评测基建 | v0.1.1（2026-08-29） | `plan/archive/0.2.0/0.1.1/` |
| 0.1.0 | 可追问的诊断助手（beta1–22：Session + Tower、工具输出导航 L0–L3、TUI、发布管线） | v0.1.0（2026-08-20） | `plan/archive/0.1.0/` |
| 0.0.1 | 最小单轮诊断闭环（beta1–3） | — | `plan/archive/0.0.1/` |

## 工作单元（0.1.4）

| # | 模块 | 状态 | 备注 |
| - | --- | --- | --- |
| 0.1.4-1 | 磁盘存储地基 | ✅ | #145 `DiskStore`/`DiskRunLedger` per-session 目录 + `storage.data_dir` |
| 0.1.4-2 | 挂起快照持久化 | ✅ | #146 跨进程 Resume + `DiskStore.Close` |
| 0.1.4-3a | 超巨输出 spill·捕获留存 | ✅ | #149 stdout 双写盘上原带 |
| 0.1.4-3b | 超巨输出 spill·盘读翻页 | ✅ | #150 `SliceSpool` + `evidence.read` 盘读路径 |
| 0.1.4-4 | 会话发现 | ✅ | #151 `aruing sessions` 只读列表 |
| map-reduce-1 | map-reduce 纯函数核心 | ✅ | #155 两遍分片 + 溢出标注 |
| map-reduce-2 | map-reduce 教学面接线 | ✅ | #158 Spec / tower prompt / example.yaml |
| map-reduce-3 | map-reduce 对照装置 | ✅ | #159 rich-value fleet 生成 + 三档判分 |
| map-reduce-4 | map-reduce 真场景 smoke | ✅ | #161 bigtable-fleet + chat-env 注入 |
| streaming 集成 | 流式响应收线 | ⏳ | `feature/stream-out`（#148 #152 #154 #156）→ 集成 PR + 三标志验证 |
| 收尾 | 版本级集成验证 + 发布 | ⏳ | `make check` + `make smoke-all` 全绿 → tag `v0.1.4` |

产品路径（`run`/`chat`）须 LLM 齐全；单元测试用 `agenttest`/`toolstest` 假实现，不依赖 CLI 假闭环。

## 下一步

**下一项**：**0.1.4 收尾**：persistence 与 map-reduce 已关闭（见上表），剩余三件——① streaming 集成收线（随版交付已裁决 2026-09-24；分支 `feature/stream-out`，S1–S4 已并入 #148/#152/#154/#156，未进 main）：集成 PR 回归 main + 按功能头三标志验证关闭（S1–S4 全链逐字渲染 / 中断无半截消息 / 真 LLM 正负路径 smoke）；② 版本级集成验证 `make check` + `make smoke-all` 场景全绿（含新场景 bigtable-fleet，chat-env 自动走 map-reduce 臂；裁决 2026-09-18：smoke-all 不再作欠账项）；③ 打 tag `v0.1.4` 发布（production 流程）。

**候选方向**（远景与排序依据见笔记 `plan/version/0.2.0.md`；遗留清单见笔记 `plan/archive/0.2.0/0.1.3/2026-8-31-open-issues.md`；0.1.4 已立项三项不再列此处）：

1. **实验扩点**：断崖曲线 100/200 轮（20/50 两点不可判）；synthesis 类探针短板的产品侧跟进（排序依据见笔记仓）
2. **benchmark 升格**：kind 场景 harness → 可复现评测基准（0.1.1 评测基建之上扩场景）
3. **产品完备（降位）**：npm 平台子包（beta22 遗留）/ TUI L4 / `/` 运行时命令（流式已提级 0.1.4）

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
