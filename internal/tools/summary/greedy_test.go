package summary

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
	"time"
)

// T² 数值回归（6 行两特征玩具表，手算基准）：
// r1–r4 健康簇 T²≈0.50；r5（未就绪 + CrashLoop）≈4.99；r6 刚启动 ≈5.00
// 平方口径 + 总体方差（÷n）；T² 忠实标出两个稀有组合——找统计离群，不是判根因
func TestT2ScoresToyExample(t *testing.T) {
	cols := []string{"READY", "CL"}
	rows := [][]string{
		{"1", "0"}, {"1", "0"}, {"1", "0"}, {"1", "0"},
		{"0", "1"}, // r5：根因（中段）
		{"0", "0"}, // r6：刚启动未就绪
	}
	hists := ColumnHistograms(cols, rows)
	sig := SignificantColumns(hists)
	scores := t2Scores(rows, sig, hists)
	if scores == nil {
		t.Fatal("t2Scores returned nil for toy table")
	}
	for i := 0; i < 4; i++ {
		if d := math.Abs(scores[i] - 0.50); d > 0.05 {
			t.Errorf("健康行 r%d T² = %.3f，期望 ≈0.50", i+1, scores[i])
		}
	}
	if d := math.Abs(scores[4] - 4.99); d > 0.10 {
		t.Errorf("根因行 r5 T² = %.3f，期望 ≈4.99", scores[4])
	}
	if d := math.Abs(scores[5] - 5.00); d > 0.10 {
		t.Errorf("未就绪行 r6 T² = %.3f，期望 ≈5.00", scores[5])
	}
}

// 中段稀有行优先入选：100 行 1 异常，紧预算下贪心第一选必须是异常行（覆盖与异常同向）
// 锚硬约束在场、预算硬顶与基数上限同时成立
func TestGreedyPickRareMidRowFirst(t *testing.T) {
	cols, rows := buildLargeTableRareMid(100, 50, "Running", "Error")
	hists := ColumnHistograms(cols, rows)
	res := GreedyPick(rows, hists, GreedyOptions{BudgetRunes: 600})
	if len(res.Order) == 0 || res.Order[0] != 50 {
		t.Fatalf("贪心首选 = %v，期望 [50]", res.Order)
	}
	if res.K <= 0 {
		t.Fatalf("折算基数 k = %d，期望 > 0", res.K)
	}
	for _, a := range res.Anchors {
		if !containsInt(res.Selected, a) {
			t.Fatalf("锚 %d 不在 Selected 中", a)
		}
	}
	if res.TotalRunes > 600 {
		t.Fatalf("TotalRunes = %d 超预算 600", res.TotalRunes)
	}
	if len(res.Order) > res.K {
		t.Fatalf("贪心选中 %d 行超过基数 k=%d", len(res.Order), res.K)
	}
}

// 子模性性质测试：固定种子随机表上验证 Δf(A,x) >= Δf(B,x)（A ⊆ B，x ∉ B）
// 覆盖项是子模来源；异常项为模（边际恒定）不破坏性质，两条路径分别验证
func TestGreedySubmodularProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 10; trial++ {
		n := 12 + rng.Intn(9)
		rows := randomRows(rng, n)
		cols := []string{"A", "B", "C"}
		hists := ColumnHistograms(cols, rows)
		sig := SignificantColumns(hists)
		uni := buildCoverUniverse(rows, sig, hists, trial%2 == 0)
		var anom []float64
		if trial%2 == 1 {
			// 模项在场：合成行分验证「子模 + 模 = 子模」（小表真实异常分为 nil）
			anom = make([]float64, n)
			for i := range anom {
				anom[i] = rng.Float64() * 0.5
			}
		}
		s := &greedySolver{uni: uni, covered: map[coverKey]struct{}{}, anom: anom}
		fill := func(set []int) {
			s.covered = make(map[coverKey]struct{})
			for _, i := range set {
				s.take(i)
			}
		}
		for probe := 0; probe < 40; probe++ {
			perm := rng.Perm(n)
			mB := 1 + rng.Intn(n-1)
			B := perm[:mB]
			A := perm[:1+rng.Intn(mB)]
			x := perm[mB]
			fill(A)
			gA := s.marginal(x)
			fill(B)
			gB := s.marginal(x)
			if gA+1e-9 < gB {
				t.Fatalf("子模性被破坏：trial=%d probe=%d Δf(A,x)=%.6f < Δf(B,x)=%.6f", trial, probe, gA, gB)
			}
		}
	}
}

