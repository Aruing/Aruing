package acquire

import (
	"math"
	"testing"
)

// §7 数值算例（svc-wrong-selector）复算：EIG 排序、贝叶斯后验、MSPRT 停止、两次动作收敛
// 文档展示值断言容差 1e-2（文档保留位数）；内部一致性另以紧容差断言
func TestWorkedExampleSvcWrongSelector(t *testing.T) {
	opts := Options{}
	b, err := NewBelief([]float64{0.40, 0.40, 0.20})
	if err != nil {
		t.Fatalf("belief: %v", err)
	}
	if h := b.EntropyBits(); math.Abs(h-1.522) > 1e-2 {
		t.Fatalf("初始熵应 ≈1.522 bit，got %.4f", h)
	}

	a1 := mustAction(t, "get pods", []string{"Running", "CrashLoop", "NotFound"}, [][]float64{
		{.85, .05, .10},
		{.03, .92, .05},
		{.40, .10, .50},
	}, 1)
	a2 := mustAction(t, "get endpoints", []string{"空", "非空"}, [][]float64{
		{.90, .10},
		{.75, .25},
		{.45, .55},
	}, 1)
	a3 := mustAction(t, "get svc -A", []string{"重名", "唯一"}, [][]float64{
		{.05, .95},
		{.05, .95},
		{.80, .20},
	}, 2)

	// EIG 与文档展示值对齐（0.71 / 0.10 / 0.35 bit）
	for _, c := range []struct {
		act  Action
		want float64
	}{
		{a1, 0.71}, {a2, 0.10}, {a3, 0.35},
	} {
		got, gerr := EIG(b, c.act)
		if gerr != nil {
			t.Fatalf("EIG(%s): %v", c.act.Name(), gerr)
		}
		if math.Abs(got-c.want) > 1e-2 {
			t.Fatalf("EIG(%s) 应 ≈%.2f bit，got %.4f", c.act.Name(), c.want, got)
		}
	}

	// 成本归一选择：a₁ 的 EIG/c 最高；MaxEIG 同为 a₁（此例一致）
	sel, err := Select(b, []Action{a2, a3, a1})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if sel.Best != 2 || sel.MaxEIG < 0.6 {
		t.Fatalf("应选 a₁（索引 2），got %+v", sel)
	}

	// 观测 CrashLoop → 后验 (0.049, 0.902, 0.049)，无意外标志
	b2, surprise, err := opts.UpdateOutcome(b, a1, "CrashLoop")
	if err != nil || surprise {
		t.Fatalf("更新失败或误报意外：%v surprise=%v", err, surprise)
	}
	post := b2.Posterior()
	want := []float64{0.049, 0.902, 0.049}
	for i, p := range post {
		if math.Abs(p-want[i]) > 1e-2 {
			t.Fatalf("后验[%d] 应 ≈%.3f，got %.4f（全量 %v）", i, want[i], p, post)
		}
	}

	// P* 刚过线：0.902 ≥ 0.9 即 supported（「确证再停」是接线层策略，非本包职责）
	stop := CheckStop(b2, 0.5, 3, opts)
	if !stop.Stop || stop.Kind != VerdictSupported || stop.Winner != 1 || math.Abs(stop.Confidence-0.902) > 1e-2 {
		t.Fatalf("应 supported h₂（置信 ≈0.902），got %+v", stop)
	}

	// 确证步：a₄ logs（h₂ 真则必现 ImagePull）→ 置信 ≈0.988，两次动作收敛
	a4 := mustAction(t, "logs pod", []string{"ImagePull", "其他"}, [][]float64{
		{.05, .95},
		{.95, .05},
		{.05, .95},
	}, 1)
	g4, gerr4 := EIG(b2, a4)
	if gerr4 != nil || g4 < 0.1 {
		t.Fatalf("确证动作 EIG 应显著（文档「仍高」），got %.3f err=%v", g4, gerr4)
	}
	b3, _, err := opts.UpdateOutcome(b2, a4, "ImagePull")
	if err != nil {
		t.Fatalf("确证更新: %v", err)
	}
	p3 := b3.Posterior()
	if math.Abs(p3[1]-0.988) > 1e-2 {
		t.Fatalf("确证后 h₂ 置信应 ≈0.988，got %.4f", p3[1])
	}
	stop3 := CheckStop(b3, 0.5, 1, opts)
	if !stop3.Stop || stop3.Winner != 1 || stop3.Confidence < 0.98 {
		t.Fatalf("两次动作后应高置信停止，got %+v", stop3)
	}
}

