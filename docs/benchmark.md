# 评测基准（benchmark）

> 一条命令跑「多场景 × 长会话 × 判分」三维矩阵：逐场景起/拆 kind 集群 → 单轮诊断维 + 长会话维 → ③层抽样 → 聚合报告。
> **不进** `make test` / `make check` / CI（必绿路径不依赖 Docker / kind）。

## 三维矩阵

| 维度 | 驱动 | 矩阵 | 判分 |
| --- | --- | --- | --- |
| 单轮诊断（diag） | `scripts/eval-sweep.sh` | 场景 × 取证方法臂 × K × 重复 | ①根因命中 ②引用合法（`aruing judge`） |
| 长会话（probe） | `scripts/probe-sweep.sh` | 场景 × 记忆方法臂 × 轮数 × 重复 | 探针①层（含内嵌诊断复判）+ 会话级记录 |
| ③层抽样 | `aruing judge --sample-total` | diag 维记录逐场景池化抽 N 行 | supports / partial / not_supports（待回填或 LLM 辅助评） |

`make bench-run`（`scripts/bench-run.sh`）编排以上全部：逐场景 **fresh-up**（已存在同场景集群先拆再建；一次只存一个 kind 集群；不碰默认 kubecontext，复现者无须手动 `lab-up`）→ 两维单元 → ③层抽样 → 聚合。场景声明的 `chat-env`（如 `bigtable-fleet` 的 map-reduce 投影）自动注入该场景两维单元，留痕 `scenario-env.txt`。probe 维仅对带 `probe.yaml` 的场景生效（当前 8 个场景均带）。

## 依赖

| 工具 | 用途 |
| --- | --- |
| Docker + kind | 逐场景本地集群（fresh-up 起/拆） |
| kubectl | 清单 apply / aruing 集群工具后端 |
| python3 | manifest 写入与聚合（标准库即可） |
| aruing | `make build` 产出 `./bin/aruing`（或 `ARUING=` 指定既有二进制） |
| LLM 配置 | `aruing connect`（用户级）或 `playground/config.yaml`（`CONFIG=` 指定） |

## 从零复现

```bash
git clone https://github.com/Aruing/Aruing.git aruing && cd aruing
make build                 # 或 go install github.com/Aruing/Aruing/cmd/aruing@latest
aruing connect             # 或 cp aruing.example.yaml playground/config.yaml 后填 llm.*
make bench-run DRYRUN=1    # 干跑：逐场景生命周期计划 + 两维单元命令与计数，零集群零 LLM
make bench-run             # 真跑（先按「成本纪律」核对）
```

产物在 `OUT`（默认 `eval/results/bench/<UTC 时间戳>`，已 gitignore），先看 `summary.md`。

子集示例：

```bash
make bench-run SCENARIOS="crashloop-bad-image" DIMS=diag                     # 单场景单维
make bench-run SCENARIOS="crashloop-bad-image configmap-missing" \
  METHODS="ours" KS=3 REPS=1 ROUNDS=20 RUBRIC_N=5                            # 小矩阵
make bench-run AGG_ONLY=1 OUT=<既有批目录>                                    # 只重聚合
```

## 参数

默认值与完整语义以 `scripts/bench-run.sh` 头注释为权威；本表为速览。

顶层：

| 参数 | 默认 | 语义 |
| --- | --- | --- |
| `DIMS` | `both` | `both` / `diag` / `probe`——跑哪些维度 |
| `SCENARIOS` | 全部已知场景 | 空格分隔；场景 = `scenarios/` 下有 `manifests/` 的目录 |
| `OUT` | `eval/results/bench/<UTC 时间戳>` | 产物目录 |
| `CONFIG` | `playground/config.yaml` | LLM 配置文件 |
| `ARUING` | `go run ./cmd/aruing` | aruing 调用方式（建议 `./bin/aruing` 省每次冷启动） |
| `DRYRUN` | `0` | `1` = 打印计划零执行（零集群零 LLM 零文件写） |
| `FORCE` | `0` | `1` = 忽略已有记录强制重跑（含重抽 rubric） |
| `AGG_ONLY` | `0` | `1` = 只从既有产物重聚合 |
| `RUBRIC_N` | `20` | ③层每场景抽样行数；`0` = 跳过 |
| `RUBRIC_SEED` | `0` | ③层抽样种子 |
| `RUBRIC_LLM` | `0` | `1` = `judge --rubric-llm` 同模型逐行先评（增加 LLM 成本） |

