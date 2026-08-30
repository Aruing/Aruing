// 期望信息增益（EIG）与动作选择：argmax EIG(a)/c(a)
//
// EIG(a) = H(P) − E_{o~P(o|a)}[H(P(·|o))]（bit）——动作的「好」= 各种可能结果
// 造成的信念更新幅度的期望；铁证型动作自动高于赌博型（思考文档 §4）

package acquire

import (
	"errors"
	"math"
)

// ErrBadAction 动作构造非法（矩阵形状与结果类别或假设数不符、负/非有限概率、结果名重复）
var ErrBadAction = errors.New("acquire: action matrix must be [hypotheses x outcomes] with non-negative finite entries and unique outcome names")

// Action 一个候选取证动作：判别矩阵 + 成本
//
// 判别矩阵 d[i][j] = P(outcomes[j] | hᵢ) 由 Planner 结构化输出（语义预判显式化，
// 思考文档 §3.2）。字段私有、经 NewAction 强制构造——全部不变量（形状、非负有限、
// 行归一、成本有效、结果名唯一）集中在构造器一处维护，绕过校验的路径不存在。
// 这是 pr-agent #119 三轮评审反复发现的根因修复：此前字段导出，消费点逐条打补丁
// 挡不住新的绕过路径（Belief 字段一直私有，故从未被发现同类问题）。
// 零值 Action 在全部消费点安全返回 ErrMisaligned / 未知结果错误，不 panic 不出非法数值。
// 成本 c(a) 是 token / 延迟 / 交互的加权和，问用户类动作由调用方注入放大成本
// （≈10×，统一建模无特判，§6）
type Action struct {
	name     string
	outcomes []string
	d        [][]float64
	cost     float64
}

// NewAction 校验并构造动作：矩阵行数 = 假设数、列数 = 结果数、元素非负有限、
// 结果类别名唯一（重名会让更新静默错位到首个同名列）；每行按和归一
// （和为正时，LLM 输出轻微不归一的容错），全零行保持全零（该假设认为动作无任何
// 可能结果）；成本非正或非有限（含 NaN/±Inf）归一为 1
func NewAction(name string, outcomes []string, d [][]float64, cost float64) (Action, error) {
	if len(outcomes) == 0 || len(d) == 0 {
		return Action{}, ErrBadAction
	}
	seen := make(map[string]struct{}, len(outcomes))
	for _, o := range outcomes {
		if _, dup := seen[o]; dup {
			return Action{}, ErrBadAction
		}
		seen[o] = struct{}{}
	}
	for _, row := range d {
		if len(row) != len(outcomes) {
			return Action{}, ErrBadAction
		}
	}
	a := Action{name: name, outcomes: append([]string(nil), outcomes...), d: make([][]float64, len(d))}
	if !(cost > 0) || math.IsInf(cost, 0) { // NaN > 0 为 false，一并归一
		a.cost = 1
	} else {
		a.cost = cost
	}
	for i, row := range d {
		a.d[i] = make([]float64, len(row))
		var sum float64
		for j, v := range row {
			// 非有限值一律拒绝：+Inf 会在行归一时变 Inf/Inf=NaN 静默污染下游（pr-agent 二轮）
			if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
				return Action{}, ErrBadAction
			}
			a.d[i][j] = v
			sum += v
		}
		// 有限元素求和溢出同样拒绝：finite/Inf=0 会把整行静默清零（pr-agent 二轮）
		if math.IsInf(sum, 0) {
			return Action{}, ErrBadAction
		}
		if sum > 0 {
			for j := range a.d[i] {
				a.d[i][j] /= sum
			}
		}
	}
	return a, nil
}

// Name 动作名（人读标识，不参与计算）
func (a Action) Name() string { return a.name }

// Outcomes 结果类别名（副本，顺序即矩阵列序）
func (a Action) Outcomes() []string { return append([]string(nil), a.outcomes...) }

// Matrix 判别矩阵副本：d[i][j] = P(Outcomes[j] | hᵢ)（诊断与记录用；改副本不影响内部）
func (a Action) Matrix() [][]float64 {
	out := make([][]float64, len(a.d))
	for i, row := range a.d {
		out[i] = append([]float64(nil), row...)
	}
	return out
}

// Cost 动作成本（构造期已归一为正有限值）
func (a Action) Cost() float64 { return a.cost }

// outcomeIndex 结果类别的列号；不存在返回 -1
func (a Action) outcomeIndex(outcome string) int {
	for j, o := range a.outcomes {
		if o == outcome {
			return j
		}
	}
	return -1
}

// EIG 动作在当前信念下的期望信息增益（bit）
// 矩阵行数与信念假设数不符返回 ErrMisaligned（缺行会被当作概率 1 预测，必须显式拒绝）。
// 边缘概率为零的结果不产生更新期望（跳过，不除零）。
// 部分假设行全零时（矩阵质量缺损），缺损质量按「观测无结果 = 信念不更新 = 保留全熵」
// 计入期望（pr-agent 三轮修正：按可达质量条件化会高估「只区分子集假设」的动作）；
// 全零矩阵因此自然返回 0——无信息动作，argmax 自然不选
func EIG(b Belief, act Action) (float64, error) {
	if len(act.d) != b.Len() {
		return 0, ErrMisaligned
	}
	h0 := b.EntropyBits()
	var expected, totalMass float64
	for j := range act.outcomes {
		logJoint := make([]float64, b.Len())
		for i := range logJoint {
			logJoint[i] = math.Log(act.d[i][j]) + b.logp[i]
		}
		logM := logSumExp(logJoint)
		if math.IsInf(logM, -1) {
			continue // 零概率结果：对期望无贡献
		}
		m := math.Exp(logM)
		// 该结果下的后验熵
		post := Belief{logp: normalizeLog(logJoint)}
		expected += m * post.EntropyBits()
		totalMass += m
	}
	// 不可达质量（1 − totalMass）不产生信念更新：按保留全熵计入期望，
	// 缺损动作的信息增益被诚实折半而不是条件化高估
	expected += (1 - totalMass) * h0
	return h0 - expected, nil
}

// Selection 一次评估的结果：成本归一最优 + 全候选最大 EIG
//
// 两个口径刻意分开（pr-agent #119 一轮评审修正）：选择按 EIG/c（便宜优先），
// 信息平台检测按全候选最大原始 EIG——不等成本下最优动作的 EIG 可以很小，
// 但只要存在任一高 EIG 候选就不算「区分不动」
type Selection struct {
	// 成本归一最优动作索引；空集为 -1
	Best int
	// 最优动作的 EIG/c 得分；近零成本动作为 +Inf（成本归一的设计语义：
	// 近零成本即近乎免费，理应支配全部有成本候选）；恒不为 NaN
	BestScore float64
	// 全候选最大原始 EIG（bit）——CheckStop 平台检测的口径
	MaxEIG float64
}

// Select 评估候选动作集：argmax EIG(a)/c(a)（并列取索引小者，确定性），
// 同时返回全候选最大 EIG。任一动作矩阵与信念不对齐返回 ErrMisaligned（整集拒绝，
// 不静默跳过——不对齐是调用方 bug，须暴露）；成本经构造器归一，此处恒为正有限
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
		if score := g / a.cost; sel.Best < 0 || score > sel.BestScore {
			sel.Best, sel.BestScore = i, score
		}
	}
	return sel, nil
}
