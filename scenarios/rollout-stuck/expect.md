# rollout-stuck — 验收（chat 路径）

> 以 `aruing chat` 为验收对象。「应出现 / 不应出现」由人/AI 对照勾选，**非**自动评分；LLM 措辞不要求逐字匹配。
> 高难场景：症状（发布卡死）与根因（v2 模板探针端口）同对象异层；现场 HPA 报错是诱饵，结论前须证据排除。

## 应出现

- 主结论指向 **v2 就绪探针端口错配**：探针 `httpGet /healthz` 指向 8081，而容器实际监听 8080 → 新 pod 永不 ready
- 取证链含两代 RS 状态理解（任一即可）：
  - describe deploy web-api：两代 ReplicaSet（旧代 ready 3/3、新代 ready 0/1）与 Progressing 状态
  - describe rs / get pods：v2 pod Running 但 0/1 Ready
- 根因直证（任一即可）：
  - v2 pod events：`Readiness probe failed: Get http://…:8081/healthz: connect: connection refused`
  - 对照 v2 模板探针端口 8081 与容器实际监听 8080（logs 中 httpd 监听行可佐证）
- 诱饵排除痕迹：HPA / metrics-server 报错被识别为与发布卡死无关（replicas 未增、卡在「新代不 ready」而非「扩不动」）

## 不应出现

- 主结论为「装 metrics-server / 修好 HPA 即可恢复发布」（诱饵：报错真实但与卡死无关）
- 主结论停在「扩容失败 / 副本不够」症状复述，未查两代 RS 与探针状态
- 编造探针配置 / 事件而不调用工具取证
