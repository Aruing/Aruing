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

![Aruing 行内对话诊断 crashloop Pod](docs/assets/example.png)

## 当前阶段

**`0.1.3` —— 证据信息增益驱动的取证决策 + 分层记忆。** 调查阶段由取证决策循环驱动（贝叶斯信念、EIG 排序动作选择、MSPRT 序贯停止——`agent.acquire.method` 可在同一二进制内切 ReAct / 随机 / 最低成本 / 串行基线臂），长会话采用信任分层记忆视图：诊断与证据索引卡常驻可寻址、近期轮次原文保留、中段历史预算内压缩、分层检索按需回灌证据 raw 预览（`agent.memory.method` 可切 last-N / 平铺摘要基线臂）。评测栈新增探针装置（`aruing probe`，20/50 轮脚本化长会话 + 尾部探针）、③层全池化抽样 + LLM 辅助评 + 人工一致率（`judge --sample-total/--rubric-llm/--agree`）与矩阵驱动器（`make eval-sweep` / `make probe-sweep`，含断点续跑与 manifest）。0.1.0–0.1.1 的能力全部保留：真集群 + 真 LLM 诊断、交互式终端对话、证据导航、澄清挂起与恢复、代表性投影、一行安装与自更新。

尚未构建（计划 0.2+）：npm 分发、磁盘持久化（会话进程内、退出即丢）、流式响应、带审批的写工具、多集群、Web UI。路线图见 [`docs/project-state.md`](docs/project-state.md)。

已随 [`v0.1.3`](https://github.com/Aruing/Aruing/releases/tag/v0.1.3) 发布（2026-09-05；release notes 含 0.1.2 取证决策全部增量）。

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

## 安装

一行命令（安装最新 Release 到 `~/.aruing/bin`，带 sha256 校验）：

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/Aruing/Aruing/main/scripts/install.sh | bash
```

```powershell
# Windows（PowerShell）—— experimental：基础命令已验证；交互式 `chat` TUI 未在真实 Windows 终端验证
irm https://raw.githubusercontent.com/Aruing/Aruing/main/scripts/install.ps1 | iex
```

`~/.aruing/bin` 不在 PATH 时安装器会打印需追加的那一行。从源码安装：`go install github.com/Aruing/Aruing/cmd/aruing@latest` 或 `make build`（源码构建版本号为 dev 且无法自更新；需要可升级安装请用安装脚本）。

后续升级用 `aruing update`（自 GitHub Releases 自更新，带校验和验证）。npm 安装的请用 `npm update -g aruing`。

首次使用？用交互式向导配置大模型（写入用户级配置文件，保存前做连通测试）：

```bash
aruing connect
```

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
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | 参与贡献：环境、PR 规则、验证要求 |

更长的设计笔记在私人 `arui-note/aruing/` 笔记目录（仅维护者）。
