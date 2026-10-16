# quota-vs-scheduler — 验收（chat 路径）

> 以 `aruing chat` 为验收对象。「应出现 / 不应出现」由人/AI 对照勾选，**非**自动评分；LLM 措辞不要求逐字匹配。
> 高难场景：症状（副本恒 0）与根因（ResourceQuota 满）分属不同机制层；nodeSelector 是诱饵，结论前须证据排除。
> 核心考面：admission 拒（无 Pod 对象）vs 调度 Pending（有 Pod 对象）的证据形态判别。

## 应出现

- 主结论指向**配额满 + 准入拒绝**：ResourceQuota `compute-resources` 的 requests.cpu 已满（used 1/1），payment-svc 的 Pod 在准入阶段被拒、从未创建
- 形态判别与根因直证（任二即可）：
  - `get pods`：payment-svc **无 Pod 对象**（≠ 调度 Pending 的有对象形态）
  - Deployment/ReplicaSet 事件：`FailedCreate … exceeded quota: compute-resources, requested: requests.cpu=2, used: requests.cpu=1, limited: requests.cpu=1`
  - `describe quota compute-resources`：used/hard 对照（requests.cpu 1/1）
- 诱饵排除痕迹（任一即可）：
  - ledger-api 带相同 nodeSelector 却 Running 健康（该选择器不阻塞调度）
  - nodeSelector 目标标签实际存在于节点（如 `get nodes --show-labels`）
  - 无 Pod 对象 → 根本未到调度阶段，nodeSelector 无从生效

## 不应出现

- 主结论为「nodeSelector 指向的节点调度不上去 / 选不到节点」（诱饵：标签实际存在，且无 Pod 可调度）
- 主结论停在「副本 0 / 起不来」症状复述，未查 Pod 对象形态与配额
- **编造** Pending 状态或 FailedScheduling 事件（实际无 Pod 对象——最常见的幻觉点）
- 编造配额数值 / 事件而不调用工具取证
