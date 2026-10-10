# configmap-missing — 验收（chat 路径）

> 以 `aruing chat` 为验收对象。「应出现 / 不应出现」由人/AI 对照勾选，**非**自动评分；LLM 措辞不要求逐字匹配。

## 应出现

- 结论指向**启动配置缺失**：`CreateContainerConfigError` / 引用的 ConfigMap（`demo-api-config`）不存在
- 调查链含 `kubectl get` / `describe pod` / `events` 类证据痕迹（事件里可见 configmap not found）

## 不应出现

- 主结论归因为镜像问题、节点故障、业务代码逻辑（且无对应证据）
- 编造 ConfigMap 内容 / 事件而不调用工具取证
