package summary

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// 等行数切分：边界 ⌊j·n/S⌋、覆盖不重不漏、片大小差 ≤1；S>n 收敛、非法入参返回 nil
func TestSplitShards(t *testing.T) {
	if got := splitShards(0, 3); got != nil {
		t.Fatalf("n=0 应返回 nil，got %v", got)
	}
	if got := splitShards(10, 0); got != nil {
		t.Fatalf("S=0 应返回 nil，got %v", got)
	}
	// n=10 S=3：边界 ⌊10/3⌋=3、⌊20/3⌋=6、尾片吃余数
	got := splitShards(10, 3)
	want := []shardRange{{0, 3}, {3, 6}, {6, 10}}
	if len(got) != len(want) {
		t.Fatalf("片数 = %d，want %d：%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("片 %d = %+v，want %+v", i, got[i], want[i])
		}
	}
	if got := splitShards(3, 7); len(got) != 3 {
		t.Fatalf("S>n 应收敛为 n 片，got %d 片：%v", len(got), got)
	}
	if got := splitShards(100, 1); len(got) != 1 || got[0].From != 0 || got[0].To != 100 {
		t.Fatalf("S=1 应为全表一片：%v", got)
	}
	// 覆盖不重不漏、片大小差 ≤1（等分边界的通用性质）
	for _, c := range [][2]int{{100, 7}, {97, 13}, {5, 5}, {41, 40}} {
		shards := splitShards(c[0], c[1])
		if len(shards) == 0 {
			t.Fatalf("n=%d S=%d 返回空", c[0], c[1])
		}
		prevTo, lo, hi := 0, c[0], 0
		for _, r := range shards {
			if r.From != prevTo || r.To <= r.From {
				t.Fatalf("n=%d S=%d 片 %+v 不连续（prevTo=%d）", c[0], c[1], r, prevTo)
			}
			if sz := r.To - r.From; sz < lo {
				lo = sz
			} else if sz > hi {
				hi = sz
			}
			prevTo = r.To
		}
		if prevTo != c[0] || hi-lo > 1 {
			t.Fatalf("n=%d S=%d 覆盖到 %d（want %d）或片大小差 %d > 1", c[0], c[1], prevTo, c[0], hi-lo)
		}
	}
}

// 片内聚合与阶段 A：稀有命中全量清点（与名额无关）；清单按全局权重降序；
// 每个稀有值在预算内必有代表行，首个携带行优先（权重高的值先拿名额）
func TestPickShardRowsRareCoverage(t *testing.T) {
	cols := []string{"NAME", "STATUS", "NODE"}
	rows := make([][]string, 100)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("p-%03d", i), "Running", "node-1"}
	}
	for _, i := range []int{10, 11, 12, 13, 14} {
		rows[i][1] = "Error" // STATUS 稀有值 ×5
	}
	for _, i := range []int{20, 30, 31} {
		rows[i][2] = "node-9" // NODE 稀有值 ×3（log2(100/3) > log2(100/5)，权重更高）
	}
	hists := ColumnHistograms(cols, rows)
	base := buildMapBaseline(rows, hists)

	// 三行名额（按渲染整行口径）：阶段 A 须先装 node-9 首携带行 20、再装 Error 首携带行 10
	budget := mrRowRunes(20, rows[20]) + mrRowRunes(10, rows[10]) + 2
	pick := pickShardRows(rows, shardRange{0, 100}, &base, budget)

	if pick.RareRows != 8 {
		t.Fatalf("RareRows = %d，want 8（5+3 命中行去重）", pick.RareRows)
	}
	if len(pick.RareList) != 2 || pick.RareList[0].Val != "node-9" || pick.RareList[1].Val != "Error" {
		t.Fatalf("RareList = %+v，want node-9（权重高）在前", pick.RareList)
	}
	for _, want := range []int{20, 10} {
		if !containsInt(pick.Rows, want) {
			t.Fatalf("阶段 A 代表行缺 %d（Rows=%v）", want, pick.Rows)
		}
	}
	for _, e := range pick.RareList {
		found := false
		for _, i := range pick.Rows {
			if rows[i][e.Col] == e.Val {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("稀有值 %s 无代表行（Rows=%v）", e.Val, pick.Rows)
		}
	}
	if pick.ShownHits < 2 || pick.ShownHits > pick.RareRows {
		t.Fatalf("ShownHits = %d 应在 [2, %d]", pick.ShownHits, pick.RareRows)
	}
}