// 对数域与朴素线性域小规模互验：同输入同输出、公式实现一致
func TestLogDomainCrossCheck(t *testing.T) {
	b, _ := NewBelief([]float64{0.4, 0.4, 0.2})
	a := mustAction(t, "a", []string{"x", "y"}, [][]float64{
		{.7, .3}, {.2, .8}, {.5, .5},
	}, 1)
	// 朴素线性域后验
	p := b.Posterior()
	wx := .7*p[0] + .2*p[1] + .5*p[2]
	naive := []float64{.7 * p[0] / wx, .2 * p[1] / wx, .5 * p[2] / wx}
	got, _, err := Options{}.UpdateOutcome(b, a, "x")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	for i, g := range got.Posterior() {
		if math.Abs(g-naive[i]) > 1e-12 {
			t.Fatalf("对数域与线性域不一致：[%d] %.15f vs %.15f", i, g, naive[i])
		}
	}
	// 确定性：重复调用逐位一致
	got2, _, _ := Options{}.UpdateOutcome(b, a, "x")
	for i := range got.logp {
		if got.logp[i] != got2.logp[i] {
			t.Fatalf("同输入应逐位一致")
		}
	}
}

// 信念构造边界：空假设 / 非法权重明确报错；归一性在更新链上保持
func TestBeliefEdges(t *testing.T) {
	if _, err := NewUniformBelief(0); err == nil {
		t.Fatalf("空假设空间应报错")
	}
	if _, err := NewBelief([]float64{0.5, -0.1, 0.6}); err == nil {
		t.Fatalf("负权重应报错")
	}
	b, err := NewBelief([]float64{2, 3, 5})
	if err != nil {
		t.Fatalf("belief: %v", err)
	}
	sum := 0.0
	for _, p := range b.Posterior() {
		sum += p
	}
	if math.Abs(sum-1) > 1e-12 {
		t.Fatalf("先验应归一，got Σ=%.15f", sum)
	}
	// 更新链上 Σp = 1 恒成立
	a := mustAction(t, "a", []string{"x", "y"}, [][]float64{{.9, .1}, {.1, .9}, {.4, .6}}, 1)
	cur := b
	for range 5 {
		cur, _, err = Options{}.UpdateOutcome(cur, a, "x")
		if err != nil {
			t.Fatalf("update: %v", err)
		}
		sum = 0
		for _, p := range cur.Posterior() {
			sum += p
		}
		if math.Abs(sum-1) > 1e-12 {
			t.Fatalf("更新后应保持归一，got Σ=%.15f", sum)
		}
	}
}

// 单假设退化：EIG 恒 0、立即 supported
func TestSingleHypothesis(t *testing.T) {
	b, _ := NewUniformBelief(1)
	a := mustAction(t, "a", []string{"x", "y"}, [][]float64{{.9, .1}}, 1)
	if g, err := EIG(b, a); err != nil || g != 0 {
		t.Fatalf("单假设 EIG 应为 0，got %f err=%v", g, err)
	}
	stop := CheckStop(b, 0, 1, Options{})
	if !stop.Stop || stop.Kind != VerdictSupported || stop.Winner != 0 {
		t.Fatalf("单假设应立即 supported，got %+v", stop)
	}
}

// 全零矩阵动作 EIG=0；argmax 不选无信息动作；全零集合信息平台出口
func TestZeroMatrixAction(t *testing.T) {
	b, _ := NewUniformBelief(3)
	zero := mustAction(t, "zero", []string{"x", "y"}, [][]float64{{0, 0}, {0, 0}, {0, 0}}, 1)
	if g, err := EIG(b, zero); err != nil || g != 0 {
		t.Fatalf("全零矩阵 EIG 应为 0，got %f err=%v", g, err)
	}
	good := mustAction(t, "good", []string{"x", "y"}, [][]float64{{.9, .1}, {.1, .9}, {.5, .5}}, 1)
	if sel, err := Select(b, []Action{zero, good}); err != nil || sel.Best != 1 {
		t.Fatalf("应选有信息动作，got %+v err=%v", sel, err)
	}
	sel, _ := Select(b, []Action{zero, zero})
	if sel.Best != 0 || sel.MaxEIG != 0 {
		t.Fatalf("全零集合应返回首动作且 EIG=0，got %+v", sel)
	}
	stop := CheckStop(b, sel.MaxEIG, 3, Options{})
	if !stop.Stop || stop.Kind != VerdictInsufficient {
		t.Fatalf("max EIG=0 应信息平台 insufficient，got %+v", stop)
	}
}

