---
name: aruing-pr-description
description: Use when creating or updating a pull request for this repository. Triggered by tasks like "open a PR", "create PR", "submit this branch for review", "写 PR 描述", "提 PR".
---

# PR Description

## 作用范围

为本仓库当前分支生成结构化 PR 描述。skill 触发后收集 git 事实、判定类型与架构影响、填充模板；不替作者跑 CI、不替作者改代码、不替作者合并

## 触发后的工作流

### 1. 收集事实（通过 git 命令，不靠猜）

```bash
git log main..HEAD --oneline                                         # 本分支 commit 列表
git diff main...HEAD --stat                                          # 改动文件 + 行数
git diff main...HEAD -- internal/core/ internal/agent/orchestrator.go internal/tools/tool.go   # 是否动核心
git diff main...HEAD -- docs/architecture.md docs/project-state.md docs/skills/                # 是否动文档
```

不要把命令输出原样塞进 PR 描述。这些是判定依据，不是描述内容

### 2. 判定类型（开放标签，可多个）

类型不限定枚举集合。按 commit message 前缀和实际改动推断，可用标签举例（不穷尽）：

- `feat`：新功能 / 新角色接入
- `fix`：bug 修复
- `refactor`：内部重构，不改外部行为
- `ci`：CI / workflow / Makefile
- `docs`：仅文档
- `chore`：依赖升级、format、杂项
- `test`：仅测试
- `perf`：性能优化

一个 PR 可以有多个类型，比如同时是 `feat` 和 `fix`，或 `refactor` 和 `docs`。按 PR 实际情况选，不要硬塞

### 3. 识别架构影响

以下任一条件成立时，`#### 4. 架构影响` 标"有"，并用一句话简述：

- 改了 `internal/core/*.go` 的 exported type 字段或新增结构
- 改了 `internal/agent/orchestrator.go` 角色接口签名
- 改了 `internal/tools/tool.go` 的 `Tool` / `Registry` / `Dispatcher` 接口
- 新增或替换诊断流上的角色（Parser / Resolver / Planner / Verifier / Reporter）
- 改了信任边界
- 改了硬约束

都不命中时填"无"

### 4. 识别破坏性变更

以下任一条件成立时，`#### 5. 破坏性变更` 标"有"，并简述影响范围：

- 删除或重命名 exported type / 函数 / 字段
- 改了 exported 函数签名
- 改了核心数据结构的 JSON 字段名（破坏向后兼容）
- 删除了既有工具或既有角色

都不命中时填"无"

### 5. 判定该同步哪些文档

按 `aruing-docs` §更新时机 的映射表判断本 PR 是否触发文档同步。在 `#### 6. 检查` 段如实反映：

- 触发了哪些文档更新（说明已做）
- 没触发任何文档更新（说明"不适用"）
- 不确定时显式标注，让 reviewer 确认

### 6. 填充模板

用下面的模板。所有标题必须是 `####` 且带递增序号索引（`1.` `2.` `3.`…，与模板一致），不允许 `#`、`##`、`###`

### 6.5 PR 前对抗自检（含新增/修改代码的 PR）

目标：把历史上 pr-agent 反复发现的两个族消灭在 push 前——每次 push 触发一轮全新评审，轮次是时间成本（历史教训：#119 五轮评审里族内兄弟洞排队出现）。纯文档 PR 可跳过。

对 PR 内新增/修改的代码逐项过：

1. **导出输入面枚举**：公开字段的结构体、导出函数参数、config 新字段——逐面自问「绕过构造器/直接构造/零值」时消费点行为是什么？崩溃与静默错都不可接受（报错或归默认）；能用私有字段 + 构造器集中校验就不留公开可变面
2. **数值退化清单**：每个数值输入过 NaN / ±Inf / 0 / 负 / 越语义域（概率 >1、阈值方向反）/ 亚正常 / 溢出路径（exp、连乘、除法）——防护统一 `!(v > 0) || IsInf(v, 0)` + 语义域约束；比较一律对数域优先
3. **静默语义污染自问**：这个非法值会让某条分支恒真/恒假吗（静默开启/关闭某个出口、开关、路径）？比 NaN 更隐蔽
4. **钉板**：上述边界照 `aruing-test-guidelines` 写成回归测试（克制：只钉真实会遇到的边界，不穷举）

pr-agent 评论处理见 `aruing-pr-agent-triage`（实测裁决 + 按类修复 + 批量单推）。

### 6.6 PR 规模自检（参考阈值，非硬限）

push 前看一眼规模（`docs/` 同步与生成物不计入）：

