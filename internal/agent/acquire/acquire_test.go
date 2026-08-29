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
			t.Fatalf("EIG(%s): %v", c.act.Name, gerr)
		}
		if math.Abs(got-c.want) > 1e-2 {
			t.Fatalf("EIG(%s) 应 ≈%.2f bit，got %.4f", c.act.Name, c.want, got)
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
	// 行和 0.9（LLM 输出容差）→ 构造时归一，D 用作似然不偏置
	a, err := NewAction("tol", []string{"x", "y"}, [][]float64{{0.72, 0.18}}, 1)
	if err != nil {
		t.Fatalf("tol: %v", err)
	}
	if a.D[0][0] != 0.8 || a.D[0][1] != 0.2 {
		t.Fatalf("行应按和归一，got %v", a.D[0])
	}
	// 零成本归一为 1（评分除法防护）
	if a.Cost != 1 {
		t.Fatalf("零成本应归一为 1，got %f", a.Cost)
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