// 不可能结果：所有假设下概率为零 → 明确报错（接线层映射 abduction），不除零不崩溃
func TestImpossibleOutcome(t *testing.T) {
	b, _ := NewUniformBelief(2)
	a := mustAction(t, "a", []string{"x", "y"}, [][]float64{{1, 0}, {1, 0}}, 1)
	if _, _, err := (Options{}).UpdateOutcome(b, a, "y"); err == nil {
		t.Fatalf("零概率结果应报错")
	}
	if _, _, err := (Options{}).UpdateOutcome(b, a, "nope"); err == nil {
		t.Fatalf("未知结果类别应报错")
	}
}

// 强度更新：方向正确倾斜、α=0 不动、对称 ×8/÷8、假设不归零
func TestStrengthUpdate(t *testing.T) {
	b, _ := NewBelief([]float64{0.4, 0.4, 0.2})
	opts := Options{} // α=3

	// 只反驳 h₂（s=1）：ℓ=2⁻³=1/8，相对权重压 8 倍
	got, _, err := opts.UpdateStrength(b, StrengthEvidence{D: []int{0, -1, 0}, S: []float64{0, 1, 0}})
	if err != nil {
		t.Fatalf("strength: %v", err)
	}
	p := got.Posterior()
	// h₁:h₂ 相对比应从 1:1 变 8:1
	if ratio := p[0] / p[1]; math.Abs(ratio-8) > 1e-9 {
		t.Fatalf("s=1 反驳应 ×8 相对压制，ratio=%.6f", ratio)
	}
	if p[1] <= 0 {
		t.Fatalf("假设不得归零，got %v", p)
	}

	// α 零值/负值走默认；全无关证据（d=0）后验不变
	same, _, _ := Options{Alpha: -1}.UpdateStrength(b, StrengthEvidence{D: []int{0, 0, 0}, S: []float64{0, 0, 0}})
	for i, q := range same.Posterior() {
		if math.Abs(q-b.Posterior()[i]) > 1e-12 {
			t.Fatalf("全无关证据不应改变信念")
		}
	}

	// d=+1 满强度对称：支持 h₃ → h₃:h₁ 相对比 ×8
	got, _, _ = opts.UpdateStrength(b, StrengthEvidence{D: []int{0, 0, 1}, S: []float64{0, 0, 1}})
	p = got.Posterior()
	if ratio := (p[2] / .2) / (p[0] / .4); math.Abs(ratio-8) > 1e-9 {
		t.Fatalf("s=1 支持应 ×8 相对抬升，ratio=%.6f", ratio)
	}
}

// refuted 出口：全体假设被反复强反驳 → 假设空间保留质量坍缩
func TestRefutedByMassCollapse(t *testing.T) {
	b, _ := NewUniformBelief(3)
	opts := Options{} // MassFloor 0.05
	cur := b
	var err error
	// 每条满强度反驳全体：ℓ=1/8，边缘质量 ×0.125；五条后 ≈3e-5 < 0.05
	for i := 0; i < 5; i++ {
		cur, _, err = opts.UpdateStrength(cur, StrengthEvidence{
			D: []int{-1, -1, -1}, S: []float64{1, 1, 1},
		})
		if err != nil {
			t.Fatalf("strength: %v", err)
		}
	}
	if cur.Mass() >= 0.05 {
		t.Fatalf("五条满强度反驳后质量应坍缩，got %.2e", cur.Mass())
	}
	stop := CheckStop(cur, 0.5, 3, opts)
	if !stop.Stop || stop.Kind != VerdictRefuted {
		t.Fatalf("质量坍缩应 refuted，got %+v", stop)
	}
}

