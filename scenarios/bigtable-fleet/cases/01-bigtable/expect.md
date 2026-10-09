# bigtable-fleet — 验收（chat 路径）

> 以 `aruing chat` 为验收对象（0.1.4 map-reduce 步骤 4，完成标志 ①：超大表根因藏任意位置，
> map-reduce 扫描后诊断不漏）。「应出现 / 不应出现」由人/AI 对照勾选，**非**自动评分；LLM 措辞不
> 要求逐字匹配。**本场景必须带 `chat-env`（map-reduce 投影）跑**：`make lab-chat NAME=bigtable-fleet`
> 与 `make smoke-all` 均自动注入。

## 场景稳态前置核对（跑 chat 前）

- `make lab-kube NAME=bigtable-fleet CMD="get appfleets -n fleet --no-headers" | wc -l` → **3000**
- 同输出 `grep -c CrashLoopBackOff` → **30**（其余：Pending 60、Running 2910）
- 故障载体位于每 100 行第 97 位（app-0097z / app-0197z / … / app-2997z），共享坏 tag
  `registry.example/fleet/app:v1_faulty`
- 独立命名空间 `fleet-ops` 有一个稳态探针 Pod（lab-up 等待信号用）：不在 fleet 表内、不参与验收；
  若模型列出全集群资源时可见，容忍（非主因）

## 应出现

- 工具链含一次**全表** `kubectl get appfleets -n fleet`（≈3000 行），且该观察的 Summary 呈
  **map-reduce 分片形态**：全局频次头 + 片节（区间 + 报数）+ 全表 0 基行号——① 的直接过程观测
- 结论指出 CrashLoopBackOff 实例**存在**（30 个或「约 30」量级，来自频次/报数而非编造）
- 受影响实例的位置以**片区间 / 簇 / 名单**概括（如「每 100 行第 ~97 位」「app-XX97z 系列」），
  覆盖全表分布而非只报单例；不要求逐个列出全部 30 个
- 至少一次 drill 跟进：`evidence.read` 按片区间翻原文，或 `kubectl get/describe` 具体异常实例
- 根因指向共享坏 image tag（`v1_faulty`），基于 describe/get 的真实观察并**引用证据**（evidence id
  / 命令视图）

## 不应出现

- 「一切正常 / 全部健康」类结论（存在性漏 = 单遍断崖形态，本场景判 fail）
- 只见单例即宣称「就这一个异常」，无全表分布概括
- 无工具观察支撑的编造（如虚构节点故障、虚构重启原因）

## 可选对照（手动、非判据、不进 smoke-all）

unset 投影方法（默认 fast）重跑同 prompt：预期频次段仍见 CrashLoopBackOff（presence 保留）但
无片节地址、枚举受影响实例明显吃力/部分枚举——真路径上完成标志 ② 差异（1/0/0 vs 1/1/0）的
观察佐证，结果记录进步骤 plan 数据段。