```bash
git diff <base>...HEAD --stat -- internal/ cmd/ | tail -1
```

参考阈值：~100 行最好；~300 行单一逻辑可接受；**>500 行必须主动评估**。超限不是自动拆分信号——依赖升级、机械重构、生成物属合理大批量。评估间：内容是否合理、是否清晰、是否会导致 pr-agent 上下文爆炸或审核者无从下手：

- 不合理 → 拆分：按可独立编译验证的层 / 切片建栈式 PR（如 agent → store/session → cmd 装配），不是机械按行数剁碎
- 合理 → 在 PR 描述「检查」段显式说明理由，由维护者裁决；必要时维护者手动触发 `aruing-pr-review` 兜底审核

步骤设计文档预估超限时，应在设计内预排 PR 切分（见笔记仓《工作流约定》步骤节）。

### 6.7 标题与用语规范（公开仓内容边界）

PR 标题与描述面向公开仓的所有读者（贡献者、AI 工具、外部访客），不是笔记仓的内部通信。遵守《工作流约定》「仓库内容边界」：笔记仓代称与排期坐标不进公开仓，需要交代时用工程语言展开，或指向 note 仓路径

**标题**：

- **统一英文**：`<type>: <english subject>`。`feat:` / `fix:` 等前缀本身即英文，标题属对外面，与「README 默认英文 + docs 中文」同一分工；描述正文仍中文为主
- 祈使语气、小写开头（专有名词与标识符除外）、句末无句号；整条 ≤ 72 字符，超长先砍修饰词，不砍宾语
- 只写做了什么，不写内部步骤编号与里程碑坐标括号注记（如 `（0.1.4 persistence 步骤 2）`）

**用语黑名单**（标题与描述正文共用，出现即视为未过闸门）：

| 类别 | 禁用示例 | 正确做法 |
| --- | --- | --- |
| 步骤 / 功能目录坐标 | `0.1.4 步骤 N`、`S1–S4`、`腿 A`、`步骤 3b` | 移到「关联」段 note 仓 plan 路径，标题与正文不出现 |
| 实验臂代称 | `D1` / `D2` / `B3`、`统一实验批` | 展开为工程语言（last-N baseline arm、ReAct baseline arm…）；config 枚举值本身可写（`agent.memory.method=d1-last-n`），代称不行 |
| 符号速记 | `①层 ②层 ③层`、`λ₁/λ₂`、`L0/L1/L2` | 展开为实际概念（rubric 抽样判分层、确定性寻址 / LLM 兜底定位…） |
| 流程黑话 | `钉板`、`分诊`、`断崖`、`收线`、`兜底`、`提级`、`降位`、`随版交付` | 用普通工程语言改写（固定为回归测试 / 评审意见分类处置 / 能力断崖式下跌…） |

**允许**：产品事实（config 键与枚举值、模块路径、公开文档概念如 `evidence.read` / spool）；note 仓**路径**引用（放「关联」段），不内联其内容与代称

**正反例**（取自历史 PR 改写）：

| ❌ 历史 | ✅ 改写 |
| --- | --- |
| `feat: 挂起快照持久化——跨进程 Resume + DiskStore.Close（0.1.4 persistence 步骤 2）` | `feat: persist suspension snapshots across restarts` |
| `feat: 超巨输出 spill 腿 A——捕获留存（0.1.4 persistence 步骤 3a）` | `feat: spill oversized tool output to disk` |
| `feat: tier-aware 组装器与记忆方法开关（0.1.3 步骤 2）` | `feat: tier-aware history assembly with method switch` |
| `feat: B3 ReAct 对比臂 + 决策轨迹插桩（0.1.2 步骤 5，统一实验批前置）` | `feat: add ReAct baseline arm and decision traces` |

### 7. 创建 PR

创建前先过本地闸门：

```bash
make lint fmt-check   # 不绿不上 PR：顺手修掉自己分支引入的 lint/格式问题后再继续
```

lint 不绿时停下修（只修本分支引入的问题，不做全仓顺手重构），然后才把填充好的模板作为 `--body` 传给 `gh pr create`，并自动打 assignee 与 label：

```bash
gh pr create --base main --head <当前分支名> \
  --title "<英文 subject，按 §6.7 标题与用语规范>" \
  --body "<模板内容>" \
  --assignee @me \
  --label "<按第 8 步映射，每个 label 一个 --label 标志>"
```