// Λᵢⱼ 优势比出口：k=5 时 Λ 先于 P* 触发（0.83 < 0.9 但对全部对手优势 ≥19）
func TestStopOddsExit(t *testing.T) {
	b, _ := NewBelief([]float64{0.83, 0.0425, 0.0425, 0.0425, 0.0425})
	stop := CheckStop(b, 0.5, 3, Options{})
	if !stop.Stop || stop.Kind != VerdictSupported || stop.Winner != 0 {
		t.Fatalf("Λ 判据应触发 supported，got %+v", stop)
	}
	// 未达双判据的对照：0.82 对 0.045 优势 18.2 < 19
	b2, _ := NewBelief([]float64{0.82, 0.045, 0.045, 0.045, 0.045})
	if stop := CheckStop(b2, 0.5, 3, Options{}); stop.Stop && stop.Kind == VerdictSupported {
		t.Fatalf("优势 18.2 不应触发 Λ 出口")
	}
}

// insufficient 两形态：信息平台（maxEIG < τ）与预算尽，缺口说明区分
func TestStopInsufficient(t *testing.T) {
	b, _ := NewBelief([]float64{0.5, 0.3, 0.2})
	plateau := CheckStop(b, 0.001, 3, Options{})
	if !plateau.Stop || plateau.Kind != VerdictInsufficient || plateau.Gap == "" {
		t.Fatalf("信息平台应 insufficient 带缺口，got %+v", plateau)
	}
	budget := CheckStop(b, 0.5, 0, Options{})
	if !budget.Stop || budget.Kind != VerdictInsufficient || budget.Gap == "" {
		t.Fatalf("预算尽应 insufficient 带缺口，got %+v", budget)
	}
}

// 全局意外：观测在所有假设下概率极低 → 意外标志（供 abduction），更新本身仍完成
func TestGlobalSurprise(t *testing.T) {
	b, _ := NewUniformBelief(3)
	// 列 [0.01, 0.02, 0.03]：max=0.03 < δ=0.05
	a := mustAction(t, "a", []string{"x", "意外"}, [][]float64{
		{0.99, 0.01}, {0.98, 0.02}, {0.97, 0.03},
	}, 1)
	got, surprise, err := (Options{}).UpdateOutcome(b, a, "意外")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !surprise {
		t.Fatalf("max D=0.03 < δ 应触发意外标志")
	}
	if got.Len() != 3 {
		t.Fatalf("意外时更新仍应完成（旧假设保留）")
	}
	// 强度路径：α=5 满强度反驳全体 → ℓ=2⁻⁵≈0.031 < δ
	b2, _ := NewUniformBelief(2)
	_, surprise2, err := (Options{Alpha: 5}).UpdateStrength(b2, StrengthEvidence{D: []int{-1, -1}, S: []float64{1, 1}})
	if err != nil || !surprise2 {
		t.Fatalf("α=5 满强度反驳应触发意外标志：%v %v", err, surprise2)
	}
}

// 动作构造校验：形状不符 / 负概率报错；行和轻微不归一时的容错归一
func TestNewActionValidation(t *testing.T) {
	if _, err := NewAction("bad", []string{"x"}, [][]float64{{0.5, 0.5}}, 1); err == nil {
		t.Fatalf("列数不符应报错")
	}
	if _, err := NewAction("bad", []string{"x", "y"}, [][]float64{{0.5, -0.1}}, 1); err == nil {
		t.Fatalf("负概率应报错")
	}
	// +Inf 入口：归一会变 NaN 静默污染下游，构造期必须拒绝
	if _, err := NewAction("inf", []string{"x", "y"}, [][]float64{{math.Inf(1), 0}}, 1); err == nil {
		t.Fatalf("+Inf 概率应报错")
	}
	// 有限但求和溢出：finite/Inf 会把行静默清零，同样拒绝
	if _, err := NewAction("oflow", []string{"x", "y"}, [][]float64{{1e308, 1e308}}, 1); err == nil {
		t.Fatalf("行和溢出应报错")
	}
	// 行和 0.9（LLM 输出容差）→ 构造时归一，D 用作似然不偏置
	a, err := NewAction("tol", []string{"x", "y"}, [][]float64{{0.72, 0.18}}, 1)
	if err != nil {
		t.Fatalf("tol: %v", err)
	}
	if m := a.Matrix(); m[0][0] != 0.8 || m[0][1] != 0.2 {
		t.Fatalf("行应按和归一，got %v", m[0])
	}
	// 零成本归一为 1（评分除法防护）
	if a.Cost() != 1 {
		t.Fatalf("零成本应归一为 1，got %f", a.Cost())
	}
	// NaN / ±Inf 成本归一为 1（NaN > 0 为 false，经非正分支一并归一）
	for _, c := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0, -2} {
		a, err := NewAction("c", []string{"x"}, [][]float64{{1}}, c)
		if err != nil || a.Cost() != 1 {
			t.Fatalf("非法成本 %v 应归一为 1，got %f err=%v", c, a.Cost(), err)
		}
	}
	// 结果名重复：更新会静默错位到首个同名列，构造期拒绝
	if _, err := NewAction("dup", []string{"x", "x"}, [][]float64{{.5, .5}}, 1); err == nil {
		t.Fatalf("重复结果名应报错")
	}
}

