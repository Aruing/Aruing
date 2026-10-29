# 架构图（交互版）

[`aruing-architecture.html`](aruing-architecture.html) 是 [architecture.md](architecture.md) 所记事实的交互版：单页自包含，可离线打开；点开节点可跳到该事实对应的源码行区间，并支持上游 / 下游路径追踪、明暗主题与图片 / SVG 导出。

## 生成方式

| 项 | 值 |
| --- | --- |
| 工具 | Archify 3.2.0（agent skill，Apache-2.0，仓库 `tt-a1i/archify`；安装 `npx skills add tt-a1i/archify`） |
| 输入 | 手写的 `architecture` 类型 JSON：组件、边界、关系，每个组件带 `sources` 的仓库相对路径与行区间 |
| 代码基线 | `915da4007343d554a7abed6ae0c531449275baf7`（生成时的 `main`） |
| 产品 | 单个 HTML（约 840KB），内联样式与脚本，无外部请求 |

节点上的来源链接固定指向上述 commit 的 GitHub 行区间，所以这些行号的含义不会随后续改动漂移；它们记录的是生成那一刻的代码。生成走 schema 校验、交付、provenance 校验和真实浏览器检查（1440×900 与 2048×1320，零横向溢出），任何一项不通过都不会写出文件。

## 覆盖范围

图画的是运行时主链路：CLI / TUI → `Session` → `Tower` 基线（reply / call_tool / escalate）→ `Orchestrator` 五角色与取证循环 → `Dispatcher`（策略授权区）→ `k8s` 工具 → 集群，另含 LLM 客户端与数据目录。评测侧（`internal/eval`，`bench` / `probe` / `judge` / `scenarios` 子命令）和 `connect` / `update` 运维命令不在图内。

图只表达代码里能读出的事实：没有证据的边不画，也不声称运行时影响、风险或运行时拓扑之外的推断。字段与硬约束以 [architecture.md](architecture.md) 为准。

## 重生成

```bash
npx skills add tt-a1i/archify --skill archify        # 装到 .agents/skills/（不入库）
node .agents/skills/archify/bin/archify.mjs finalize architecture candidate.json \
  docs/aruing-architecture.html --repo-root . --quality showcase
```

`candidate.json` 不入库：组件与关系的措辞、取舍和布局是人对当时代码的判断，重生成是一次重新描述，不是重放旧输入。重生成时把 `meta.repository.revision` 钉到当时的 `HEAD`，并让每个组件的 `sources` 指回该 revision 的真实行区间；行列对不上时 `finalize` 会失败而不是产出过期图。
