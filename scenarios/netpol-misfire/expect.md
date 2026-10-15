# netpol-misfire — 验收（chat 路径）

> 以 `aruing chat` 为验收对象。「应出现 / 不应出现」由人/AI 对照勾选，**非**自动评分；LLM 措辞不要求逐字匹配。
> 高难场景：症状对象（edge/frontend）与根因对象（core/NetworkPolicy 标签错配）跨 ns 分层；sandbox 的 deny-all 是诱饵，结论前须证据排除。
> **数据面模拟声明**：kind 默认 CNI 不 enforce NetworkPolicy，frontend 崩溃症状由容器脚本确定性重放（真实连接结果被丢弃）；本场景验收以配置证据为准，**不要求连通性实测类证据**（工具面亦无此能力）。

## 应出现

- 主结论指向 core ns 的 NetworkPolicy `allow-frontend-to-api`：入站放行规则的 from podSelector 标签 `frontend-web` 与 frontend pod 实际标签 `app=frontend` 不匹配 → 规则匹配空集 → api 入站对所有人关闭
- 取证链（任二即可）：
  - frontend 日志：`dial tcp api.core.svc.cluster.local:80: i/o timeout`（超时语义指向丢包类故障）
  - frontend pod 实际标签（`app=frontend`）与放行规则错值（`app=frontend-web`）的对照
  - api 侧健康证据（Running / ready / Service 正常）——排除 api 本身故障
- 诱饵排除痕迹（任一即可）：
  - deny-all 属 sandbox ns，NetworkPolicy 仅作用于自身 ns 的 pod，edge→core 流量不归它管
  - sandbox ns 内无工作负载（deny-all 无实际作用对象）

## 不应出现

- 主结论为「sandbox 的 deny-all 挡了 frontend→api 流量」（诱饵：作用域管不着）
- 主结论为「api 挂了 / 没监听」（api 健康；日志为超时语义而非拒绝语义）
- **编造连通性测试结果**（工具面无 exec / connect 能力，不得声称「实测连不通 / 连得通」）
- 停在「网络超时」症状复述，未回溯策略配置
