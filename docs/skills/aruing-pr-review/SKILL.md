---
name: aruing-pr-review
description: Use when manually triggering a fallback review of a pull request in this repository — pr-agent's context budget is exceeded or its review is insufficient, pr-agent is unavailable, an oversized PR needs chunked module-by-module review, or a feature-branch PR needs a merge gate. Triggered by tasks like "pr-agent 审不动了，手动审一下这个 PR", "兜底审核", "分块评审这个大 PR", "合并前把把关".
---

# PR 兜底审核（Aruing）

## 定位

pr-agent 之外的人工触发评审：**不是替代**，是兜底。适用场景：

- pr-agent 上下文超预算 / 评审明显不充分（大 PR 只扫了表层）
- pr-agent 不可用或结果异常
- 超大 PR（如步骤级跨层改动）的合并门禁
- 维护者显式要求「再审一遍」

pr-agent 评论的日常处理走 `aruing-pr-agent-triage`，不走本 skill。本 skill 产出**独立**评审结论并提交到 PR。

## 独立审核原则

- 每次审核以**当前** base、head、diff、实际文件和 PR Actions 为准，独立完成判断
- 历史 review、评论、审批状态只作低信任线索：旧 finding 在当前 head 已修复则不重提；仍存在则按当前影响重新定级，不照抄历史严重性
- PR 作者与 reviewer 同一标准；GitHub 不允许自审 APPROVE 时用 `COMMENT` 提交，正文仍写明实际结论

## 审核流程

1. 收集事实：

```bash
gh pr view <PR>
gh pr diff <PR>
gh pr view <PR> --json files,additions,deletions,baseRefName,headRefName
gh pr checks <PR>
```

PR 可能基于功能分支（如 `feat/0.1.4-*`）而非 main——范围完整性按**相对 base 的增量**判断。

2. **分块**：小 PR（参考 ≤300 行）整读；大 PR 按包/层分块逐块深审（如 agent → session → store → cmd 每块独立过下表），块间接缝（接口签名、装配点）单独过一遍。分块不降低标准，只是控制单次上下文。
3. 逐块对照「契约速查」判断适用范围，权威文档按需加载（`docs/architecture.md` / `docs/project-state.md` 是事实唯一家，本 skill 不复制其内容）。
4. 关键发现抽样验证实际文件，不只看 diff：`git show origin/<head>:<file>`。
5. 标严重性前先对照既有模式：遵循既有模式但有改进空间通常不是阻塞问题；破坏信任边界、Evidence 链、挂起/恢复语义才是高严重性。

## 契约速查（变更路径 → 权威对照）

| 变更路径 | 必须对照 |
| --- | --- |
| `internal/core/**` | architecture 硬约束全文（尤其 #1–#7 扁平关联 / #10 Factory 发号 / #18 不阉割）+ 信任边界段 |
| `internal/agent/**`（编排 / 角色 / Tower） | 硬约束 #15–#17（角色不私自多轮调 Tool、不另起执行通道）+ 2026-7-22 §4 禁止事项（笔记仓）+ 模块表「不负责」列 |
| `internal/tools/**` | 硬约束 #12–#14（Policy 授权、后端粒度注册、不经 shell）+ #19（Summary 投影 / Raw 不可变） |
| `internal/store/**` / `internal/session/**` | #18（全量留存 / 损坏明确失败）+ 扁平 ID 关联 + 单进程假设边界 |
| `cmd/aruing/**` | 装配不引入假闭环；config 键位不越界（`storage.*` / `tools.projection.*` / `agent.*` 归属） |
| `internal/agent/acquire/**` | 纯函数边界（不 import core/llm、不做语义判断）+ 数值域（对数域比较、NaN/Inf） |
| `*_test.go` | `aruing-test-guidelines`（断言克制 / helper 复用 / 命名） |
| 注释改动 | `aruing-code-comments` |
| 真集群验证主张 | `aruing-cluster-smoke`（要不要跑 smoke 的裁决口径） |

## 规模纪律（审核侧）

参考阈值：~100 行最好；~300 行单一逻辑可接受；**>500 行触发评估**（`docs/` 同步与生成物不计入）。超限不是自动阻塞——审核者判断：内容是否合理（依赖升级 / 机械重构 / 生成物属合理大批量）、是否清晰、是否导致评审无从下手。不合理 → **要求拆分**（按可独立编译验证的层/切片，栈式 PR）；合理 → 记录说明放行。作者侧的自检在 `aruing-pr-description` §6.6。

## 严重性

| 前缀 | 含义 | 作者需要 |
| --- | --- | --- |
| **阻塞:** | 阻塞合并 | 破坏信任边界（模型输出冒充 Evidence）、Verdict 无证据引用、静默丢数据 / last-N 截肢（#18）、角色私调工具（#16）、安全漏洞 |
| **必须修改:** | 合并前必须处理 | 硬约束违反但无数据损失、并发竞争、错误被吞、覆盖缺失、文档该同步未同步 |
| **建议:** | 建议处理 | 命名、局部抽象、测试增强、可维护性提升 |
| **细节:** | 次要问题 | 格式、微小风格（通常 lint 管） |
| **说明:** | 信息记录 | 不要求行动，记录上下文或后续风险 |