// 均匀三假设熵 = log₂3（bit 口径锚点）
func TestEntropyUniform(t *testing.T) {
	b, _ := NewUniformBelief(3)
	if h := b.EntropyBits(); math.Abs(h-math.Log2(3)) > 1e-12 {
		t.Fatalf("均匀三假设熵应 = log₂3，got %f", h)
	}
}

func mustAction(t *testing.T, name string, outcomes []string, d [][]float64, cost float64) Action {
	t.Helper()
	a, err := NewAction(name, outcomes, d, cost)
	if err != nil {
		t.Fatalf("action %s: %v", name, err)
	}
	return a
}

// 矩阵与信念不对齐：更新与评估都必须明确报错（缺行不得当作概率 1 预测）
func TestMisalignedMatrix(t *testing.T) {
	b, _ := NewUniformBelief(3)
	short := mustAction(t, "short", []string{"x", "y"}, [][]float64{{.9, .1}, {.1, .9}}, 1)
	if _, _, err := (Options{}).UpdateOutcome(b, short, "x"); err == nil {
		t.Fatalf("行数不足的更新应报错")
	}
	if _, err := EIG(b, short); err == nil {
		t.Fatalf("行数不足的 EIG 应报错")
	}
	if _, err := Select(b, []Action{short}); err == nil {
		t.Fatalf("含不对齐动作的 Select 应报错")
	}
}

// 选择与平台检测口径分离（pr-agent #119 评审修正）：低成本低 EIG 动作可被成本归一选中，
// 但只要存在高 EIG 候选，平台检测不算 insufficient
func TestSelectMaxEIGVersusBest(t *testing.T) {
	b, _ := NewUniformBelief(3)
	// cheap：弱区分（EIG ≈0.02）但成本极低；expensive：h₁ 与其余劈裂（EIG ≈0.61）
	cheap := mustAction(t, "cheap", []string{"x", "y"}, [][]float64{{.6, .4}, {.4, .6}, {.5, .5}}, 0.001)
	expensive := mustAction(t, "expensive", []string{"x", "y"}, [][]float64{{.99, .01}, {.01, .99}, {.5, .5}}, 1)
	sel, err := Select(b, []Action{cheap, expensive})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if sel.Best != 0 {
		t.Fatalf("成本归一应选 cheap，got %+v", sel)
	}
	if sel.MaxEIG < 0.5 {
		t.Fatalf("MaxEIG 应取 expensive 的高 EIG，got %+v", sel)
	}
	// 平台检测用 MaxEIG：τ=0.1 下选中动作自身 EIG（≈0.02）会误报平台，全候选最大 EIG 不报
	if stop := CheckStop(b, sel.MaxEIG, 3, Options{Tau: 0.1}); stop.Stop && stop.Kind == VerdictInsufficient {
		t.Fatalf("存在高 EIG 候选不应误报信息平台，got %+v", stop)
	}
}