- `--title` 按 §6.7 生成英文 subject；分支 commit 主题不符合规范时改写，不照抄
- `--body` 用第 6 步填好的模板原文
- 不要在 `--body` 里转义 `####`（GitHub 会正常渲染 markdown）
- 如果分支还没 push，先 `git push -u origin <分支名>`
- PR 创建后向用户返回 PR URL

### 8. 打 assignee 与 label

**assignee**：固定 `--assignee @me`（创建者即维护者本人，不硬编码用户名）。

**label 映射**（类型 → 仓库现有 label；与历史 PR #74–#78 的实际打法一致）：

| 第 2 步判定类型 | label |
| --- | --- |
| `feat` | `enhancement` |
| `docs` | `documentation` |
| `test` | `test` |
| `fix` | `bug` |
| `refactor` | `refactor` |
| 改动含 `docs/skills/`（新建 / 修改 skill） | 追加 `skill` |
| `ci` / `chore` / `perf` 等其他类型 | 不映射，不硬造新 label |

规则：

- 多类型 PR 取并集（如 `feat`+`docs` → `enhancement documentation`，与 #77/#78 一致）
- **只用仓库已存在的 label**，不新建；拿不准时先 `gh label list` 核对
- 多个 label 用**多个 `--label` 标志或逗号分隔**，不得空格分隔（会被当成单个 label 名导致创建失败）
- `Review effort N/5` 由 pr-agent 自动打，本 skill 不管
- 若 label 拼错导致 `gh` 报错，去掉 `--label` 重试并在 PR 创建后手动补，不要卡住流程
- **创建后必须核对**：`gh pr view <N> --json labels` 确认 label 实际落上——创建命令可能静默丢 label（如仓库迁移重定向）；未落上时用 REST 补：`gh api repos/<owner>/<repo>/issues/<N>/labels -f 'labels[]=<label>'`
- **向已有 PR 追加提交前必须核验 PR 仍 OPEN**：`gh pr view <N> --json state`；已 MERGED/CLOSED 的 PR 追加提交不会生效（无新 PR 包含它），须基于 main 另开新分支新 PR（历史案例：PR #89 合并后向其分支追加截图 commit，内容搁置在分支上未进 main）

## 模板

```markdown
#### 1. 类型

<开放标签，可多个>

#### 2. 工作内容

<一句话，不超过 2 行>

#### 3. 改动范围

<简短描述改了哪几块，不列具体文件路径；reviewer 直接看 github diff>

#### 4. 架构影响

<无 / 有：简述>

#### 5. 破坏性变更

<无 / 有：简述>

#### 6. 检查

- [ ] 已按 `aruing-docs` §更新时机 同步相关文档（如适用，说明哪些；不适用则写"不适用"）

#### 7. 关联

- 工作单元：<#编号 或 "计划外">
- note 仓 plan：<路径 或 无；内部步骤坐标 / 设计出处落这里，不进标题与正文>
- 预留问题：<P/L/C/S-x 或 无>
- 相关 PR：<#编号 或 无>
```

## 约束

- 所有段标题必须 `####` 且带递增序号索引（`1.` `2.` `3.`…，与模板一致），不允许 `#`、`##`、`###`
- "工作内容"一句话，不展开细节（细节在 commit message）
- "改动范围"简短描述，不列具体文件路径
- "架构影响"和"破坏性变更"必填，"无"也要写明
- "改动范围"不超过 6 条 bullet
- **PR 标题统一英文**，遵 §6.7；标题与描述正文不得出现笔记仓内部代称与步骤坐标（黑名单见 §6.7），内部追溯放「关联」段 note 仓 plan 路径
- 不复制笔记仓内容（笔记仓链接放"关联"即可）
- 不替作者勾选 checkbox，由作者自己确认后勾
- 描述正文中文为主，技术术语保留英文（例外：PR 标题统一英文，见 §6.7）

## 不做的事

- 不替作者跑 `make test` / CI（已在 GitHub Actions 配置，不重复）
- 不替作者改代码
- 不替作者合并 PR
- 不替代 pr-agent 评审（本 skill 是作者自检，pr-agent 是 CI 外部评审，互补）
- 不生成 changelog（如需 changelog 由专门工具处理）

## 与 pr-agent 的关系

- pr-agent 是**外部评审**，跑在 CI 里，用 LLM 自由生成 describe / review / improve
- 本 skill 是**作者自检**，生成模板化、约束化的描述并创建 PR
- 两者互补：本 skill 保证关键信息（类型 / 架构影响 / 破坏性变更）一定暴露；pr-agent 看到这些信息后能做更准的评审
- 如果 pr-agent 的 `/describe` 与本 skill 输出冲突，以本 skill 输出为准（作者意图优先）