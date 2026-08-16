# Aruing

[English](README.md) | [中文](README.zh-CN.md)

[![CI](https://github.com/Aruing/aruing/actions/workflows/pr-check.yml/badge.svg)](https://github.com/Aruing/aruing/actions/workflows/pr-check.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.26-00ADD8.svg)](go.mod)

**一个用工具和证据回答的 Kubernetes 运维 agent——不靠感觉。**

自然语言提问。agent 推理、调用集群工具取真实证据、形成假设；需要根因时，强制走证据裁决链——每条结论可追溯。

## 为什么是 agent（不是 chatbot）

| 层 | 做什么 |
| --- | --- |
| **智能基线** | 观察 → 思考 → **调工具** → 观察 → 回答。「集群装了什么」「这个服务怎么配的」这类问题都基于实时集群数据作答。 |
| **诊断专长** | 根因类提问升格为正式管道：假设 → 任务 → **Evidence** → **Verdict**（必须引用 Evidence）→ **Report**。没有「我觉得是 X」——只有带工具痕迹的结论。 |
| **多轮对话** | 同会话追问；既往正式诊断可按 `RunID` 深读细解，不编造证据。 |

工具经共享 **Registry / Dispatcher**（shell-less kubectl 后端 + 授权 Policy）。模型输出永远不冒充 Evidence。

## 当前阶段

**`0.1.0` —— 可用的诊断助手，发布供评估。** 真集群 + 真 LLM 端到端可用：交互式终端对话（行内 / 全屏双模式）、主题定制、证据导航（含时间窗切片）、歧义提问的澄清挂起与恢复。

尚未构建（计划 0.2+）：磁盘持久化（会话进程内、退出即丢）、流式响应、带审批的写工具、多集群、Web UI。路线图见 [`docs/project-state.md`](docs/project-state.md)。

⚠️ 当前仅支持简单试用 / 测试——未达生产可用。

## 核心数据流

```
Run → Query → Target → Hypothesis → Task → Evidence → Verdict → Report
```

- **Run** —— 一次诊断单元  
- **Query / Node / Edge** —— 问题中未经核实的线索  
- **Target** —— 在真实集群中确认过的对象  
- **Hypothesis** —— 待证据验证的候选原因  
- **Evidence** —— 工具实际执行的记录（唯一可信事实源）  
- **Verdict** —— 只能来自 Evidence  
- **Report** —— 引用 Verdict + Evidence；不编造  

## 环境要求

| 依赖 | 说明 |
| --- | --- |
| Go 1.26+ | 仅构建需要 |
| kubectl | 集群访问；路径自动探测或设 `tools.kubectl_path` |
| LLM（OpenAI 兼容） | 任意 base URL + 模型；`run` / `chat` 必需 |
| Docker + kind | 可选——仅可复现故障场景需要 |

## 快速开始

```bash
make build              # 构建 cmd/aruing
make test               # 全部测试
make check              # 完整 CI（test-ci + vet + lint + fmt + tidy + vuln）

cp aruing.example.yaml playground/config.yaml   # 填 llm.*（已 gitignore）
./bin/aruing run --config playground/config.yaml why is demo-api in default unreachable
./bin/aruing chat --config playground/config.yaml hello   # 交互式 TUI（行内模式）
```

`run` / `chat` **须 LLM 配置齐全**。优先级：CLI 旗标（如 `--verbose`、`--ui`）> 环境变量（`ARUING_*`）> YAML 文件 > 零值。配置搜索：`--config` / `ARUING_CONFIG` → `playground/config.yaml` → `$XDG_CONFIG_HOME/aruing` → `/etc/aruing`。

更多示例：

```bash
./bin/aruing run --format json why is demo-api in default unreachable
./bin/aruing chat --session sess_xxx check redis again   # 续会话
./bin/aruing chat --ui app                               # 全屏模式
```

### 终端交互（TUI）

`aruing chat` 两种模式（配置 `tui.mode` 或 `--ui`）：

- **inline**（默认）—— 终端内滚动留痕式对话，glamour 渲染 Markdown，shift+enter 软换行
- **app** —— bubbletea 全屏界面

主题：内置 `dark` / `light` / `auto`（配置 `tui.theme`）。完整定制：复制 [`tui.example.yaml`](tui.example.yaml) 并在配置中写明 `tui.theme_file` 指向它——只声明要覆盖的样式项，其余回落内置基底。

### 可复现场景（kind）

一键起、测完拆的故障集群，供手工 smoke。验收以 `chat` 为对象（见 [`scenarios/README.md`](scenarios/README.md)）：

```bash
make lab-list                                     # 已知场景 + 集群状态
make lab-up   NAME=crashloop-bad-image            # kind 集群 + 故障清单
make lab-chat NAME=crashloop-bad-image MSG="why is demo-api in demo not starting"
make lab-down NAME=crashloop-bad-image
```

当前内置四个场景：`crashloop-bad-image`、`svc-wrong-selector`、`same-name-multi-ns`（含多轮澄清挂起 case）、`log-time-window`（证据时间窗切片）。`lab-chat` / `lab-kube` 自动注入 KUBECONFIG（无需手动 export）。不进 `make test` / CI；本地需 Docker + kind + kubectl。

### 配置与本地 LLM

完整参考：[`aruing.example.yaml`](aruing.example.yaml)（含注释：llm / tools / tui / debug）。环境变量兜底：[`.env.example`](.env.example)，配 `make run-llm` / `make print-env`（Make source `.env`；二进制本身不解析该文件）。

## 约束（摘要）

- 实体扁平、经 `RunID` 关联——`Run` 不嵌套  
- 线索未经环境确认不得成为 `Target`  
- 模型输出 ≠ Evidence；Verdict 必须引用 Evidence  
- 不枚举用户操作 / 资源类型；不得用人为 N 条上限阉割正常能力（超预算 → 压缩，不静默丢弃）  
- 工具不限定读写；授权由 Policy 把关。当前注册只读工具；写工具带审批后开放  
- `run` → Orchestrator；`chat` → Session.Turn + Tower；共用 Dispatcher  

完整清单：[`docs/architecture.md`](docs/architecture.md#硬约束)（含 #15–#20）。

## 文档

| 路径 | 内容 |
| --- | --- |
| [`docs/architecture.md`](docs/architecture.md) | 架构事实：模块、数据模型、信任边界、硬约束 |
| [`docs/project-state.md`](docs/project-state.md) | 阶段、工作单元、下一步 |
| [`docs/README.md`](docs/README.md) | docs/ 与私人笔记目录的分工 |
| [`scenarios/README.md`](scenarios/README.md) | kind 故障场景：用法、cases 协议、验收 |
| [`aruing.example.yaml`](aruing.example.yaml) / [`tui.example.yaml`](tui.example.yaml) | 带注释的配置 / 主题参考 |
| [`docs/skills/`](docs/skills) | 项目级 skill（文档/测试/注释规范、PR 描述、里程碑收尾、全量自检、真集群测试纪律、回顾审计） |
| [`AGENTS.md`](AGENTS.md) | AI 工具约定 / skill 安装 |

更长的设计笔记在私人 `arui-note/aruing/` 笔记目录（仅维护者）。