// 封闭不变量：零值 Action（唯一可能的绕过形态）在全部消费点安全报错，不 panic
func TestZeroValueActionRejected(t *testing.T) {
	b, _ := NewUniformBelief(3)
	good := mustAction(t, "good", []string{"x", "y"}, [][]float64{{.99, .01}, {.01, .99}, {.5, .5}}, 1)
	var zero Action
	if _, err := EIG(b, zero); err == nil {
		t.Fatalf("零值动作 EIG 应报对齐错误")
	}
	if _, _, err := (Options{}).UpdateOutcome(b, zero, "x"); err == nil {
		t.Fatalf("零值动作更新应报错")
	}
	if _, err := Select(b, []Action{good, zero}); err == nil {
		t.Fatalf("含零值动作的 Select 应报错")
	}
	// 合法集合得分恒为有限正值（成本经构造器归一，无 Inf/NaN 得分路径）
	sel, err := Select(b, []Action{good})
	if err != nil || sel.BestScore <= 0 || math.IsInf(sel.BestScore, 0) || math.IsNaN(sel.BestScore) {
		t.Fatalf("合法集选择得分应为有限正值，got %+v err=%v", sel, err)
	}
}

// 强度证据值域校验：方向越界 / 强度非有限明确报错（自扫发现，clamp01 曾放行 NaN）
func TestStrengthValidation(t *testing.T) {
	b, _ := NewUniformBelief(2)
	if _, _, err := (Options{}).UpdateStrength(b, StrengthEvidence{D: []int{2, 0}, S: []float64{1, 0}}); err == nil {
		t.Fatalf("方向越界（d=2）应报错")
	}
	if _, _, err := (Options{}).UpdateStrength(b, StrengthEvidence{D: []int{1, 0}, S: []float64{math.NaN(), 0}}); err == nil {
		t.Fatalf("NaN 强度应报错")
	}
	if _, _, err := (Options{}).UpdateStrength(b, StrengthEvidence{D: []int{1, 0}, S: []float64{math.Inf(1), 0}}); err == nil {
		t.Fatalf("Inf 强度应报错")
	}
	// 有限越界强度照旧钳位：s=1.5 与 s=1 等效
	a, _, _ := (Options{}).UpdateStrength(b, StrengthEvidence{D: []int{1, 0}, S: []float64{1.5, 0}})
	c, _, _ := (Options{}).UpdateStrength(b, StrengthEvidence{D: []int{1, 0}, S: []float64{1, 0}})
	pa, pc := a.Posterior(), c.Posterior()
	for i := range pa {
		if math.Abs(pa[i]-pc[i]) > 1e-12 {
			t.Fatalf("有限越界强度应钳位等效：%v vs %v", pa, pc)
		}
	}
}

// 部分零行不高估（pr-agent 三轮）：只区分子集假设的动作信息增益按不可达质量诚实折半
func TestEIGPartialZeroRowNotOverranked(t *testing.T) {
	b, _ := NewBelief([]float64{0.5, 0.5})
	// partial：h₁ 完全区分（观测必为 x），h₂ 行全零（一半质量无更新）→ 诚实值 0.5 bit
	partial := mustAction(t, "partial", []string{"x", "y"}, [][]float64{{1, 0}, {0, 0}}, 1)
	g, err := EIG(b, partial)
	if err != nil {
		t.Fatalf("eig: %v", err)
	}
	if math.Abs(g-0.5) > 1e-9 {
		t.Fatalf("部分零行动作 EIG 应 ≈0.5 bit（一半质量无更新），got %f", g)
	}
	// 对照：完全区分动作 1 bit——部分零行不得与之并列（条件化口径下两者都是 1）
	full := mustAction(t, "full", []string{"x", "y"}, [][]float64{{1, 0}, {0, 1}}, 1)
	gf, _ := EIG(b, full)
	if math.Abs(gf-1) > 1e-9 {
		t.Fatalf("完全区分动作 EIG 应 =1 bit，got %f", gf)
	}
	if g >= gf {
		t.Fatalf("部分零行动作不得排在完全区分动作之前")
	}
}

