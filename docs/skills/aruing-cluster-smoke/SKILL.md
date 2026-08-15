---
name: aruing-cluster-smoke
description: Use when deciding whether/how to run kind scenario smoke tests, adding or modifying scenarios/cases, or judging if code changes require scenario updates. Triggered by tasks like "要不要跑 smoke", "加个场景/case", "改了 X 场景要不要跟着改", "场景配置审查".
---

# Cluster Smoke

## 作用范围

真集群场景测试的纪律层：何时跑、跑哪些、改代码后场景/case 要不要跟着改、何时新增 case、case 编排与污染纪律、场景配置审查节奏。执行入口在 `aruing-self-check`（跑与判定）与 `make smoke-all`（脚本）；本 skill 负责「决策」。

## 何时跑（决策表）

| 触发 | 跑什么 |
| --- | --- |
| 合入动 `internal/tools/` `internal/agent/` `internal/tui/` 编排的 PR 前 | 全量 `make smoke-all` |
| 新增 / 修改场景或 case、改 prompts/expect | 只跑受影响场景：`bash scripts/smoke-all.sh <name>` |
| 改 tower prompt / Resolver / 挂起恢复链 | 全量（跨场景行为风险） |
| 纯文档 / `docs/` / 依赖小升级 | 不跑，`make check` 足够 |
| 关版本 / 里程碑收尾（动过上述模块） | 全量 + 场景配置审查（见下） |

## 改代码 → 场景要不要改（映射表）

| 动了什么 | 检查什么 |
| --- | --- |
| 工具输出格式 / Summary / Slicer | summary 相关场景（log-time-window、crashloop）的 expect 是否仍描述准确 |
| 挂起 / 恢复（clarify）| 歧义类 case（same-name-multi-ns 系）是否仍触发预期路径 |
| tower prompt / 工具教学文案 | 全场景抽查 1 个 + 受影响能力对应场景 |
| 新工具注册 | 该工具可被发现的场景（api-resources 可见性）；必要时新增 case |
| k8s argv / Policy | 全场景（执行层风险） |
| 纯 internal 重构、零行为变化 | 不改场景；跑受影响场景确认 |

检查后结论二选一：场景仍准确（不改）/ 场景已失真（同 PR 内更新 expect 或 prompts，并在 PR 描述说明）。

## 何时新增 case（而非新场景）

- 新能力落地且无场景覆盖（例：investigate clarify）
- 与既有场景**同根因、同基底**的新表现形态（不值得新起 kind）
- 需要多轮对话链路（挂起-回复、追问深解）——case 内多 prompt 同 session

新场景（新 kind）仅当：需要全新的故障形态/资源拓扑，无法在既有基底上表达。

## case 编排与污染纪律

- **顺序 = 现场演进**：case 按目录名字典序执行，`apply/` 只追加不回滚——命名须让现场从干净到复杂（`01-basic` → `02-inject-fault` → …）；破坏性/叠加性 case 排后
- **故障隔离**：同集群多 case 的故障放不同 namespace；prompt 显式带 ns
- **expect 容忍度**：各 case 的 expect 容忍他 case 故障「可见但非主因」，但不容忍其成为主结论
- **apply/ 纪律**：只追加显式可追溯的清单文件（`*.yaml`）；不写脚本；fresh-up（smoke-all 每次拆重建）保证重启后严格 = manifests + 本轮已跑 case 的 apply
- **迁移规则**：老场景加 `cases/` 时，顶层默认 case 应迁入为 `cases/01-<名>/`（保留原路径为新 case），顶层 prompts/expect 删除——`cases/` 存在即顶层不执行，留着是误导

## 场景配置审查（关版本时）

每关版本扫一遍 `scenarios/`：资源量是否合理、故障注入方式是否仍代表真实故障形态、有无场景已被产品演进淘汰（expect 长期失真）。结论进 self-check 报告；调整走普通 PR。

## 边界

- 不自动评分、不做 golden（beta10 哲学；判定见 `aruing-self-check`）
- 不替维护者决定「这次改动要不要跑」之外的事——拿不准时列出依据问维护者
