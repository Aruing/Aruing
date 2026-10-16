# pending-nodeselector — 验收（chat 路径）

> 以 `aruing chat` 为验收对象。「应出现 / 不应出现」由人/AI 对照勾选，**非**自动评分；LLM 措辞不要求逐字匹配。

## 应出现

- 结论指向**调度约束不满足**：nodeSelector（`workload-type=batch-node`）与节点标签不匹配，无节点可调度
- 调查链含 `kubectl get` / `describe pod` / `events`（FailedScheduling）类证据痕迹

## 不应出现

- 主结论归因为资源不足（Insufficient cpu/memory）、镜像问题、控制器故障（且无对应证据）
- 编造节点标签 / 事件而不调用工具取证