透传两维 sweep（未设时 sweep 回落自身默认）：

| 参数 | eval-sweep 默认（diag 维） | probe-sweep 默认（probe 维） | 语义 |
| --- | --- | --- | --- |
| `METHODS` | `b1-serial b2-random b4-cheapest ours b3-react`（取证方法臂） | `ours d1-last-n d2-flat-summary`（记忆方法臂） | **两维词表不同**：`METHODS` 单值同时透传两维，需分别选臂时用 `DIMS` 分两次跑 |
| `KS` | `1 2 3 5 8` | 不消费 | diag 维名义 K |
| `ROUNDS` | 不消费 | `20 50` | probe 维会话轮数 |
| `REPS` | `3` | `3` | 重复次数 |

## 产物布局

```
<OUT>/
  manifest.json                 批档案：git/commit/model/kind·kubectl 版本、矩阵参数、③层参数
  summary.md / summary.csv      聚合报告（人读 / 机器可联结）
  rubric-summary.csv            ③层逐场景分布（机器可联结）
  failures.txt                  场景级失败清单（全量记录，不静默）
  scenario-env.txt              chat-env 注入留痕（有注入才建）
  diag/<scn>/records/<scn>/     诊断维评测记录（--eval-json 落盘；成功/挂起/失败三路径全记）
  diag/<scn>/judge-<scn>.json   ①②层判分
  diag/<scn>/eval-sweep.csv     诊断维逐单元汇总
  diag/<scn>/rubric-<scn>.json  ③层抽样评分表
  diag/<scn>/manifest.json      sweep 级档案
  diag/<scn>/plots/            诊断维可选出图（本机 `.venv` 装有 matplotlib 时生成；缺则跳过不阻塞）
  probe/<scn>/…                 长会话维同构（records/、judge、probe-summary.csv、manifest）
  *.log                         调试产物（见「开源清洗」）
```

## 开源清洗

产物默认无私人路径，可开源：

- manifest 的 `config` / `aruing` 字段**写时归一**：仓库内路径剥成相对，其余取 basename；sweep 级 manifest 的 `config` 聚合前同款重写
- **scrub 自检门**：聚合前扫 `OUT` 下全部非 `.log` 文件，命中 `$HOME` / 仓库根 /（`OUT` 为绝对路径时）`OUT` 字面量 → 记 `scrub-FAIL` 入失败清单并使批非零退出
- **可开源集 = `OUT` 下全部非 `.log` 文件**（自检门通过即无私人路径）；`*.log` 为调试产物不进开源集：`bench-run.stdout.log`、`run.rubric.stderr.log`、`diag/*/run.stderr.log`、`probe/*/{run.stderr,probe.stdout}.log`

## 断点续跑

- 单元记录已存在 → sweep 跳过该单元；rubric 评分表已存在 → 跳过抽样
- `FORCE=1` 全部重跑；只重抽某场景 rubric → 删对应 `diag/<scn>/rubric-<scn>.json` 后重跑
- `AGG_ONLY=1` 从既有产物重聚合（不碰集群、不跑单元、不重抽样）

## 成本纪律与失败语义

- 真跑前先 `DRYRUN=1` 核对单元数，按模型单价估算 LLM 成本再放行；缩子集用 `SCENARIOS` / `METHODS` / `KS` / `ROUNDS` / `REPS`
- 场景级失败**不中断**后续场景，全量记入 `failures.txt`，批末非零退出（失败不静默丢）。失败种类：`up-FAIL` / `down-FAIL` / `sweep-fail` / `records-missing` / `rubric-fail` / `scrub-FAIL`
- 单元失败重跑：修因后原命令再跑一次，已完成单元自动跳过
