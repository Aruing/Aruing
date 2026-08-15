---
name: aruing-milestone-close
description: Use when closing a completed milestone (完成标志全绿、PR 已合并、维护者确认关闭). Triggered by tasks like "close beta16", "关里程碑", "归档里程碑", "收尾", "关闭版本".
---

# Milestone Close

## 作用范围

执行里程碑关闭的**机械化收尾清单**：状态头更新、目录归档、文档四方同步、历程起草。不替维护者判定完成标志是否全绿、不替维护者决定是否蒸馏历程、不替维护者改冻结的设计内容。

## 前置闸门（全部满足才触发本 skill）

1. 里程碑完成标志全绿（对照 `plan/<里程碑>/<里程碑>.md` 头文件）
2. 该里程碑全部 PR 已合并进 main
3. **维护者明确说了"关闭 / 归档"**（AI 不得主动触发关闭）

任一不满足时，向维护者报告缺什么，停手。

## 收尾清单（按序执行）

### 1. 收集事实（git 为准，不靠猜）

```bash
git log --oneline -10                     # 找该里程碑的 PR merge commit
ls plan/<里程碑>/                          # 步骤 plan 清单
```

记下：里程碑名、PR 号、最终 commit、关闭日期、交付的 arc（若有）。

### 2. 笔记仓 `arui-note/aruing/`

| 动作 | 规则 |
| --- | --- |
| 头文件状态头 → done | 只改状态行（含日期 + PR 号），正文不动 |
| 逐步骤核对状态头 | 每个 step plan 须已 `merged`（PR 号 + commit）；发现未 merged 的列出，问维护者 |
| 归档 | `mv plan/<里程碑>/ plan/archive/<里程碑>/`（头文件 + 步骤 plan 一起，不拆） |
| `快速恢复上下文.md` | 「当前整体状态」清单加一行（格式照抄上一行） |

### 3. 代码仓 `docs/project-state.md`

- 「当前阶段」加完成段（格式照抄上一条已归档里程碑）
- 「工作单元」表加行
- 「下一步」重开候选窗口：删旧下一项，候选清单按 arc 指针与残留缺口刷新
- 顶部「最后更新」日期改今天

### 4. 代码仓 `docs/architecture.md`

对照最后 PR 的改动检查模块职责表 / 编排事实段是否已同步。已同步则不动；有缺口按 `aruing-docs` §更新时机 补齐。

### 5. arc（living，仅当里程碑交付了某 arc 的步骤）

- arc 步骤表状态更新（done + PR 号）；缺口表若有变化同步
- **整条 arc 全部交付或废止** → `mv plan/arc/<主题>.md plan/archive/arc/`

### 6. 历程蒸馏（维护者定夺，AI 起草）

问维护者是否蒸馏。同意时：

- 读 `plan/archive/<里程碑>/` + `git log`，按工作流约定 §五结构起草（≤100 行，放 `历程/<大版本>/<主题>.md`；跨版本放 `历程/全程/` 并标注跨越版本）
- `历程/README.md` 索引加行
- **AI 只起草，维护者改定**；小里程碑可并入既有弧（在对应历程「留下的取舍」补注记）而不新开文件，由维护者选

### 7. 版本收尾（仅当关闭的是版本最后一个里程碑）

- 版本文档退役：`mv plan/version/<版本>.md plan/archive/version/`
- `project-state.md` 按新版本重写「当前阶段」

### 8. 汇报

向维护者列出本次改动的全部文件（两个仓库分开列），等确认后由维护者决定提交（笔记仓为本地私有仓，提交方式遵维护者指令；代码仓文档改动走 PR，描述遵循 `aruing-pr-description`）。

## 约束

- 已冻结的 step plan 正文**永不回头改**；状态头只允许 draft → merged（step）与定稿 → done（头文件）各一次
- 收尾只动文档与笔记仓目录，**不动核心代码**；发现代码问题另立 issue / 计划，不顺手改
- 文档写法遵循 `aruing-docs`；历程结构与蒸馏时机遵循工作流约定 §五
- 开发期间不写历程；本 skill 不在步骤交付时触发，只在关里程碑时触发

## 与其他 skill 的关系

| skill | 分工 |
| --- | --- |
| `aruing-docs` | 文档内容写法与更新时机映射的权威 |
| `aruing-pr-description` | 收尾产生的代码仓文档改动开 PR 时使用 |
