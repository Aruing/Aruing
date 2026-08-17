---
name: aruing-retrospective
description: Use when asked to scan for dead/garbage code, audit code quality, check test coverage status, verify compliance with project test/comment guidelines, review structural health (encapsulation/package splits), or hunt change-narration leakage in evergreen docs. Triggered by tasks like "扫一下代码卫生", "质量审计", "死代码检查", "覆盖率怎么样", "该不该拆这个包", "文档怎么在讲历史", and at milestone close after large code changes.
---

# Retrospective

## 作用范围

代码质量反思的两剖面审计：**卫生**（该删的：死代码/未用导出/过时占位误留）与**守规**（该改的：覆盖率缺口/规范违背/结构性问题）。产出报告，不直接改代码。

## 触发条件（任一）

1. 维护者明确要求（「扫一下卫生 / 质量审计 / 死代码 / 覆盖率」）
2. 关里程碑且本里程碑动过大面积代码（`aruing-milestone-close` 可选钩子）
3. 大重构 / 批量依赖升级合并后

不做定时。

## 工作流

### 剖面 1：卫生扫描（该删的）

```bash
go run golang.org/x/tools/cmd/deadcode@latest ./...   # 可达性分析（未安装时 go install）
make lint                                              # golangci-lint 已含 unused
```

对每条候选**逐条裁决**，分四类：

| 分类 | 判据 |
| --- | --- |
| 垃圾（建议删） | 无引用、非占位、无规划关联 |
| 占位-保留 | 与 `docs/architecture.md` 模块职责表「占位」标注或 project-state 计划对应（**#15：占位不得当死代码清**） |
| 误报 | 工具可达性局限（反射/接口实现/测试专用导出） |
| 待定 | 拿不准 → 报告标出让维护者定 |

纪律：删 exported 符号前查引用（`gopls` references 或 grep）；测试包专用导出（agenttest/toolstest）不算死代码。

### 剖面 2：守规审计（该改的）

1. **覆盖率现状**（只报告，不设门禁——门禁由维护者看现状后定）：
   ```bash
   go test -cover ./...
   ```
   按包列出现状 + 明显缺口（哪些关键路径没测试），首次审计附基线建议
2. **规范抽查**：按 `aruing-test-guidelines`（断言是否证明关键行为、helper 重复度）与 `aruing-code-comments`（注释规范）抽查近期新增/修改的测试与注释——**抽查而非全读**（每包挑代表性文件）
3. **结构性嗅探**：对照 `docs/architecture.md` 模块职责表读实际代码，找「该封装未封装 / 该拆未拆 / 职责越界」。主观性最强：建议单列「结构性建议」段，**每条标置信度（高/中/低）**，只是候选
4. **文档时态嗅探**（常青文档讲历史而非现状）：扫 `README*` / `docs/*.md` / 代码注释中的变更叙事——「以前是 X，现在是 Y」「betaN 起」「本次 PR 改了」「评审时认为」这类需要作者语境才看得懂的话。常青文档只放当前事实（`aruing-docs`）；发现即列偏离项交维护者裁决。**过度修剪陷阱**：改写不得丢事实——「已修复 X，因为 Y」改写为现在时反事实「无 X 时会 Y」，不是删掉；被硬约束 / 信任边界钉住的事实一律保留

### 报告

固定格式，交维护者：

```
## 回顾审计报告 <日期>（范围：全仓 | <包>）
### 卫生
- 垃圾（建议删）：<清单，每条附引用核查结果>
- 占位-保留：<清单，对应规划>
- 误报/待定：<清单+理由>
### 守规
- 覆盖率现状：按包 <表>；缺口 <关键路径>
- 规范抽查：符合/偏离项
- 文档时态偏离项：<清单，每条附建议改写>
- 结构性建议：<每条 + 置信度>
### 建议的后续 PR
- <按工作量排序的候选清单>
```

首次全仓审计的报告落笔记仓 `plan/<里程碑>/`；后续按需。

## 约束

- **只报告不动手**：任何清理/重构走独立小 PR（维护者批准后）
- 不设覆盖率门禁（除非维护者明确定过基线）
- 裁决基准只有三份常青文档（architecture / project-state / 两 skill）+ git log，不靠记忆
- 结构性建议不与硬约束冲突（拆分建议不得违背扁平 RunID / 模块职责表现状）
