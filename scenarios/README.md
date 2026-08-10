# Reproducible Scenarios (kind)

> 一键起、测完拆的本地故障场景台架。给关键节点 smoke 提供**可复现**的真集群 + 真 LLM 环境。
> 这是 `0.1.0-beta10` 的交付物；**不是** CI 必绿、**不是**自动打分 / golden 体系。

## 前置依赖

| 工具 | 用途 | 安装 |
| --- | --- | --- |
| Docker | kind 的运行时 | [Docker Desktop](https://www.docker.com/) |
| kind | 跑本地 k8s 集群 | `brew install kind` 或 `go install sigs.k8s.io/kind@latest` |
| kubectl | 场景清单 apply / aruing 集群工具后端 | `brew install kubectl` |

`aruing` 二进制：先 `make build`（产出 `./bin/aruing`）。
LLM 配置：`playground/config.yaml`（见根 README 与 `aruing.example.yaml`）。

## 三命令

```bash
make scenario-list                          # 列已知场景 + kind 集群状态
make scenario-up   NAME=crashloop-bad-image # 起集群 + apply 故障清单 + 导出 kubeconfig
make scenario-down NAME=crashloop-bad-image # 拆集群 + 清理 kubeconfig
```

`scenario-up` 完成后会打印：

- 临时 kubeconfig 路径：`scenarios/.kube/<NAME>.yaml`（已 gitignore，不入库）
- 下一步的 `export KUBECONFIG=...` 与 `aruing chat` 命令
- 提示词文件与验收文件路径

集群命名隔离（`aruing-sc-<NAME>`），**不碰默认 kubecontext**。

## 验收（以 `chat` 为主）

> **验收对象是 `aruing chat`**（正常使用即对话路径）。`aruing run` 单轮路径**不作单独验收**：在 `run` 下无论是反问（clarify 打问题后非零退出）还是默认输出，都视为允许。

每个场景目录三件套：

| 文件 | 内容 |
| --- | --- |
| `prompts.md` | 固定用户问法（按顺序发给 `chat`） |
| `expect.md` | 验收：应出现 / 不应出现（人/AI 对照，非自动评分） |
| `manifests/` | 故障清单（kubectl apply） |
| `scenario.yaml` | 场景元数据（给人读；脚本不解析，命名按约定 `aruing-sc-<name>`） |

标准流程（`scenario-chat` / `scenario-kube` 已自动注入 KUBECONFIG，无需手动 export）：

```bash
make build
make scenario-up NAME=crashloop-bad-image
make scenario-chat NAME=crashloop-bad-image MSG="demo 命名空间里的 demo-api 为什么起不来"
# 对照 scenarios/crashloop-bad-image/expect.md 勾选
make scenario-down NAME=crashloop-bad-image
```

查集群细节（同样不用手动 export）：

```bash
make scenario-kube NAME=crashloop-bad-image CMD="get po -A"
make scenario-kube NAME=crashloop-bad-image CMD="describe pod -n demo -l app=demo-api"
```

> 不想用包装、想自己控制环境变量也行：`make scenario-up` 结尾会打印 `export KUBECONFIG=...` 路径，自己执行后即可直接用 `./bin/aruing chat ...` 和 `kubectl ...`。多轮交互式 chat：`make scenario-chat NAME=<name>`（不带 MSG）。

**通过** = `expect.md` 中「应」基本满足且无严重「不应」。LLM 措辞不要求逐字匹配。

## 已知场景

| 场景 | 故障 | 测什么 |
| --- | --- | --- |
| `crashloop-bad-image` | Deployment 引用不存在的镜像 tag | 主路径：ImagePull / 起不来 |
| `svc-wrong-selector` | Service selector 与 Pod label 不一致 | 「访问不到」类；应查到 endpoints 空 |
| `same-name-multi-ns` | 两个 ns 同名 Deployment（一好一坏），提示词不带 ns | beta9 clarify 挂起或歧义并列 |

## 约束

- **不进** `make test` / `make check`：CI 不依赖 Docker / kind。
- kubeconfig 写 `scenarios/.kube/`（gitignore），不入库、可删。
- 场景 ID / 提示词**不进** agent 意图枚举（这是验收探针，不是产品分支）。
- LLM 密钥仍放 `playground/config.yaml` / env，不进场景清单。
