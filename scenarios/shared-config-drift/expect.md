# shared-config-drift — 验收（chat 路径）

> 以 `aruing chat` 为验收对象。「应出现 / 不应出现」由人/AI 对照勾选，**非**自动评分；LLM 措辞不要求逐字匹配。
> 高难场景：症状对象（orders-api）≠ 根因对象（ConfigMap app-config）；现场有两个真故障诱饵，结论前须证据排除。

## 应出现

- 主结论指向**配置漂移**：`app-config` 的 `DB_HOST` 指向 payments ns 的 `payments-db`，而 orders-api 本该连 demo ns 的 `orders-db`
- 取证链含 env → ConfigMap 回溯痕迹：describe orders-api Deployment（envFrom 来源）→ 读出 `app-config` 中 `DB_HOST` 的原值
- 对照证据（任二即可）：
  - orders-api 日志 `fatal: cannot reach database at payments-db...`（连接目标实锤）
  - demo ns 存在健康 `orders-db`（本该连的对象）
  - payments-exporter 消费同一 ConfigMap 却健康（非整体损坏）
- 诱饵排除痕迹（任一即可）：
  - canary 形态区分：ImagePullBackOff（从未启动）≠ orders-api 的 CrashLoop（已启动打日志）；或 label 不在 Service selector / endpoints 不含 canary
  - payments-db OOM 属 payments ns——orders-api 连它是配置结果，修它不修配置

## 不应出现

- 主结论为「修好 payments-db（OOM / 内存）即可解决」（诱饵②：库真挂了，但不是该连的库）
- 主结论为「orders-api / canary 镜像问题」（诱饵①：canary 与 orders-api 无关）
- 结论停在「数据库连不上」症状复述，未回溯 DB_HOST 配置来源
- 编造配置内容 / 事件而不调用工具取证