// 预算截断：名额装不下时命中行不可见，但报数全量、截断可判（RareRows > ShownHits）——G2 的核心
func TestPickShardRowsBudgetCutKeepsCounts(t *testing.T) {
	cols := []string{"NAME", "STATUS"}
	rows := make([][]string, 100)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("p-%03d", i), "Running"}
	}
	for _, i := range []int{10, 11, 12, 13, 14} {
		rows[i][1] = "Error" // 5 个命中行
	}
	hists := ColumnHistograms(cols, rows)
	base := buildMapBaseline(rows, hists)

	// 预算只装得下一行：强制装入 Error 首携带行，其余 4 行不可见但已清点
	budget := mrRowRunes(10, rows[10]) + 1
	pick := pickShardRows(rows, shardRange{0, 100}, &base, budget)

	if len(pick.Rows) != 1 || pick.Rows[0] != 10 {
		t.Fatalf("紧预算应只装首个携带行 10，got %v", pick.Rows)
	}
	if pick.RareRows != 5 || pick.ShownHits != 1 {
		t.Fatalf("报数须全量：RareRows=%d ShownHits=%d，want 5/1", pick.RareRows, pick.ShownHits)
	}
}

// 阶段 B：全局 T² 降序装入；T² 全零退化为行号序；T² 不可用走均匀步长；零预算强制首行
func TestSelectShardRowsPhaseB(t *testing.T) {
	rows := make([][]string, 6)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("r%d", i)}
	}
	emptyUni := coverUniverse{rowSets: make([][]coverKey, 6)}
	agg := aggregateShard(rows, shardRange{0, 6}, emptyUni)
	if agg.rareRows != 0 || len(agg.keys) != 0 {
		t.Fatalf("空论域不应有稀有命中，got %+v", agg)
	}
	// 两行名额按渲染整行口径
	two := mrRowRunes(1, rows[1]) * 2

	base := &mapBaseline{uni: emptyUni, anom: []float64{0.1, 0.9, 0.5, 0.3, 0.7, 0.2}}
	if pick := selectShardRows(rows, shardRange{0, 6}, base, agg, two); !equalInts(pick.Rows, []int{1, 4}) {
		t.Fatalf("T² 降序应选 [1 4]，got %v", pick.Rows)
	}
	zero := &mapBaseline{uni: emptyUni, anom: []float64{0, 0, 0, 0, 0, 0}}
	if pick := selectShardRows(rows, shardRange{0, 6}, zero, agg, two); !equalInts(pick.Rows, []int{0, 1}) {
		t.Fatalf("全零退化应按行号序选 [0 1]，got %v", pick.Rows)
	}
	if pick := selectShardRows(rows, shardRange{0, 6}, &mapBaseline{uni: emptyUni}, agg, two); !equalInts(pick.Rows, []int{0, 3}) {
		t.Fatalf("步长兜底应选 [0 3]，got %v", pick.Rows)
	}
	if pick := selectShardRows(rows, shardRange{0, 6}, base, agg, 0); !equalInts(pick.Rows, []int{1}) {
		t.Fatalf("零预算应强制首候选 [1]（T² 最高），got %v", pick.Rows)
	}
}

// 候选循环精确记账：实际 total ≤ avail、每片 ≥1 代表行、最大化、宽窄表自适应
func TestChooseShardCount(t *testing.T) {
	cols, rows := buildLargeTableRareMid(200, 100, "Running", "Error")
	hists := ColumnHistograms(cols, rows)
	base := buildMapBaseline(rows, hists)
	avail := 1500

	s := chooseShardCount(cols, rows, &base, avail)
	if s < 2 || s > minInt(200, MapReduceMaxShards) {
		t.Fatalf("S = %d 应在 [2, %d]", s, minInt(200, MapReduceMaxShards))
	}
	// 复算：实际渲染 total ≤ avail 且每片 ≥1 代表行（结构性地板）；perShard 与循环同源
	pool := mrSectionPool(avail, 200)
	var b strings.Builder
	shards := splitShards(200, s)
	for si, r := range shards {
		if pick := writeShardSection(&b, cols, rows, si, len(shards), r, &base, pool/len(shards)); len(pick.Rows) == 0 {
			t.Fatalf("片 %+v 无代表行，地板被破坏", r)
		}
	}
	if got := utf8.RuneCountInString(b.String()); got > avail {
		t.Fatalf("total = %d 超 avail = %d", got, avail)
	}
	// 最大化：S+1 的 total 必超 avail（同函数复算，确定性）
	if s+1 <= minInt(200, MapReduceMaxShards) {
		var b2 strings.Builder
		more := splitShards(200, s+1)
		for si, r := range more {
			writeShardSection(&b2, cols, rows, si, len(more), r, &base, pool/len(more))
		}
		if got := utf8.RuneCountInString(b2.String()); got <= avail {
			t.Fatalf("S=%d 未最大化：S+1 total=%d ≤ avail=%d", s, got, avail)
		}
	}

	// 宽窄表自适应：同预算下行越长片数越少
	mk := func(cellLen int) ([]string, [][]string) {
		cell := strings.Repeat("x", cellLen)
		rs := make([][]string, 100)
		for i := range rs {
			rs[i] = []string{cell}
		}
		return []string{"V"}, rs
	}
	wCols, wRows := mk(120)
	nCols, nRows := mk(3)
	wBase := buildMapBaseline(wRows, ColumnHistograms(wCols, wRows))
	nBase := buildMapBaseline(nRows, ColumnHistograms(nCols, nRows))
	if sWide := chooseShardCount(wCols, wRows, &wBase, 1000); sWide >= chooseShardCount(nCols, nRows, &nBase, 1000) {
		t.Fatalf("宽表 S 应小于窄表")
	}
}