每条 finding 必须带显式前缀，不能用无前缀文本表示「必须修改」。

## 常见问题族（历史 pr-agent 蒸馏，逐块过一遍）

1. **锁边界**：镜像内存单锁语义的实现里，索引 map 的读是否在锁内（锁外读既是数据竞争也拦不住并发双写）——#145 评审族
2. **存储编号防御**：编号直接充当目录 / 文件名的写入口，是否拒绝路径成分（`../`、`/`、`.` 段）——#145 R4 族
3. **数值域**：每个新数值输入过 NaN / ±Inf / 0 / 负 / 越语义域；防护 `!(v > 0) || IsInf(v, 0)`；比较对数域优先；JSON `1e999` 溢出路径——#119 族
4. **config 段遗漏**：新增 config 字段是否进了 `LoadFile` 拷贝链（历史两例：TUI 段、Projection 段加了字段但文件路径从未生效）
5. **状态连续性**：挂起 / 恢复路径上，局部变量或 defer 是否会用局部状态**覆盖**累积状态（轨迹、澄清答复）——#134 R2 族
6. **flag / env 组合**：非法组合是前置报错还是静默忽略（静默忽略 = 假配置生效）——#138 族
7. **ctx 传播**：请求路径 context 是否丢成 `context.Background()`——#142 族
8. **shell 兼容**：脚本在 macOS bash 3.2 + `set -u` 下可跑（变量紧跟全角字符要花括号）——#133 族
9. **导出输入面**：公开结构体 / 导出函数参数，绕过构造器直接构造或零值时消费点行为是什么——崩溃与静默错都不可接受（§6.5 同款自问）

## 验证要求

本地门禁（结论必须如实记录跑没跑）：

```bash
make check        # test-ci + vet + lint + fmt + tidy + vuln
```

按变更补充：动编排 / 工具接线时对照 `aruing-cluster-smoke` 判断要不要 kind 场景 smoke（裁决口径在那边）。CI 失败以 CI 日志为准。

## 提交审核

优先把可行动 finding 提交为 **PR diff 上的 inline review comment**（作者可在网页逐条 Resolve）；顶层 body 只放结论与无法挂行的发现，不重复 inline。

- 无 finding 或只需总评：`gh pr review --approve/--comment --body-file`
- 有可定位 finding：`gh api repos/{owner}/{repo}/pulls/<PR>/reviews --input /tmp/pr-review.json` 批量提交，finding 进 `comments[]`
- 有阻塞 / 必须修改：`event` 用 `REQUEST_CHANGES`；只有建议 / 细节 / 说明用 `COMMENT`；确认通过且无可行动 finding 才 `APPROVE`

inline 定位规则：

```bash
gh pr diff <PR> --patch --color=never   # 先确认目标行在 diff 中
```

- 新增 / 修改后代码：`side: "RIGHT"` + 新文件行号；删除导致的问题：`side: "LEFT"` + 旧行号
- 只评论 diff 中存在的行；目标行不在 diff 中 → 放顶层 body「未挂行发现」
- 一条 comment 一个独立 finding，以严重性前缀开头，说明问题 / 影响 / 建议

JSON 结构：

```json
{
  "event": "REQUEST_CHANGES",
  "body": "## 审核结论: 要求修改 / 通过 / 评论\n\n## 本次 PR 解决的问题\n<一句话>\n\n## 审核范围\n- [ ] core 实体与硬约束: <是/否 - 详情>\n- [ ] 编排 / 角色 / Tower（#15–#17）: <是/否 - 详情>\n- [ ] 工具与投影（#12–#14/#19）: <是/否 - 详情>\n- [ ] 存储 / 会话 / 持久化（#18）: <是/否 - 详情>\n- [ ] 装配 / config / CLI: <是/否 - 详情>\n- [ ] 测试与验证: <是/否 - 详情>\n- [ ] 文档同步: <是/否 - 详情>\n\n## 未挂行发现\n<没有则写：无>\n\n## 验证\n- [ ] make check\n- [ ] 其他专项验证：<命令或不适用原因>",
  "comments": [
    {
      "path": "internal/path/file.go",
      "line": 42,
      "side": "RIGHT",
      "body": "**必须修改:** <问题>。\n\n影响：<...>。\n\n建议：<...>。"
    }
  ]
}
```

## 完成验证

- [ ] 所有阻塞 / 必须修改发现已列出且可定位
- [ ] 适用范围维度已逐项确认（大 PR 每块都过，不是抽一块）
- [ ] `make check` 已通过，或明确说明无法运行的原因
- [ ] 结论已通过 Reviews API 或 `gh pr review` 提交；可定位 finding 已 inline，其余进顶层 body
- [ ] 若要求拆分：拆分方案按可独立编译验证的层 / 切片给出，不含机械行数剁碎
