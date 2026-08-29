// 信念状态：对数域概率 + 假设空间累计保留质量；贝叶斯更新（离散结果 / 富文本强度两路）

package acquire

import (
	"errors"
	"math"
)

// ErrBadBelief 信念构造非法（空假设空间 / 非正权重）
var ErrBadBelief = errors.New("acquire: belief requires at least one hypothesis with positive weight")

// ErrImpossibleOutcome 观测结果在所有假设下概率为零：后验无定义
// 接线层可将其映射为全局意外 → abduction 重规划，而非崩溃
var ErrImpossibleOutcome = errors.New("acquire: observed outcome has zero probability under every hypothesis")

// ErrMisaligned 矩阵行数与假设数不符：静默截断会把缺行当作概率 1 预测，必须明确报错
// （NewAction 不感知假设数，对齐校验在信念相关的操作处做）
var ErrMisaligned = errors.New("acquire: matrix rows must align with belief hypotheses")

// Belief 假设空间上的信念状态（值类型，更新返回新值不改旧值）
//
// 对数域存储（自然对数）+ log-sum-exp 归一，长证据链不下溢；
// logMass 记录假设空间累计保留的对数质量（每次更新的归一化常数累乘，
// 即观测序列在该假设空间下的边缘似然）——反复预测失败会压低它，
// 是 refuted 出口「被强证据压死」的机械判据
type Belief struct {
	// 各假设的归一自然对数概率（与假设索引对齐）
	logp []float64
	// 假设空间累计保留质量的对数（初始 0；每次更新累加归一化常数的对数）
	logMass float64
}

// NewBelief 用正权重构造信念（归一化）；先验来源任意（均匀 / Planner 粗粒度），
// 先验不必准——几轮更新后影响衰减殆尽（思考文档 §3.1）
func NewBelief(weights []float64) (Belief, error) {
	if len(weights) == 0 {
		return Belief{}, ErrBadBelief
	}
	logp := make([]float64, len(weights))
	for i, w := range weights {
		if w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
			return Belief{}, ErrBadBelief
		}
		logp[i] = math.Log(w)
	}
	b := Belief{logp: logp}
	b.logp = normalizeLog(b.logp)
	return b, nil
}

// NewUniformBelief k 个假设的均匀先验
func NewUniformBelief(k int) (Belief, error) {
	if k <= 0 {
		return Belief{}, ErrBadBelief
	}
	w := make([]float64, k)
	for i := range w {
		w[i] = 1
	}
	return NewBelief(w)
}

// Len 假设数
func (b Belief) Len() int { return len(b.logp) }

// Posterior 概率快照（线性域，供展示 / 判停 / 记录）
func (b Belief) Posterior() []float64 {
	p := make([]float64, len(b.logp))
	for i, lp := range b.logp {
		p[i] = math.Exp(lp)
	}
	return p
}

// Mass 假设空间累计保留质量（线性域）：观测序列在假设空间下的边缘似然
func (b Belief) Mass() float64 { return math.Exp(b.logMass) }

// EntropyBits 信念熵 H(P) = −Σ p log₂ p（bit）；信息量的统一量纲
func (b Belief) EntropyBits() float64 {
	var h float64
	for _, lp := range b.logp {
		p := math.Exp(lp)
		if p > 0 {
			h -= p * (lp / math.Ln2)
		}
	}
	return h
}

// UpdateOutcome 离散结果更新：观测到动作 act 的结果 outcome，按判别矩阵列做贝叶斯
// P(hᵢ|o) ∝ D(o|hᵢ)·P(hᵢ)。第二返回值为全局意外标志（maxᵢ D(o|hᵢ) < δ，
// 供编排触发 abduction，本函数仍完成归一更新——旧假设保留、后验按证据重排）
func (o Options) UpdateOutcome(b Belief, act Action, outcome string) (Belief, bool, error) {
	if len(act.D) != b.Len() {
		return Belief{}, false, ErrMisaligned
	}
	j := act.outcomeIndex(outcome)
	if j < 0 {
		return Belief{}, false, errors.New("acquire: unknown outcome " + outcome)
	}
	logD := make([]float64, b.Len())
	maxD := 0.0
	for i := 0; i < b.Len() && i < len(act.D); i++ {
		logD[i] = math.Log(act.D[i][j])
		if act.D[i][j] > maxD {
			maxD = act.D[i][j]
		}
	}
	logJoint := make([]float64, b.Len())
	for i := range logJoint {
		logJoint[i] = logD[i] + b.logp[i]
	}
	logMarginal := logSumExp(logJoint)
	if math.IsInf(logMarginal, -1) {
		return Belief{}, false, ErrImpossibleOutcome
	}
	return Belief{
		logp:    normalizeLog(logJoint),
		logMass: b.logMass + logMarginal,
	}, maxD < o.withDefaults().Delta, nil
}

// UpdateStrength 富文本证据强度更新（logs/describe 类无法离散化的证据）：
// Verifier 对每对 (E, hᵢ) 给方向与强度，伪似然 ℓᵢ = 2^(α·d·s)（对数线性，恒正）
// P(hᵢ|E) ∝ ℓᵢ·P(hᵢ)。第二返回值为全局意外标志（max ℓᵢ < δ：单条证据强烈反驳全部假设）
func (o Options) UpdateStrength(b Belief, e StrengthEvidence) (Belief, bool, error) {
	if len(e.D) != b.Len() || len(e.S) != b.Len() {
		return Belief{}, false, ErrMisaligned
	}
	d := o.withDefaults()
	logL := make([]float64, b.Len())
	maxL := 0.0
	for i := range logL {
		dd, s := e.D[i], clamp01(e.S[i])
		logL[i] = (d.Alpha * float64(dd) * s) * math.Ln2
		if l := math.Exp(logL[i]); l > maxL {
			maxL = l
		}
	}
	logJoint := make([]float64, b.Len())
	for i := range logJoint {
		logJoint[i] = logL[i] + b.logp[i]
	}
	logMarginal := logSumExp(logJoint)
	if math.IsInf(logMarginal, -1) {
		// 每个假设 ℓ>0，联合恒正；到这说明信念本身已退化（不可达防御）
		return Belief{}, false, ErrImpossibleOutcome
	}
	return Belief{
		logp:    normalizeLog(logJoint),
		logMass: b.logMass + logMarginal,
	}, maxL < d.Delta, nil
}

// StrengthEvidence 一条富文本证据对各假设的判定（与假设索引对齐）
// 方向取值 +1 支持 / 0 无关 / −1 反驳；枚举字符串解析归接线层，本包只收数值
type StrengthEvidence struct {
	// 每假设方向：+1 / 0 / −1
	D []int
	// 每假设强度：[0,1]，越界钳位
	S []float64
}

// normalizeLog 对数域归一：减去 logSumExp 使 Σexp = 1
func normalizeLog(logp []float64) []float64 {
	ls := logSumExp(logp)
	out := make([]float64, len(logp))
	for i, lp := range logp {
		out[i] = lp - ls
	}
	return out
}

// clamp01 强度钳位到 [0,1]（LLM 输出容错；方向不钳——非法方向是接线层解析错误）
func clamp01(s float64) float64 {
	if s < 0 {
		return 0
	}
	if s > 1 {
		return 1
	}
	return s
}
