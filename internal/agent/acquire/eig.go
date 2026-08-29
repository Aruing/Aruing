// 期望信息增益（EIG）与动作选择：argmax EIG(a)/c(a)
//
// EIG(a) = H(P) − E_{o~P(o|a)}[H(P(·|o))]（bit）——动作的「好」= 各种可能结果
// 造成的信念更新幅度的期望；铁证型动作自动高于赌博型（思考文档 §4）

package acquire

import (
	"errors"
	"math"
)

// ErrBadAction 动作构造非法（矩阵形状与结果类别或假设数不符、出现负概率）
var ErrBadAction = errors.New("acquire: action matrix must be [hypotheses x outcomes] with non-negative entries")

// Action 一个候选取证动作：判别矩阵 + 成本
//
// 判别矩阵 D[o=hᵢ] = P(outcomes[j] | hᵢ) 由 Planner 结构化输出（语义预判显式化，
// 思考文档 §3.2）；行在构造时按和归一（LLM 输出轻微不归一时的容错），全零行保持全零
// （该假设认为动作无任何可能结果）。成本 c(a) 是 token / 延迟 / 交互的加权和，
// 问用户类动作由调用方注入放大成本（≈10×，统一建模无特判，§6）
type Action struct {
	// 动作名（人读标识，不参与计算）
	Name string
	// 结果类别（判别矩阵列键；顺序即矩阵列序）
	Outcomes []string
	// D[i][j] = P(Outcomes[j] | hᵢ)，行与 Belief 假设索引对齐
	D [][]float64
	// 成本；<=0 视为 1
	Cost float64
}

// NewAction 校验并构造动作：矩阵行数 = 假设数、列数 = 结果数、元素非负；
// 每行按和归一（和为正时）
func NewAction(name string, outcomes []string, d [][]float64, cost float64) (Action, error) {
	if len(outcomes) == 0 || len(d) == 0 {
		return Action{}, ErrBadAction
	}
	for _, row := range d {
		if len(row) != len(outcomes) {
			return Action{}, ErrBadAction
		}
	}
	a := Action{Name: name, Outcomes: append([]string(nil), outcomes...), D: make([][]float64, len(d)), Cost: cost}
	if a.Cost <= 0 {
		a.Cost = 1
	}
	for i, row := range d {
		a.D[i] = make([]float64, len(row))
		var sum float64
		for j, v := range row {
			// 非有限值一律拒绝：+Inf 会在行归一时变 Inf/Inf=NaN 静默污染下游（pr-agent #119 二轮）
			if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
				return Action{}, ErrBadAction
			}
			a.D[i][j] = v
			sum += v
		}
		// 有限元素求和溢出同样拒绝：finite/Inf=0 会把整行静默清零
		if math.IsInf(sum, 0) {
			return Action{}, ErrBadAction
		}
		if sum > 0 {
			for j := range a.D[i] {
				a.D[i][j] /= sum
			}
		}
	}
	return a, nil
}

// outcomeIndex 结果类别的列号；不存在返回 -1
func (a Action) outcomeIndex(outcome string) int {
	for j, o := range a.Outcomes {
		if o == outcome {
			return j
		}
	}
	return -1
}

// EIG 动作在当前信念下的期望信息增益（bit）
// 矩阵行数与信念假设数不符返回 ErrMisaligned（缺行会被当作概率 1 预测，必须显式拒绝）。
// 边缘概率为零的结果不产生更新期望（跳过，不除零）；可达结果的总边缘质量为零
// （所有假设对所有结果概率为零，动作无预测）时返回 0——无信息动作，argmax 自然不选。
// 个别假设行全零时总质量缺损，期望按可达结果的质量归一（良好矩阵下总量为 1，不变）
func EIG(b Belief, act Action) (float64, error) {
	if len(act.D) != b.Len() {
		return 0, ErrMisaligned
	}
	h0 := b.EntropyBits()
	var expected, totalMass float64
	for j := range act.Outcomes {
		logJoint := make([]float64, b.Len())
		for i := range logJoint {
			logJoint[i] = math.Log(act.D[i][j]) + b.logp[i]
		}
		logM := logSumExp(logJoint)
		if math.IsInf(logM, -1) {
			continue // 零概率结果：对期望无贡献
		}
		m := math.Exp(logM)
		totalMass += m
		// 该结果下的后验熵
		post := Belief{logp: normalizeLog(logJoint)}
		expected += m * post.EntropyBits()
	}
	if totalMass <= 0 {
		return 0, nil
	}
	return h0 - expected/totalMass, nil
}

// Selection 一次评估的结果：成本归一最优 + 全候选最大 EIG
//
// 两个口径刻意分开（pr-agent #119 评审修正）：选择按 EIG/c（便宜优先），
// 信息平台检测按全候选最大原始 EIG——不等成本下最优动作的 EIG 可以很小，
// 但只要存在任一高 EIG 候选就不算「区分不动」
type Selection struct {
	// 成本归一最优动作索引；空集为 -1
	Best int
	// 最优动作的 EIG/c 得分
	BestScore float64
	// 全候选最大原始 EIG（bit）——CheckStop 平台检测的口径
	MaxEIG float64
}

// Select 评估候选动作集：argmax EIG(a)/c(a)（并列取索引小者，确定性），
// 同时返回全候选最大 EIG。任一动作矩阵与信念不对齐返回 ErrMisaligned（整集拒绝，
// 不静默跳过——不对齐是调用方 bug，须暴露）
func Select(b Belief, acts []Action) (Selection, error) {
	sel := Selection{Best: -1}
	for i, a := range acts {
		g, err := EIG(b, a)
		if err != nil {
			return Selection{}, err
		}
		if g > sel.MaxEIG {
			sel.MaxEIG = g
		}
		if score := g / a.Cost; sel.Best < 0 || score > sel.BestScore {
			sel.Best, sel.BestScore = i, score
		}
	}
	return sel, nil
}
