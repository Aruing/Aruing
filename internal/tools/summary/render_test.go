package summary

import (
	"fmt"
	"strings"
	"testing"
)

// 构造 N 行表格：默认所有行 STATUS=commonVal；可选 midIdx 行单列异常
func buildLargeTableRareMid(n, midIdx int, commonVal, errorVal string) ([]string, [][]string) {
	cols := []string{"NAME", "STATUS"}
	rows := make([][]string, n)
	for i := range rows {
		status := commonVal
		if i == midIdx {
			status = errorVal
		}
		rows[i] = []string{fmt.Sprintf("p-%03d", i), status}
	}
	return cols, rows
}

// 构造 N 行三列表：midIdx 行同时取 readyBad + statusBad（多列组合异常）
func buildLargeTableComboAnomaly(n, midIdx int, readyOK, statusOK, readyBad, statusBad string) ([]string, [][]string) {
	cols := []string{"NAME", "READY", "STATUS"}
	rows := make([][]string, n)
	for i := range rows {
		ready, status := readyOK, statusOK
		if i == midIdx {
			ready, status = readyBad, statusBad
		}
		rows[i] = []string{fmt.Sprintf("p-%03d", i), ready, status}
	}
	return cols, rows
}

// 大表单列异常代表行：100 行只有 1 行 STATUS=Error 位于中段，Summary 必须把它带入异常段或覆盖段
func TestRenderLargeTableRareMidRowSurfaces(t *testing.T) {
	cols, rows := buildLargeTableRareMid(100, 50, "Running", "Error")
	got := Render("pods", cols, rows, false)

	if !strings.Contains(got, "pods · 100 行") {
		t.Fatalf("missing row count\ngot:\n%s", got)
	}
	if !strings.Contains(got, "STATUS: Running×99 / Error×1") {
		t.Fatalf("missing frequency histogram for STATUS\ngot:\n%s", got)
	}
	if !strings.Contains(got, "p-050  Error") {
		t.Fatalf("rare mid row p-050/Error not surfaced\ngot:\n%s", got)
	}
	if strings.Count(got, "p-050") != 1 {
		t.Fatalf("rare row should appear exactly once, got %d\n%s", strings.Count(got, "p-050"), got)
	}
}

// 大表组合异常：PCA 在主成分空间抓组合偏离
func TestRenderLargeTableComboAnomalySurfaces(t *testing.T) {
	cols, rows := buildLargeTableComboAnomaly(100, 50, "1/1", "Running", "0/1", "CrashLoopBackOff")
	got := Render("pods", cols, rows, false)

	if !strings.Contains(got, "pods · 100 行") {
		t.Fatalf("missing row count\ngot:\n%s", got)
	}
	if !strings.Contains(got, "p-050  0/1  CrashLoopBackOff") {
		t.Fatalf("combo-anomaly row p-050 not surfaced\ngot:\n%s", got)
	}
}

// 大表全常态：PCA 走兜底；覆盖段均匀步长铺满中段
func TestRenderLargeTableAllNormalCoversMid(t *testing.T) {
	cols := []string{"NAME", "STATUS"}
	rows := make([][]string, 100)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("p-%03d", i), "Running"}
	}
	got := Render("pods", cols, rows, false)

	if !strings.Contains(got, fmt.Sprintf("覆盖抽样 %d 行", CoverShowBudget)) {
		t.Fatalf("missing coverage section with budget=%d\ngot:\n%s", CoverShowBudget, got)
	}
	hasLateMid := false
	for i := 50; i <= 95; i++ {
		if strings.Contains(got, fmt.Sprintf("p-%03d", i)) {
			hasLateMid = true
			break
		}
	}
	if !hasLateMid {
		t.Fatalf("coverage section did not reach late mid (p-050..p-095)\ngot:\n%s", got)
	}
}

// 列频次统计：低基数列给出值×次数分布，高基数列只标 distinct 数
func TestColumnHistogramsAndRenderFreq(t *testing.T) {
	cols := []string{"NAME", "STATUS", "NOTE"}
	rows := [][]string{
		{"a", "Running", ""},
		{"b", "Running", ""},
		{"c", "Error", ""},
		{"d", "Running", ""},
	}
	hists := ColumnHistograms(cols, rows)
	if got := hists[1]["Error"]; got != 1 {
		t.Fatalf("STATUS Error count = %d, want 1", got)
	}

	tests := []struct {
		name string
		col  string
		hist map[string]int
		want string
	}{
		{name: "low cardinality sorted by count desc", col: "STATUS", hist: hists[1], want: "STATUS: Running×3 / Error×1"},
		{name: "empty value rendered as literal", col: "NOTE", hist: hists[2], want: "NOTE: (empty)×4"},
		{name: "high cardinality only labels distinct", col: "NAME", hist: mapFor("a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y"), want: "NAME: 25 distinct（略）"},
		{name: "no rows yields no line", col: "X", hist: map[string]int{}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RenderColumnFreq(tt.col, tt.hist); got != tt.want {
				t.Errorf("RenderColumnFreq = %q, want %q", got, tt.want)
			}
		})
	}
}

