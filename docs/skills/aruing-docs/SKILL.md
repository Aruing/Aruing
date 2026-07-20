---
name: aruing-docs
description: Use when adding, updating, reviewing, or restructuring documentation files in this repository, including creating new skills. Triggered by tasks like "update README", "add architecture doc", "write project state", "where should this doc go", "create a new skill".
---

# Docs

## 作用范围

仓库内文档新增、修改、审查、重构时使用，包括 skill 自身的创建与修改。不覆盖代码注释规范（见 `aruing-code-comments`）和测试规范（见 `aruing-test-guidelines`）

## 核心原则：仓库 vs 笔记

仓库内文档只放**当前事实**，面向协作者、AI 工具、想了解项目的人。设计推理、讨论过程、备选方案、阶段计划、预留问题留在 `arui-note/aruing/` 笔记仓，由维护者个人保管

仓库文档不复制笔记内容，只链接到笔记作为深入入口

## 仓库内文档分工

| 路径 | 受众 | 内容 | 不放什么 |
| --- | --- | --- | --- |
| `README.md` | 所有人 | 项目定位、目标、核心数据流一行图、快速开始、文档入口链接 | 架构细节、阶段状态（链接到对应 docs） |
| `docs/README.md` | 人 | docs/ 与笔记仓的简短分工说明 | 完整规范（在 aruing-docs skill 内） |
| `docs/architecture.md` | 开发者 + AI | 架构事实快照：诊断流程图、模块职责表、核心数据结构字段一览、信任边界、数据关联、硬约束清单 | 设计推理、备选方案、历史变迁 |
| `docs/project-state.md` | AI + 维护者 | 当前阶段、工作单元状态表、已完成 PR、下一步、当前硬约束摘要、预留问题入口 | 详细预留问题表（在笔记仓） |
| `docs/skills/aruing-*/` | AI 工具 | 项目级 skill：触发条件、规则、模板 | 与代码无关的通用规范 |

## 各文档内容约束

### `README.md`

固定部分（按顺序）：

1. 项目一句话定位
2. 当前阶段（如 `0.0.1-beta2 / 真实闭环`）
3. 核心数据流一行图（Run → Query → Target → Hypothesis → Task → Evidence → Verdict → Report）
4. 快速开始（`make build` / `make test` / `aruing run ...`）
5. 关键约束摘要（3~5 条，链接到 `docs/architecture.md`）
6. 文档入口（链接到 `docs/` 和 `arui-note`）

长度：50~80 行。不放架构细节、不放阶段计划

### `docs/architecture.md`

固定部分（按顺序，使用 `##`）：

1. 诊断流程（mermaid 图 + 一句话说明）
2. 模块职责表（`internal/*` 每个包：负责什么 / 不负责什么）
3. 核心数据结构（Run / Query / Node / Edge / Target / Hypothesis / Task / Evidence / Verdict / Report / Factory 各字段职责）
4. 信任边界（用户输入 / 线索 / 已确认目标 / 待验证猜想 / 工具产出证据 / 基于证据的判断）
5. 数据关联（扁平 ID 关联，子实体不嵌套）
6. 硬约束清单（8~12 条，从 beta1 继承 + beta2 新增）

长度：100~200 行。只放当前事实，不放设计推理过程

### `docs/project-state.md`

固定部分（按顺序，使用 `##`）：

1. **当前阶段**：阶段名 + 一句话目标（如 "把假角色逐个换成真实现"）
2. **工作单元**：表格，列 `# / 模块 / 状态 / 备注`，状态用 ✅ / ⏳ / ❌ / 未开始
3. **已完成 PR**：列表，每条 `#编号 标题（commit）`
4. **下一步**：明确写下一个该做什么、为什么（不超过 5 行）
5. **当前硬约束摘要**：3~5 条，链接到 `docs/architecture.md` 硬约束段
6. **预留问题入口**：按类别（P/L/C/S）列出编号 + 一句话，详细表格链接到 `arui-note/aruing/plan/`

长度：50~80 行。每个 PR 合并时更新，是 AI 工具的 5 分钟对齐入口

### `docs/README.md`

保留为人类入口。简短说明 docs/ 与笔记仓的分工即可

## Skill 自身规范

创建或修改项目级 skill 时必须遵守：

| 项 | 约束 |
| --- | --- |
| 位置 | `docs/skills/aruing-<name>/` |
| 命名 | 必须 `aruing-` 前缀 |
| 结构 | `SKILL.md`（frontmatter + 规则正文）+ `agents/openai.yaml`（metadata） |
| 安装 | `make install-aruing-skills` 同步到 `.agents/skills/`，不要手动复制 |
| 不 ignore | `docs/skills/` 必须进版本控制，不要加 `.gitignore` |
| 不 ignore | `.agents/` 由 Makefile 管理，按 `AGENTS.md` 约束不提交 |

`SKILL.md` frontmatter 必须含：

```yaml
---
name: aruing-<name>
description: Use when <触发条件>. Triggered by <示例任务>.
---
```

`agents/openai.yaml` 必须含 `interface.display_name`、`interface.short_description`、`interface.default_prompt`、`policy.allow_implicit_invocation`

## 写法要求

- 仓库文档以中文为主，关键术语保留英文（如 `Run`、`Query`、`Evidence`）
- 只放当前事实，不放设计推理、讨论过程、备选方案
- AI 看的文档前 20 行必须能让工具抓住本质：项目是什么、当前阶段、下一步
- 表格 / 列表优先，避免长段落
- 硬约束用"必须 / 不得"，不用"应该 / 建议"
- 链接到笔记仓用相对路径（如 `../../arui-note/aruing/plan/`）或文字说明位置，不复制笔记内容

## 更新时机

| 触发 | 更新动作 |
| --- | --- |
| PR 改了架构 / 完成工作单元 | 更新 `docs/project-state.md` 工作单元表 + 已完成 PR 列表 |
| PR 改了 `internal/core/*.go` exported type 或 Orchestrator | 检查 `docs/architecture.md` 数据结构段或模块职责段 |
| 创建新 skill | 遵守本 skill 的 §Skill 自身规范 |
| 阶段切换（如 beta2 → beta3） | 重写 `docs/project-state.md`，更新 `README.md` 当前阶段段 |

更新文档与代码改动放在同一 PR 内，不分离