// Options 非法字段（NaN / ±Inf / 非正）归默认：不得经算术产生 NaN 后验，
// 也不得语义级污染停止准则（Tau=Inf 恒报平台 / MassFloor=Inf 恒报 refuted / A=Inf 关 Λ）
func TestOptionsNonFiniteSanitized(t *testing.T) {
	b, _ := NewUniformBelief(2)

	// Alpha=+Inf / NaN：强度更新结果应与默认 Alpha 逐位一致（曾 Inf−Inf=NaN 污染后验）
	want, _, _ := Options{}.UpdateStrength(b, StrengthEvidence{D: []int{1, 0}, S: []float64{1, 0}})
	for _, bad := range []float64{math.Inf(1), math.Inf(-1), math.NaN(), 0, -3} {
		got, _, err := Options{Alpha: bad}.UpdateStrength(b, StrengthEvidence{D: []int{1, 0}, S: []float64{1, 0}})
		if err != nil {
			t.Fatalf("Alpha=%v: %v", bad, err)
		}
		for i, p := range got.Posterior() {
			if math.IsNaN(p) || math.Abs(p-want.Posterior()[i]) > 1e-15 {
				t.Fatalf("Alpha=%v 应归默认，got %v want %v", bad, got.Posterior(), want.Posterior())
			}
		}
	}

	// Tau=Inf 不得恒报信息平台；MassFloor=Inf 不得恒报 refuted
	if stop := CheckStop(b, 0.5, 3, Options{Tau: math.Inf(1)}); stop.Stop && stop.Kind == VerdictInsufficient {
		t.Fatalf("Tau=Inf 应归默认，不得恒报平台")
	}
	if stop := CheckStop(b, 0.5, 3, Options{MassFloor: math.Inf(1)}); stop.Stop && stop.Kind == VerdictRefuted {
		t.Fatalf("MassFloor=Inf 应归默认，不得恒报 refuted")
	}

	// A=NaN 不得关闭 Λ 出口：0.83/4×0.0425 的优势比判据照常触发
	five, _ := NewBelief([]float64{0.83, 0.0425, 0.0425, 0.0425, 0.0425})
	if stop := CheckStop(five, 0.5, 3, Options{A: math.NaN()}); !stop.Stop || stop.Kind != VerdictSupported {
		t.Fatalf("A=NaN 应归默认，Λ 出口照常，got %+v", stop)
	}
}

// Options 语义域校验：概率参数 ∈(0,1]、优势比阈值 >1——越域回默认，
// 不得静默禁用/恒触发停止出口（pr-agent 五轮）
func TestOptionsSemanticRange(t *testing.T) {
	b, _ := NewBelief([]float64{0.95, 0.04, 0.01})
	// PStar=1.5 曾使 P* 出口永不可达；归默认后 0.95 照常 supported
	if stop := CheckStop(b, 0.5, 3, Options{PStar: 1.5}); !stop.Stop || stop.Kind != VerdictSupported {
		t.Fatalf("PStar 越域应归默认，P* 出口照常，got %+v", stop)
	}
	// A=0.5 曾使 Λ 出口对任意信念恒真；归默认后均匀信念不得 supported
	u, _ := NewUniformBelief(3)
	if stop := CheckStop(u, 0.5, 3, Options{A: 0.5}); stop.Stop && stop.Kind == VerdictSupported {
		t.Fatalf("A 越域应归默认，均匀信念不得 supported，got %+v", stop)
	}
	// Delta=1.5 曾恒报意外；归默认后正常观测无意外标志
	a := mustAction(t, "a", []string{"x", "y"}, [][]float64{{.9, .1}, {.1, .9}, {.5, .5}}, 1)
	if _, surprise, err := (Options{Delta: 1.5}).UpdateOutcome(u, a, "x"); err != nil || surprise {
		t.Fatalf("Delta 越域应归默认，正常观测不得误报意外：%v %v", err, surprise)
	}
	// MassFloor=1.5 曾恒报 refuted；归默认后新鲜信念不得 refuted
	if stop := CheckStop(u, 0.5, 3, Options{MassFloor: 1.5}); stop.Stop && stop.Kind == VerdictRefuted {
		t.Fatalf("MassFloor 越域应归默认，不得恒报 refuted，got %+v", stop)
	}
}

