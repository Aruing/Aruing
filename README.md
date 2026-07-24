# aruing

云原生 Kubernetes 故障诊断助手。用户用自然语言提问，系统提出故障猜想、调用工具收集证据、基于证据生成可追溯的诊断报告。

## 当前阶段

`0.0.1-beta2` / 真实闭环已完成：五个角色全部接 LLM，`aruing run` 在真实 Kubernetes 集群 + 真实 LLM 下端到端产出可追溯的 Markdown 报告。下一阶段待规划。

详细状态见 [`docs/project-state.md`](docs/project-state.md)。

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

./bin/aruing run default 里的 demo-api 为什么访问不了          # 默认输出 Markdown 报告
./bin/aruing run --format json default 里的 demo-api 为什么访问不了  # 结构化 JSON
```

### 本地 LLM 调试（推荐）

不必每次手动 `export`。仓库已 ignore `.env`；用模板生成后由 Make 自动加载：

```bash
cp .env.example .env    # 填入 BaseURL / APIKey / Model
make print-env          # 确认已加载（不打印 key 全文）
make run-llm            # source .env 后执行 aruing run
make run-llm QUESTION='default 里的 demo-api 为什么访问不了'
# 多套环境：make run-llm ENV_FILE=.env.ollama
```

三个 LLM 变量都非空时走真角色；任一缺失或没有 `.env` 时与现行为一致（全 fake）。  
也可在 shell 里自行 `export`，不经过 Make。
## 关键约束摘要

- `Run` 不嵌套子实体，所有实体通过 `RunID` 扁平关联
- `Query` 线索不能直接当作 `Target`，必须经真实环境确认
- 模型输出不能冒充 `Evidence`
- `Verdict` 必须引用 `Evidence`
- `Task` 只用通用 `Refs` 关联数据，不增加阶段专用引用字段
- 工具接口不限定读写；当前阶段只注册读工具，后续"辅助修复"阶段会加入需用户确认的写工具
- 当前 `Orchestrator` 为线性单轮驱动（临时）；多轮对话与系统内连查通过后续编排升级实现，不推翻扁平领域模型与工具协议

完整硬约束见 [`docs/architecture.md`](docs/architecture.md#硬约束)。

## 文档入口

| 位置 | 内容 |
| --- | --- |
| [`docs/architecture.md`](docs/architecture.md) | 架构事实：模块职责、数据结构、信任边界、硬约束 |
| [`docs/project-state.md`](docs/project-state.md) | 当前阶段、工作单元状态、下一步 |
| [`docs/skills/`](docs/skills) | 项目级 skill（注释规范、测试规范、文档规范） |
| [`AGENTS.md`](AGENTS.md) | AI 工具安装与项目 skill 约定 |

更长的方案、设计推理、阶段计划、预留问题记录在 `arui-note/aruing/` 笔记目录，由维护者个人保管。
