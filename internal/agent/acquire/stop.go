// MSPRT 停止准则：三条出口映射 Verdict 三值（语义不改，加连续置信度与有依据的触发时机）
//
//	supported    P(hᵢ) ≥ P*，或后验优势比 Λᵢⱼ = Pᵢ/Pⱼ ≥ A（先到先停；Wald SPRT 阈值）
//	refuted      假设空间累计保留质量 < MassFloor（观测序列在全部假设下的边缘似然坍缩
//	             ——「被强证据压死」的机械判据，触发重规划）
//	insufficient 信息平台（max EIG < τ：现有假设区分不动）或预算尽；带缺口说明
package acquire

// VerdictKind 停止出口的种类；未停止为零值
type VerdictKind int

const (
	// VerdictNone 未满足停止条件，继续取证
	VerdictNone VerdictKind = iota
	// VerdictSupported 收敛：某假设高置信胜出
	VerdictSupported
	// VerdictRefuted 排除：假设空间整体被证据压死，触发重规划
	VerdictRefuted
	// VerdictInsufficient 信息平台或预算尽：现有证据不足以收敛，带缺口
	VerdictInsufficient
)

// Stop 一次停止检查的结果
type Stop struct {
	// 是否停止
	Stop bool
	// 出口种类（Stop 为 false 时为 VerdictNone）
	Kind VerdictKind
	// 胜出假设索引（supported 时有效，其余 -1）
	Winner int
	// 停止时胜出假设的后验（supported 时的置信度）
	Confidence float64
	// 缺口说明（insufficient 时非空：平台还是预算尽）
	Gap string
}

// CheckStop 检查停止准则（思考文档 §5）。maxEIG 是当前候选动作集的最大原始 EIG（bit），
// 由调用方经 Select 取得（注意不是选中动作的 EIG——见 Selection.MaxEIG 口径说明）；
// budgetLeft <= 0 表示预算耗尽。
// 出口判定次序：supported → refuted → insufficient
func CheckStop(b Belief, maxEIG float64, budgetLeft int, o Options) Stop {
	d := o.withDefaults()
	post := b.Posterior()

	// supported：P* 与优势比双判据，先到先停
	winner, best := 0, 0.0
	for i, p := range post {
		if p > best {
			winner, best = i, p
		}
	}
	if best >= d.PStar {
		return Stop{Stop: true, Kind: VerdictSupported, Winner: winner, Confidence: best}
	}
	if oddsSatisfied(post, winner, d.A) {
		return Stop{Stop: true, Kind: VerdictSupported, Winner: winner, Confidence: best}
	}

	// refuted：假设空间累计保留质量坍缩（预测序列整体失败）
	if b.Mass() < d.MassFloor {
		return Stop{Stop: true, Kind: VerdictRefuted, Winner: -1}
	}

	// insufficient：信息平台或预算尽
	if maxEIG < d.Tau {
		return Stop{
			Stop:   true,
			Kind:   VerdictInsufficient,
			Winner: -1,
			Gap:    "信息平台：候选动作最大期望信息增益不足（max EIG < τ）",
		}
	}
	if budgetLeft <= 0 {
		return Stop{
			Stop:   true,
			Kind:   VerdictInsufficient,
			Winner: -1,
			Gap:    "预算尽：取证动作次数已达上限",
		}
	}
	return Stop{Winner: -1}
}

// oddsSatisfied 胜出假设对其余全部假设的后验优势比是否都 ≥ A（Λᵢⱼ 判据）
// 对手概率为零视为优势无穷（满足）
func oddsSatisfied(post []float64, winner int, a float64) bool {
	for j, pj := range post {
		if j == winner {
			continue
		}
		if pj > 0 && post[winner]/pj < a {
			return false
		}
	}
	return len(post) > 1
}
