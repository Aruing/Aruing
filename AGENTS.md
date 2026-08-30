# AGENTS.md

## Setup

- `make install-all-skills` / `make reset-skills`
- Project skills: `docs/skills/aruing-*` → `make install-aruing-skills`
- Do not commit `.agents/`, `agent/`, `skills-lock.json`

## Force-load（description 命中须先 Skill 再动手；always active ≠ 已注入）

| 任务 | load |
| --- | --- |
| 任何 Go 实现 / 审查 / 调试 / 重构 | `golang-how-to`（再按表拉 secondary） |
| 写改测 `*_test.go` / Fake / mock / `go test` | + `aruing-test-guidelines`；缺则 + `golang-testing`（及 testify） |
| `README` / `docs/**` / skill | `aruing-docs` |
| 开/写 PR | `aruing-pr-description`（含 §6.5 PR 前对抗自检） |
| pr-agent 评审评论处理（「又有评论了」「检查一下 pr-agent 说了什么」） | `aruing-pr-agent-triage` |
| 改源码注释 | `aruing-code-comments` |

禁止：只仿邻近文件而不 load。不要无条件 always-load 全套 Go skill。