// 极端 Alpha 强度链（pr-agent 六轮验证）：后验在对数域实现下恒合法（声称的 NaN 路径
// 不存在），但 Mass() 会溢出 +Inf——判停必须走对数域比较，refuted 出口不得被静默禁用
func TestExtremeAlphaMassOverflow(t *testing.T) {
	b, _ := NewBelief([]float64{0.4, 0.4, 0.2})
	for _, alpha := range []float64{1024, 2000, 1e6, 1e300, math.MaxFloat64} {
		got, _, err := Options{Alpha: alpha}.UpdateStrength(b, StrengthEvidence{
			D: []int{+1, -1, 0}, S: []float64{1, 1, 0},
		})
		if err != nil {
			t.Fatalf("alpha=%v: %v", alpha, err)
		}
		p := got.Posterior()
		sum := 0.0
		for _, q := range p {
			if math.IsNaN(q) || math.IsInf(q, 0) {
				t.Fatalf("alpha=%v 后验非法 %v", alpha, p)
			}
			sum += q
		}
		if math.Abs(sum-1) > 1e-9 || p[0] <= p[1] {
			t.Fatalf("alpha=%v 后验应归一且支持方向胜出 %v", alpha, p)
		}
		// 溢出的 Mass 不得禁用 refuted 出口：极强确证后照常检查（不误报 refuted）
		if stop := CheckStop(got, 0.5, 3, Options{}); stop.Stop && stop.Kind == VerdictRefuted {
			t.Fatalf("alpha=%v 极强确证链不得误报 refuted", alpha)
		}
	}
	// 对照：质量坍缩路径的 refuted 出口照常工作（对数域比较未破坏原语义）
	cur, _ := NewUniformBelief(3)
	for i := 0; i < 5; i++ {
		cur, _, _ = Options{}.UpdateStrength(cur, StrengthEvidence{D: []int{-1, -1, -1}, S: []float64{1, 1, 1}})
	}
	if stop := CheckStop(cur, 0.5, 3, Options{}); !stop.Stop || stop.Kind != VerdictRefuted {
		t.Fatalf("质量坍缩 refuted 出口应照常触发，got %+v", stop)
	}
}

// 亚正常成本（pr-agent 六轮验证）：近零成本按成本归一语义支配全部候选、
// 并列时确定性取先者、得分恒不为 NaN——设计行为钉板
func TestSubnormalCostSemantics(t *testing.T) {
	b, _ := NewBelief([]float64{0.4, 0.4, 0.2})
	good := mustAction(t, "good", []string{"x", "y"}, [][]float64{{.99, .01}, {.01, .99}, {.5, .5}}, 1)
	tiny := mustAction(t, "tiny", []string{"x", "y"}, [][]float64{{.6, .4}, {.4, .6}, {.5, .5}}, math.SmallestNonzeroFloat64)
	tiny2 := mustAction(t, "tiny2", []string{"x", "y"}, [][]float64{{.7, .3}, {.3, .7}, {.5, .5}}, math.SmallestNonzeroFloat64)
	sel, err := Select(b, []Action{good, tiny})
	if err != nil || sel.Best != 1 || math.IsNaN(sel.BestScore) {
		t.Fatalf("近零成本应支配，got %+v err=%v", sel, err)
	}
	sel2, _ := Select(b, []Action{tiny, tiny2, good})
	if sel2.Best != 0 || math.IsNaN(sel2.BestScore) {
		t.Fatalf("并列近零成本应确定性取先者，got %+v", sel2)
	}
}

// 选择得分观测列：Scores 与候选索引对齐且等于 EIG/c（决策轨迹消费口径；
// Best/BestScore 与观测列同算式，不经第二路径漂移）
func TestSelectScoresColumn(t *testing.T) {
	b, _ := NewUniformBelief(2)
	weak := mustAction(t, "weak", []string{"x", "y"}, [][]float64{{.55, .45}, {.45, .55}}, 2)
	strong := mustAction(t, "strong", []string{"x", "y"}, [][]float64{{.99, .01}, {.01, .99}}, 1)
	sel, err := Select(b, []Action{weak, strong})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(sel.Scores) != 2 {
		t.Fatalf("scores = %v, want 2（索引对齐候选）", sel.Scores)
	}
	for i, a := range []Action{weak, strong} {
		g, _ := EIG(b, a)
		if math.Abs(sel.Scores[i]-g/a.cost) > 1e-12 {
			t.Errorf("scores[%d] = %v, want EIG/c = %v", i, sel.Scores[i], g/a.cost)
		}
	}
	if sel.Best != 1 || sel.BestScore != sel.Scores[1] {
		t.Errorf("best = %+v, want strong with score column value", sel)
	}
}
