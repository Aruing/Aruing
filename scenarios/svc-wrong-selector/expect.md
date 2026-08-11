# svc-wrong-selector — 验收（chat 路径）

> 以 `aruing chat` 为验收对象。「应出现 / 不应出现」由人/AI 对照勾选，**非**自动评分；LLM 措辞不要求逐字匹配。
> `run` 路径不作单独验收（反问退出或默认输出都视为允许）。

## 应出现

- 指出 **Service selector 不匹配** / Endpoints 为空 / 选不中 Pod
- 调查链含 `kubectl get endpoints` / `describe svc` 类证据痕迹

## 不应出现

- 仅归因于集群网络插件整体故障，且**不查 Endpoints** 就下结论
- 把 Pod 本身说成异常（Pod 实际是 Running）
