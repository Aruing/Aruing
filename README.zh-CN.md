# Aruing

[English](README.md) | [中文](README.zh-CN.md)

**面向 Kubernetes 的运维诊断 Agent：用工具取证回答，而不是凭感觉编造。**

用自然语言提问。Agent 会推理、调用集群工具收集真实证据、提出故障猜想；需要根因时走强制证据裁决链，每条结论可追溯到具体工具调用。

## 为什么是 Agent（不是普通 chatbot）

| 层 | 做什么 |
| --- | --- |
| **永远在线的智能基线** | 观察 → 思考 → **调工具** → 观察 → 结论。无论问「集群装了什么」还是「服务怎么配的」，都基于实证据回答。 |
| **诊断专长** | 根因类问题升格正式管道：假设 → 任务 → **Evidence** → **Verdict**（必须引用 Evidence）→ **Report**。没有工具轨迹就不能假装下结论。 |
| **可追问多轮** | 同一会话续聊；正式诊断可按 `RunID` 读回深解，不编造证据。 |

工具统一经 **Registry / Dispatcher**（shell-less kubectl 后端 + Policy 授权）。模型输出不得冒充 Evidence。

## 当前阶段

**正在开发中 —— 收拢诊断闭环。** 预计 **2026 年 8 月底** 完成可用闭环。

Aruing 现在已能端到端跑通，但仍处于早期：

- **目前仅支持简单的试用 / 测试** —— 尚未达到生产可用
- **配套的交互与工程化仍在开发** —— 终端输入交互、结构化日志等打磨尚未完成

当前能力（版本 `0.1.0` 进行中）：

- `aruing run` / `chat` — 须 LLM；YAML 配置和/或 `ARUING_*`（`--config`，见 `aruing.example.yaml`）
- `aruing run` — 单轮诊断（线性 Orchestrator；歧义时打印澄清并退出，无 resume）
- `aruing chat` — 多轮 Session + Tower；需根因时 escalate；定位澄清挂起/恢复；`RunLedger` + `prior_run_details`；compact 后按范围回灌

详见 [`docs/project-state.md`](docs/project-state.md)。

## 核心数据流

```
Run → Query → Target → Hypothesis → Task → Evidence → Verdict → Report
```

- `Run` 是一次诊断的主体  
- `Query / Node / Edge` 是从用户问题中提取的**未验证线索**  
- `Target` 是真实集群中**已确认**的对象  
- `Hypothesis` 是待证据验证的猜想  
- `Evidence` 是工具实际执行产生的记录  
- `Verdict` 只能基于 `Evidence` 得出  
- `Report` 引用 `Verdict` 和 `Evidence`，不编造  

## 快速开始

```bash
make build              # 编译 cmd/aruing
make test               # 跑全部测试
make check              # 完整 CI 检查（test-ci + vet + lint + fmt + tidy + vuln）

./bin/aruing run default 里的 demo-api 为什么访问不了
./bin/aruing run --format json default 里的 demo-api 为什么访问不了
./bin/aruing chat hello                                    # 多轮 chat（须 LLM；session id 打在 stderr）
./bin/aruing chat --session sess_xxx 再查一下 redis
```

### 可复现场景（kind）

一键起故障集群用于手工 smoke。验收**以 `chat` 为主对象**（见 [`scenarios/README.md`](scenarios/README.md)）：

```bash
make lab-up   NAME=crashloop-bad-image   # 起集群 + 故障清单
make lab-chat NAME=crashloop-bad-image MSG="demo 里的 demo-api 为什么起不来"
make lab-down NAME=crashloop-bad-image
```

`lab-chat` / `lab-kube` 自动注入 KUBECONFIG，无需手动 export。不进 `make test` / CI；需本机有 Docker + kind + kubectl。

### 配置与本地 LLM

`run` / `chat` **必须** LLM 三件套齐全（文件和/或环境变量）。优先级：CLI（如 `--verbose`）> env（`ARUING_*`）> YAML 文件 > 零值。

```bash
cp aruing.example.yaml playground/config.yaml   # 填 llm.*；playground/ 已 gitignore
./bin/aruing run --config playground/config.yaml default 里的 demo-api 为什么访问不了
# 或：ARUING_CONFIG=... / 搜索 playground → $XDG_CONFIG_HOME/aruing → /etc/aruing
```

Make + `.env` 仍可用（仓库 ignore `.env`；config 包本身不解析该文件）：

```bash
cp .env.example .env    # 填入 BaseURL / APIKey / Model
make print-env
make run-llm
make run-llm QUESTION='default 里的 demo-api 为什么访问不了'
make chat
make chat CHAT_MSG='hello'
```

## 关键约束摘要

- `Run` 不嵌套子实体，所有实体通过 `RunID` 扁平关联  
- `Query` 线索不能直接当作 `Target`，必须经真实环境确认  
- 模型输出不能冒充 `Evidence`；`Verdict` 必须引用 `Evidence`  
- 不枚举用户操作 / 资源类型；**也不用人为条数 / 步数阉割正常能力**（超物理预算则压缩上下文等，不静默截肢）  
- 工具接口不限定读写；当前阶段只注册读工具，后续「辅助修复」会加入需用户确认的写工具  
- 入口：`aruing run` 直连线性 `Orchestrator`；`aruing chat` 经 `Session.Turn` + Tower。不推翻扁平领域模型与 Dispatcher  

完整硬约束见 [`docs/architecture.md`](docs/architecture.md#硬约束)（含 #15–#19）。

## 文档入口

| 位置 | 内容 |
| --- | --- |
| [`docs/architecture.md`](docs/architecture.md) | 架构事实：模块职责、数据结构、信任边界、硬约束 |
| [`docs/project-state.md`](docs/project-state.md) | 当前阶段、工作单元状态、下一步 |
| [`docs/skills/`](docs/skills) | 项目级 skill（注释规范、测试规范、文档规范） |
| [`AGENTS.md`](AGENTS.md) | AI 工具安装与项目 skill 约定 |

更长的方案、设计推理、阶段计划、预留问题记录在 `arui-note/aruing/` 笔记目录，由维护者个人保管。