// 大表抽样预算与去重：头/尾固定，异常段+覆盖段按预算；同一物理行不重复
func TestPickLargeTableRowsDedupAndBudget(t *testing.T) {
	cols := []string{"NAME", "V"}
	rows := make([][]string, 100)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("r%d", i), "v"}
	}
	hists := ColumnHistograms(cols, rows)

	picks := PickLargeTableRows(cols, rows, hists)
	if len(picks.Head) != LargeTableHead {
		t.Errorf("head len = %d, want %d", len(picks.Head), LargeTableHead)
	}
	if len(picks.Tail) != LargeTableTail {
		t.Errorf("tail len = %d, want %d", len(picks.Tail), LargeTableTail)
	}
	if len(picks.Cover) != CoverShowBudget {
		t.Errorf("cover len = %d, want %d", len(picks.Cover), CoverShowBudget)
	}

	seen := make(map[int]string)
	check := func(name string, idxs []int) {
		for _, i := range idxs {
			if _, ok := seen[i]; ok {
				t.Errorf("index %d duplicated in %s and %s", i, seen[i], name)
			}
			seen[i] = name
		}
	}
	check("anomaly", picks.Anomaly)
	check("head", picks.Head)
	check("cover", picks.Cover)
	check("tail", picks.Tail)

	if picks.Head[0] != 0 || picks.Head[len(picks.Head)-1] != LargeTableHead-1 {
		t.Errorf("head indices = %v, want 0..%d", picks.Head, LargeTableHead-1)
	}
	if picks.Tail[0] != 100-LargeTableTail || picks.Tail[len(picks.Tail)-1] != 99 {
		t.Errorf("tail indices = %v, want %d..99", picks.Tail, 100-LargeTableTail)
	}
}

func mapFor(vals ...string) map[string]int {
	m := make(map[string]int, len(vals))
	for _, v := range vals {
		m[v] = 1
	}
	return m
}

// 方法开关矩阵：零值选项 = fast 与 Render 输出一致；各方法各自口径成立
func TestRenderWithOptionsMethods(t *testing.T) {
	cols, rows := buildLargeTableRareMid(100, 50, "Running", "Error")

	if got := RenderWithOptions("pods", cols, rows, false, RenderOptions{}); got != Render("pods", cols, rows, false) {
		t.Fatal("零值选项必须与 fast Render 输出一致（既有调用方零改）")
	}

	full := RenderWithOptions("pods", cols, rows, false, RenderOptions{Method: MethodFull})
	if got := strings.Count(full, "p-0"); got != 100 {
		t.Fatalf("full 应包含全部 100 行，got %d", got)
	}

	ht := RenderWithOptions("pods", cols, rows, false, RenderOptions{Method: MethodHeadTail, BudgetRunes: 400})
	if !strings.Contains(ht, "已省略") || strings.Contains(ht, "p-050") {
		t.Fatalf("紧预算头尾截断必须省略中段行\ngot:\n%s", ht)
	}
	if !strings.Contains(ht, "p-000") || !strings.Contains(ht, "p-099") {
		t.Fatalf("头尾截断必须保留边界行\ngot:\n%s", ht)
	}

	uni := RenderWithOptions("pods", cols, rows, false, RenderOptions{Method: MethodUniform, BudgetRunes: 300})
	if !strings.Contains(uni, "均匀采样") {
		t.Fatalf("uniform 渲染缺标记\ngot:\n%s", uni)
	}
	if got := strings.Count(uni, "p-0"); got >= 100 {
		t.Fatalf("紧预算均匀采样应只抽子集，got %d 行", got)
	}

	gr := RenderWithOptions("pods", cols, rows, false, RenderOptions{Method: MethodGreedy, BudgetRunes: 600})
	for _, want := range []string{"STATUS: Running×99 / Error×1", "代表性子集", "p-050  Error", "已省略"} {
		if !strings.Contains(gr, want) {
			t.Fatalf("greedy 渲染缺 %q\ngot:\n%s", want, gr)
		}
	}

	kn := RenderWithOptions("pods", cols, rows, false, RenderOptions{Method: MethodGreedyKnapsack, BudgetRunes: 600})
	if !strings.Contains(kn, "代表性子集") || !strings.Contains(kn, "p-050  Error") {
		t.Fatalf("greedy-knapsack 渲染缺子集或稀有行\ngot:\n%s", kn)
	}
}

// ParseMethod：空串 = fast；未知值明确报错（启动失败口径，不静默回落）
func TestParseMethod(t *testing.T) {
	if m, err := ParseMethod(""); err != nil || m != MethodFast {
		t.Fatalf("空串应解析为 fast，got %v err=%v", m, err)
	}
	if m, err := ParseMethod("greedy"); err != nil || m != MethodGreedy {
		t.Fatalf("greedy 解析失败：%v %v", m, err)
	}
	if _, err := ParseMethod("bogus"); err == nil {
		t.Fatal("未知方法应报错")
	}
}
