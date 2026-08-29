// Package acquire 承载主动取证决策的机械计算内核：贝叶斯信念更新、期望信息增益（EIG）、
// MSPRT 停止准则与富文本证据强度更新。
//
// 分工边界（与 architecture #19 同构，从工具输出推广到决策过程）：
// LLM 提供语义素材（假设、动作提议、判别矩阵、观测分类、(d,s) 强度判定），
// 本包只做算术——判断可聪明，决策必可验证。全部函数无状态、确定性、零外部依赖
// （不 import core/llm，假设对齐与 JSON 解析归接线层）；循环状态由编排持有（#16）。
//
// 数值口径：熵与 EIG 以 bit 计（log₂）；信念在对数域（自然对数）以 log-sum-exp 防下溢；
// 强度伪似然 ℓ = 2^(α·d·s)（对数线性形式，恒正无钳位；α = 每单位证据强度的 bit 证据权）。
//
// 依据：《创新点一-主动取证决策》§1–§6、§9（思考文档）。
package acquire

import "math"

// Options 决策参数；零值字段取默认。参数是实验变量（敏感性分析扫 α / P* / τ 等），
// 默认值只服务产品路径
type Options struct {
	// Alpha 强度更新灵敏度：每单位证据强度的 bit 证据权（ℓ = 2^(Alpha·d·s)）；<=0 默认 3
	Alpha float64
	// PStar supported 出口的后验阈值；<=0 默认 0.9
	PStar float64
	// A SPRT 后验优势比阈值 Λᵢⱼ = Pᵢ/Pⱼ；<=0 默认 19（经典 (1−0.05)/0.05）
	A float64
	// Tau 信息平台阈值：max EIG 低于它（bit）即现有假设区分不动；<=0 默认 0.01
	Tau float64
	// Delta 全局意外阈值：观测在所有假设下的最大似然概率低于它即触发意外标志（供 abduction）；<=0 默认 0.05
	Delta float64
	// MassFloor refuted 出口的假设空间累计保留质量下限；<=0 默认 0.05
	MassFloor float64
}

// withDefaults 归一非法字段并填充默认参数，返回有效副本
// 非法 = 非正或非有限：NaN 与 x<=0 比较为 false 会穿透旧判断，+Inf 同样穿透——
// NaN 经算术污染后验（Inf−Inf），Inf 语义级污染（Tau=Inf 恒报平台、MassFloor=Inf 恒报
// refuted、A=Inf 关闭 Λ 出口），全部静默错（pr-agent 四轮）
func (o Options) withDefaults() Options {
	sanitize := func(v, def float64) float64 {
		if !(v > 0) || math.IsInf(v, 0) { // !(v>0) 同时覆盖 NaN 与零/负
			return def
		}
		return v
	}
	o.Alpha = sanitize(o.Alpha, 3)
	o.PStar = sanitize(o.PStar, 0.9)
	o.A = sanitize(o.A, 19)
	o.Tau = sanitize(o.Tau, 0.01)
	o.Delta = sanitize(o.Delta, 0.05)
	o.MassFloor = sanitize(o.MassFloor, 0.05)
	return o
}

// logSumExp 对数域求和：log(Σ exp(xi))；全为 −Inf 时返回 −Inf
// 数值稳定的移位实现：减去最大值防上溢，−Inf 项自然归零
func logSumExp(xs []float64) float64 {
	m := math.Inf(-1)
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	if math.IsInf(m, -1) {
		return m
	}
	var sum float64
	for _, x := range xs {
		sum += math.Exp(x - m)
	}
	return m + math.Log(sum)
}