// CELF 等价性：lazy 与朴素贪心选中顺序完全一致（同 tie-break 口径）；knapsack 臂同样成立
func TestGreedyLazyNaiveEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for _, knapsack := range []bool{false, true} {
		for trial := 0; trial < 6; trial++ {
			n := 40 + rng.Intn(40)
			rows := randomRows(rng, n)
			cols := []string{"A", "B", "C"}
			hists := ColumnHistograms(cols, rows)
			sig := SignificantColumns(hists)
			uni := buildCoverUniverse(rows, sig, hists, false)
			var anom []float64
			if scores := AnomalyScores(rows, sig, hists); scores != nil {
				var sum float64
				for _, v := range scores {
					sum += v
				}
				if sum > 0 {
					anom = make([]float64, n)
					for i, v := range scores {
						anom[i] = v / sum
					}
				}
			}
			cost := make([]int, n)
			for i, r := range rows {
				cost[i] = renderedRowRunes(r)
			}
			mkSolver := func() *greedySolver {
				return &greedySolver{
					uni:      uni,
					covered:  map[coverKey]struct{}{},
					anom:     anom,
					cost:     cost,
					avail:    400,
					limit:    12,
					knapsack: knapsack,
				}
			}
			cands := rng.Perm(n)
			lazyOrder, _, _ := mkSolver().solveLazy(cands)
			naiveOrder, _, _ := mkSolver().solveNaive(cands)
			if !equalInts(lazyOrder, naiveOrder) {
				t.Fatalf("knapsack=%v trial=%d lazy=%v naive=%v", knapsack, trial, lazyOrder, naiveOrder)
			}
		}
	}
}

// 预算连锚都装不下：锚硬约束优先，贪心部分空转不崩
func TestGreedyTinyBudgetAnchorsOnly(t *testing.T) {
	cols, rows := buildLargeTableRareMid(100, 50, "Running", "Error")
	hists := ColumnHistograms(cols, rows)
	res := GreedyPick(rows, hists, GreedyOptions{BudgetRunes: 10})
	if len(res.Order) != 0 {
		t.Fatalf("极小预算下贪心应为空，got %v", res.Order)
	}
	if !equalInts(res.Selected, res.Anchors) {
		t.Fatalf("Selected 应只剩锚：%v vs %v", res.Selected, res.Anchors)
	}
}

// 退化链：无显著列（全同值）且小表无异常分 → 只剩锚，结果有定义
func TestGreedyNoSignalFallback(t *testing.T) {
	cols := []string{"STATUS"}
	rows := make([][]string, 20)
	for i := range rows {
		rows[i] = []string{"Running"}
	}
	hists := ColumnHistograms(cols, rows)
	res := GreedyPick(rows, hists, GreedyOptions{})
	if len(res.Order) != 0 || !equalInts(res.Selected, res.Anchors) {
		t.Fatalf("退化路径应只剩锚，order=%v", res.Order)
	}
}

// 性能冒烟：500 行 × 15 列贪心应为毫秒级；上限宽裕（防复杂度写崩，非性能门）
func TestGreedyLargePerformanceSmoke(t *testing.T) {
	cols := []string{"NAME", "READY", "STATUS", "RESTARTS", "NODE", "NS", "PHASE", "SCHED", "QOS", "PRI", "TOL", "AFF", "IMG", "PORT", "AGE"}
	n := 500
	rows := make([][]string, n)
	for i := range rows {
		status := "Running"
		if i == 249 {
			status = "CrashLoopBackOff"
		}
		rows[i] = []string{
			"p-" + strconv.Itoa(i), "1/1", status, "0", "node-a", "default", "Running",
			"0s", "Burstable", "0", "none", "none", "app:v1", "8080", "3d",
		}
	}
	hists := ColumnHistograms(cols, rows)
	start := time.Now()
	res := GreedyPick(rows, hists, GreedyOptions{})
	if el := time.Since(start); el > time.Second {
		t.Fatalf("500×15 贪心耗时 %v", el)
	}
	if len(res.Selected) == 0 || !containsInt(res.Selected, 249) {
		t.Fatalf("中段异常行应入选：%v", res.Order[:minInt(5, len(res.Order))])
	}
}

// randomRows 构造带偏斜取值的随机表：多数行取主流值，少数行随机取值（产生覆盖论域）
func randomRows(rng *rand.Rand, n int) [][]string {
	vals := []string{"a", "b", "c"}
	rows := make([][]string, n)
	for i := range rows {
		r := make([]string, 3)
		for j := range r {
			if rng.Intn(4) == 0 {
				r[j] = vals[rng.Intn(len(vals))]
			} else {
				r[j] = vals[0]
			}
		}
		rows[i] = r
	}
	return rows
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