// 端到端：频次段（G1）+ 片节报数（G2）+ 全表 0 基行号代表行（G3）+ 预算总口径 ≤ B；
// 稀有行必达其片——fast 大表 24 行名额竞争下可能被挤掉的场景，分片后无名额竞争
func TestRenderMapReducePipeline(t *testing.T) {
	cols, rows := buildLargeTableRareMid(400, 200, "Running", "Error")
	got, stats := RenderWithStats("pods", cols, rows, false, RenderOptions{Method: MethodMapReduce, BudgetRunes: 1500})

	for _, want := range []string{
		"map-reduce 两遍分片全覆盖",
		"STATUS: Running×399 / Error×1", // G1：全局频次段
		"── 片 1/",                       // 片头
		"稀有命中 1 行：STATUS=Error×1",       // G2：其片报数
		"#200  p-200  Error",            // G3：全表 0 基行号，稀有行必达
		"全量在 raw",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺 %q\ngot:\n%s", want, got)
		}
	}
	// 所有 #行号 均为全表 0 基行号（[0, 400)），且含 200
	seen := map[int]bool{}
	for _, m := range regexp.MustCompile(`#(\d+)`).FindAllStringSubmatch(got, -1) {
		n, _ := strconv.Atoi(m[1])
		if n < 0 || n >= 400 {
			t.Fatalf("行号 %d 越界 [0,400)", n)
		}
		seen[n] = true
	}
	if !seen[200] {
		t.Fatalf("稀有行 #200 未出现\ngot:\n%s", got)
	}
	if stats.RowsIncluded < 2 || stats.InstanceRunes <= 0 {
		t.Fatalf("观测量异常：%+v", stats)
	}
	// 预算口径 = 产物总 rune（标签 + 频次段 + 全部片节 + 尾注 ≤ B）
	if rc := utf8.RuneCountInString(got); rc > 1500 {
		t.Fatalf("产物总 rune = %d 超 B = 1500", rc)
	}
}

// 溢出标注：密集命中 + 紧预算 → 「命中 N 行仅展示 k 行」+ 可跟进地址（offset/limit）
func TestRenderMapReduceOverflowAnnotation(t *testing.T) {
	cols := []string{"NAME", "STATUS"}
	rows := make([][]string, 120)
	for i := range rows {
		status := "Running"
		if i%2 == 0 {
			status = "Error" // 60 个稀有命中：名额装不下，必有截断
		}
		rows[i] = []string{fmt.Sprintf("p-%03d", i), status}
	}
	got := RenderWithOptions("pods", cols, rows, false, RenderOptions{Method: MethodMapReduce, BudgetRunes: 400})

	for _, want := range []string{"稀有命中", "仅展示", "offset=", "limit="} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺 %q\ngot:\n%s", want, got)
		}
	}
}

// 退化链：空表无片节；无显著列走步长兜底；小表充足预算全分片 = 全覆盖；极小预算退化为单片强制首行
func TestRenderMapReduceDegenerate(t *testing.T) {
	got := RenderWithOptions("t", []string{"A"}, nil, false, RenderOptions{Method: MethodMapReduce})
	if !strings.Contains(got, "t · 0 行") || strings.Contains(got, "片 1/") {
		t.Fatalf("空表应无片节\ngot:\n%s", got)
	}

	cols := []string{"V"}
	rows := make([][]string, 60)
	for i := range rows {
		rows[i] = []string{"same"}
	}
	got = RenderWithOptions("t", cols, rows, false, RenderOptions{Method: MethodMapReduce, BudgetRunes: 900})
	if !strings.Contains(got, "片 1/") || strings.Contains(got, "稀有命中") || !strings.Contains(got, "#") {
		t.Fatalf("无显著列应有片节与代表行、无报数\ngot:\n%s", got)
	}

	// 小表 + 充足预算：S 收敛到 n，每片 1 行 = 全覆盖
	small := make([][]string, 6)
	for i := range small {
		small[i] = []string{fmt.Sprintf("r%d", i)}
	}
	if _, stats := RenderWithStats("t", nil, small, false, RenderOptions{Method: MethodMapReduce, BudgetRunes: 4096}); stats.RowsIncluded != 6 {
		t.Fatalf("小表充足预算应全覆盖 6 行，got %d", stats.RowsIncluded)
	}

	// 极小预算：avail ≤ 0 退化为单片 + 强制首行，诚实超出不崩
	got = RenderWithOptions("t", cols, rows, false, RenderOptions{Method: MethodMapReduce, BudgetRunes: 1})
	if !strings.Contains(got, "片 1/1") {
		t.Fatalf("极小预算应退化为单片\ngot:\n%s", got)
	}
}

