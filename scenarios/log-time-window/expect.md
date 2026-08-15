# log-time-window — 验收（chat 路径）

> 以 `aruing chat` 为验收对象（beta17 logs 时间游标 smoke）。「应出现 / 不应出现」由人/AI 对照勾选，**非**自动评分；LLM 措辞不要求逐字匹配。

## 应出现

- 工具链含 `kubectl logs ... --timestamps`（时间游标的前提载体）
- 出现 `evidence.read` 且参数带 `since`（或 `since`+`until`，RFC3339），evidenceId 指向刚才的 logs 观察
- 切片结果 meta 含时间窗与窗内首/末行时间戳，窗口内只含目标重启周期的行（或明确说明窗口为空的原因）
- 最终回答基于窗口内日志行，引用真实时间戳

## 不应出现

- 对无 `--timestamps` 的旧观察做时间切片还「成功」（应触发明确报错引导）
- 编造时间戳 / 行内容而不来自工具观察
- 对 logs 观察退化成整块 stdout 直接贴给用户（不做时间导航）
