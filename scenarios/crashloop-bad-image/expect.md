# crashloop-bad-image — 验收（chat 路径）

> 以 `aruing chat` 为验收对象。「应出现 / 不应出现」由人/AI 对照勾选，**非**自动评分；LLM 措辞不要求逐字匹配。
> `run` 路径不作单独验收（反问退出或默认输出都视为允许）。

## 应出现

- 结论指向**镜像拉取失败** / `ImagePullBackOff` / 镜像不存在
- 调查链含 `kubectl get` / `describe pod` / `events` 类证据痕迹

## 不应出现

- 主结论归因为节点宕机、DNS、业务代码逻辑（且无对应证据）
- 编造 Pod 名 / 事件而不调用工具取证