// 稀有清单截断：> RareListMax 个值时显式标注「另有 j 值」（#18 降级可见即不叫漏）
func TestWriteShardSectionRareListTruncation(t *testing.T) {
	cols := []string{"NAME", "STATUS"}
	rows := make([][]string, 40)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("p-%d", i), "ok"}
	}
	for i := 0; i < 10; i++ { // 10 个稀有值各 1 行（distinct=11 ≤ MaxDistinctForHist）
		rows[i][1] = fmt.Sprintf("s%d", i)
	}
	hists := ColumnHistograms(cols, rows)
	base := buildMapBaseline(rows, hists)

	var b strings.Builder
	pick := writeShardSection(&b, cols, rows, 0, 1, shardRange{0, 40}, &base, 4000)
	if pick.RareRows != 10 || len(pick.RareList) != 10 {
		t.Fatalf("清点应全量：RareRows=%d RareList=%d，want 10/10", pick.RareRows, len(pick.RareList))
	}
	if !strings.Contains(b.String(), "另有 2 值") {
		t.Fatalf("清单截断应显式标注\ngot:\n%s", b.String())
	}
}

// method=map-reduce 解析与 G4：枚举注册不改变既有默认（零值 fast、未知值报错列全方法名）
func TestParseMethodMapReduce(t *testing.T) {
	if m, err := ParseMethod("map-reduce"); err != nil || m != MethodMapReduce {
		t.Fatalf("map-reduce 解析失败：%v %v", m, err)
	}
	if m, err := ParseMethod(""); err != nil || m != MethodFast {
		t.Fatal("空串仍应为 fast 默认")
	}
	if _, err := ParseMethod("bogus"); err == nil || !strings.Contains(err.Error(), "map-reduce") {
		t.Fatalf("未知值报错应列全方法名，got %v", err)
	}
}

// 超均匀列的极稀有值必须进论域：1000 行仅 1 次 Error（主流占比 0.999 ≥ SignificantColumns
// 阈被排除），但其片报数与代表行仍须可达——G2/G3 对最稀有值恰恰最重要
func TestRenderMapReduceUltraRareInUniformColumn(t *testing.T) {
	cols := []string{"NAME", "STATUS"}
	rows := make([][]string, 1000)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("p-%03d", i), "Running"}
	}
	rows[500][1] = "Error"
	got := RenderWithOptions("pods", cols, rows, false, RenderOptions{Method: MethodMapReduce, BudgetRunes: 2000})

	if !strings.Contains(got, "STATUS: Running×999 / Error×1") {
		t.Fatalf("频次段应报存在性（G1）\ngot:\n%s", got)
	}
	if !strings.Contains(got, "稀有命中 1 行：STATUS=Error×1") {
		t.Fatalf("超均匀列的稀有值应有片报数（G2）\ngot:\n%s", got)
	}
	if !strings.Contains(got, "#500  p-500  Error") {
		t.Fatalf("稀有行应为其片代表行（G3）\ngot:\n%s", got)
	}
}

// 性能冒烟：5000 行 × 15 列两遍分片应为几十毫秒级（Pass1 O(N·C) + 候选循环 O(S*·N)）；
// 上限 100ms 防复杂度写崩，非性能门
func TestRenderMapReducePerformanceSmoke(t *testing.T) {
	cols := []string{"NAME", "READY", "STATUS", "RESTARTS", "NODE", "NS", "PHASE", "SCHED", "QOS", "PRI", "TOL", "AFF", "IMG", "PORT", "AGE"}
	rows := make([][]string, 5000)
	for i := range rows {
		status := "Running"
		if i%997 == 0 {
			status = "CrashLoopBackOff"
		}
		rows[i] = []string{
			"p-" + strconv.Itoa(i), "1/1", status, "0", "node-a", "default", "Running",
			"0s", "Burstable", "0", "none", "none", "app:v1", "8080", "3d",
		}
	}
	start := time.Now()
	_, stats := RenderWithStats("pods", cols, rows, false, RenderOptions{Method: MethodMapReduce})
	if el := time.Since(start); el > 100*time.Millisecond {
		t.Fatalf("5000×15 map-reduce 耗时 %v", el)
	}
	if stats.RowsIncluded == 0 {
		t.Fatal("应有代表行")
	}
}
