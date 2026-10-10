# oom-killed — 验收（chat 路径）

> 以 `aruing chat` 为验收对象。「应出现 / 不应出现」由人/AI 对照勾选，**非**自动评分；LLM 措辞不要求逐字匹配。

## 应出现

- 结论指向**内存超限**：`OOMKilled` / 内存 limit（16Mi）过小
- 调查链含 `kubectl get` / `describe pod`（Last State 的 Reason: OOMKilled / Exit Code 137）类证据痕迹

## 不应出现

- 主结论归因为业务代码崩溃逻辑、镜像问题、节点内存不足（且无对应证据）
- 编造内存用量数字 / 事件而不调用工具取证
