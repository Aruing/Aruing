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

以下任一条件成立时，`#### 架构影响` 标"有"，并用一句话简述：

- 改了 `internal/core/*.go` 的 exported type 字段或新增结构
- 改了 `internal/agent/orchestrator.go` 角色接口签名
- 改了 `internal/tools/tool.go` 的 `Tool` / `Registry` / `Dispatcher` 接口
- 新增或替换诊断流上的角色（Parser / Resolver / Planner / Verifier / Reporter）
- 改了信任边界
- 改了硬约束

都不命中时填"无"

### 4. 识别破坏性变更

以下任一条件成立时，`#### 破坏性变更` 标"有"，并简述影响范围：

- 删除或重命名 exported type / 函数 / 字段
- 改了 exported 函数签名
- 改了核心数据结构的 JSON 字段名（破坏向后兼容）
- 删除了既有工具或既有角色

都不命中时填"无"

### 5. 判定该同步哪些文档

按 `aruing-docs` §更新时机 的映射表判断本 PR 是否触发文档同步。在 `#### 检查` 段如实反映：

- 触发了哪些文档更新（说明已做）
- 没触发任何文档更新（说明"不适用"）
- 不确定时显式标注，让 reviewer 确认

### 6. 填充模板

用下面的模板。所有标题必须是 `####`，不允许 `#`、`##`、`###`

### 7. 创建 PR

把填充好的模板作为 `--body` 传给 `gh pr create`：

```bash
gh pr create --base main --head <当前分支名> --title "<commit message 主题或一句话概括>" --body "<模板内容>"
```

- `--title` 用本分支第一个 commit 的主题，或基于工作内容一句话改写
- `--body` 用第 6 步填好的模板原文
- 不要在 `--body` 里转义 `####`（GitHub 会正常渲染 markdown）
- 如果分支还没 push，先 `git push -u origin <分支名>`
- PR 创建后向用户返回 PR URL

## 模板

```markdown
#### 类型

<开放标签，可多个>

#### 工作内容

<一句话，不超过 2 行>

#### 改动范围

<简短描述改了哪几块，不列具体文件路径；reviewer 直接看 github diff>

#### 架构影响

<无 / 有：简述>

#### 破坏性变更

<无 / 有：简述>

#### 检查

- [ ] 已按 `aruing-docs` §更新时机 同步相关文档（如适用，说明哪些；不适用则写"不适用"）

#### 关联

- 工作单元：<#编号 或 "计划外">
- 预留问题：<P/L/C/S-x 或 无>
- 相关 PR：<#编号 或 无>
```

## 约束

- 所有标题必须 `####`，不允许 `#`、`##`、`###`
- "工作内容"一句话，不展开细节（细节在 commit message）
- "改动范围"简短描述，不列具体文件路径
- "架构影响"和"破坏性变更"必填，"无"也要写明
- "改动范围"不超过 6 条 bullet
- 不复制笔记仓内容（笔记仓链接放"关联"即可）
- 不替作者勾选 checkbox，由作者自己确认后勾
- 中文为主，技术术语保留英文

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