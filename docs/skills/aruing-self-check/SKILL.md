---
name: aruing-self-check
description: Use when asked to run a full self-check of the repo (全量自检), verify all tooling works, or smoke all kind scenarios. Triggered by tasks like "self-check", "全量自检", "把所有测试都跑一遍", "检查工具是否正常", and by milestone close when tools/agent/tui/orchestration code changed.
---

# Self-Check

## 作用范围

一键全量自检的执行与判定纪律：跑 `make self-check`（静态链 + 全场景真集群 smoke），逐场景对照 `expect.md` 判定，产出汇总报告。不替维护者改产品代码；失败只报告 + 建议；smoke 非 CI 必绿。

## 触发条件（任一）

1. 维护者明确要求：「全量自检 / self-check / 把所有检查跑一遍 / 检查工具是否正常」
2. **里程碑收尾时**（被 `aruing-milestone-close` 引用）：本里程碑动过 `internal/tools/`、`internal/agent/`、`internal/tui/` 或编排相关代码 → 关闭前须跑
3. 大重构 / 依赖升级（如 toolchain、gonum 类）合并后

不触发：纯文档 PR、只动 `docs/`、`scenarios/` 提示词微调——`make check` 足够。

## 工作流

### 1. 静态链

```bash
make check    # tidy-check build test-ci fmt-check vet lint vuln
```

失败则停：逐条列出失败项与原因，不进入 smoke（二进制可能已过期）。

### 2. 全场景真集群 smoke

```bash
make smoke-all
```

- 前置体检失败（缺 Docker/kind/kubectl/LLM 配置）→ 报告缺什么，等维护者补，不硬跑
- 全部场景都跑（严格校验）；单场景失败不中断，脚本末尾有汇总表
- 过程留痕：`scenarios/.smoke/<name>.log`（gitignore）

### 3. 内容判定（对照 expect.md）

脚本只验证「执行成功」，**内容是否符合预期须逐场景判定**：

- 读 `scenarios/<name>/expect.md` 的「应出现 / 不应出现」
- 读 `scenarios/.smoke/<name>.log` 中 chat 输出
- 每场景给判定：`pass` / `attention`（含理由）；**不做自动评分，不做逐字匹配**（beta10 哲学：LLM 措辞不要求逐字）
- 判定不了（如输出被截断）标 `attention` 并说明缺什么

### 4. 汇总报告

向维护者交一份结构化报告：

```
## self-check 报告 <日期>
- make check: <通过 / 失败项+原因>
- smoke-all 汇总表: <场景 × up/chat/down>
- 内容判定: <每场景 pass/attention + 理由>
- 需要人工看的点: <列出>
- 建议: <如有失败，给出修复方向；不顺手改>
```

## 约束

- 失败**只报告与建议**，不顺手改代码（修复走正常 step plan / 计划外小 PR）
- smoke 依赖本地 Docker/kind/LLM，**不是 CI 必绿项**；CI 只含 `make check`
- 判定基准只有 `expect.md`；expect 本身不合理时在报告中提出，由维护者决定是否改场景
- 与第 3 项（真集群测试纪律）衔接：改了代码是否要改场景/提示词 → 见 `aruing-cluster-smoke`（若已建）；未建前在本报告「建议」段提示
