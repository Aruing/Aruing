# flapping-crashloop — 验收（chat 路径）

> 以 `aruing chat` 为验收对象。「应出现 / 不应出现」由人/AI 对照勾选，**非**自动评分；LLM 措辞不要求逐字匹配。
> `run` 路径不作单独验收（反问退出或默认输出都视为允许）。

## 应出现

- 用重启计数 / `lastState.terminated` / 重启间隔这类**跨周期**证据说明「不是偶发一次，而是反复崩」
- 拿到**非当前周期**的日志：`logs -p`（上一容器）、或 `--timestamps` 后按时间窗跨重启周期取；结论引用真实时间戳
- 根因指向 `RETRY_BUDGET=0`（重试预算耗尽即致命），而不是「偶发异常 / 无问题」

## 不应出现

- 以单次 `logs` 看起来正常就下「偶发、没有故障」类结论
- 把 FATAL 归因为镜像、节点、网络问题而无对应证据
- 编造日志行 / 时间戳，或用当前周期日志冒充历史周期

## 前置核对（跑 chat 前）

- `make lab-kube NAME=flapping-crashloop CMD="get po -n demo"` → 容器应处于 `CrashLoopBackOff` 且 `RESTARTS` 持续增长
- `make lab-kube NAME=flapping-crashloop CMD="logs -n demo deploy/demo-api"` → 当前周期日志只有 `[boot]` 行（尚未崩或刚重启）
- `make lab-kube NAME=flapping-crashloop CMD="logs -n demo deploy/demo-api --previous"` → 应能看到 `FATAL: ...retry budget exhausted (RETRY_BUDGET=0)`
- 若第二次命令已直接看到 FATAL 行（崩溃恰好落在观察窗口内），本场景的「症状不稳定」前提未成立：隔几分钟重跑上述两条再验收
