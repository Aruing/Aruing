// 加权贪心代表性投影：预算约束下最大化「覆盖 + 异常」目标
//
// 目标函数 f(S) = f_cov(S) + λ·f_anom(S)，两分量各自归一到 [0,1]：
//   f_cov：显著列非主流取值的加权集合覆盖，权重 = log2(N/count(u))（自信息量，稀有优先）；
//          单调子模——取值已被代表后，第二行不再加分
//   f_anom：PCA T² 行分（平方口径）作模函数奖励，行独立加分
// 子模 + 模 = 子模；头尾锚以硬约束预置（等价充分大的边界权重 μ），预算再紧不丢锚
//
// 约束形态：k = ⌊可用 rune 预算 / 中位渲染行长⌋ 折算基数 → lazy greedy（CELF）按基数装入，
// 1−1/e 近似保证落在基数口径（Nemhauser–Wolsey–Fisher 1978）；个别超长行装不下即跳过，
// 渲染 rune 超预算时从最后选中行回退（贪心选择序的前缀仍是贪心解）
// gain/token knapsack 变体为对照变体（Knapsack=true），无近似保证口径
//
// 全程机械、确定：无随机、无业务判断（#16/#19）；退化链逐级有定义——
// 无显著列 → 覆盖项置 0；小表 / 方差 0 / 协方差奇异 → 异常项置 0；两项皆空 → 只剩锚

package summary

import (
	"container/heap"
	"math"
	"sort"
)

// GreedyOptions 贪心选择选项；零值 = 默认预算、λ=1、log 稀有度加权、基数折算主实现
type GreedyOptions struct {
	// 实例行 rune 预算硬顶（锚计入）；<=0 用 DefaultBudgetRunes
	BudgetRunes int
	// f_anom 权重 λ；<=0 视为默认 1
	Lambda float64
	// 覆盖权重均匀化（对照变体）：每个非主流取值等权，关闭 log 稀有度加权
	UniformWeight bool
	// knapsack 对照变体：按收益/行长装入，不做基数折算
	Knapsack bool
	// 异常项置 0（只覆盖对照）
	AnomalyOff bool
	// 覆盖项置 0（只异常对照）
	CoverageOff bool
	// 简单统计量消融臂（bench 注入，不进 config）：异常分不用 PCA/T²，
	// 用行长 |z| + 非常规取值计数（见 SimpleStatScores）；证明多元统计必要性的对照
	SimpleStat bool
}

// GreedyPickResult 贪心投影的选中结果；索引均对齐输入 rows
type GreedyPickResult struct {
	// 全部选中行（锚 + 贪心），升序；渲染直接消费
	Selected []int
	// 头尾锚行索引，升序（硬约束预置，不占贪心名额）
	Anchors []int
	// 贪心选中顺序（先选在前）；预算回退从尾部裁剪后同步
	Order []int
	// 与 Order 一一对应的选中时刻边际收益（诊断与实验输出用）
	Gains []float64
	// 折算基数 k（knapsack 臂为 -1）
	K int
	// 选中实例行的渲染 rune 总量（含锚）
	TotalRunes int
}

// coverKey 覆盖论域元素：某显著列上的一个非主流取值
type coverKey struct {
	col int
	val string
}

// coverUniverse 覆盖论域：元素权重与每行覆盖的元素集合
type coverUniverse struct {
	// 每行覆盖的论域元素（与行索引对齐；无覆盖行为空切片）
	rowSets [][]coverKey
	// 元素权重；uniform 时全 1，否则 log2(N/count)
	weight map[coverKey]float64
	// 权重总和，f_cov 的归一分母；<=0 表示覆盖项退化（无显著列）
	total float64
}

// buildCoverUniverse 构造覆盖论域
// 主流值 = 列内计数最大者，并列取字典序最小（确定性破除）；论域 = 显著列的全部非主流取值
func buildCoverUniverse(rows [][]string, sigCols []int, hists []map[string]int, uniform bool) coverUniverse {
	u := coverUniverse{weight: make(map[coverKey]float64)}
	n := len(rows)
	// rowSets 先行分配：空论域（total<=0）时 marginal/take 仍可安全索引空切片
	u.rowSets = make([][]coverKey, n)
	for _, c := range sigCols {
		if c >= len(hists) {
			continue
		}
		dominant := dominantValue(hists[c])
		for v, cnt := range hists[c] {
			if v == dominant {
				continue
			}
			w := 1.0
			if !uniform {
				// 自信息量：出现越稀有权重越高；非主流取值 cnt < n，恒为正
				w = math.Log2(float64(n) / float64(cnt))
			}
			u.weight[coverKey{c, v}] = w
			u.total += w
		}
	}
	if u.total <= 0 {
		return u
	}
	for i, r := range rows {
		for _, c := range sigCols {
			if c >= len(r) {
				continue
			}
			k := coverKey{c, r[c]}
			if _, ok := u.weight[k]; ok {
				u.rowSets[i] = append(u.rowSets[i], k)
			}
		}
	}
	return u
}

// dominantValue 列内主流取值：计数最大，并列取字典序最小（确定性破除，map 迭代序无关）
func dominantValue(h map[string]int) string {
	best := ""
	first := true
	for v, cnt := range h {
		if first || cnt > h[best] || (cnt == h[best] && v < best) {
			best = v
			first = false
		}
	}
	return best
}

// greedySolver 承载一次贪心选择的全量状态；lazy 与 naive 两种驱动共用，
// 边际收益只依赖已覆盖集合（覆盖项）与行分（异常项，模项与已选无关）
type greedySolver struct {
	// 覆盖论域（CoverageOff 时为零值）
	uni coverUniverse
	// 已覆盖论域元素
	covered map[coverKey]struct{}
	// 归一后的异常行分 λ·t²(i)/Σt²；nil = 异常项关闭
	anom []float64
	// 每行渲染 rune 长（预算记账）
	cost []int
	// 锚后可用预算
	avail int
	// 选中行数上限（基数折算）；knapsack 臂为大数
	limit int
	// knapsack 臂开关：堆序按收益/行长
	knapsack bool
}

// marginal 行当前边际收益：未覆盖元素权重和（归一）+ 异常行分
func (s *greedySolver) marginal(i int) float64 {
	var g float64
	for _, k := range s.uni.rowSets[i] {
		if _, ok := s.covered[k]; !ok {
			g += s.uni.weight[k]
		}
	}
	if s.uni.total > 0 {
		g /= s.uni.total
	}
	if s.anom != nil {
		g += s.anom[i]
	}
	return g
}

// take 选中一行：登记其覆盖的论域元素
func (s *greedySolver) take(i int) {
	for _, k := range s.uni.rowSets[i] {
		s.covered[k] = struct{}{}
	}
}

// heapKey 堆序键：基数臂按收益，knapsack 臂按收益/行长（行长非正时退化为收益本身防除零）
func (s *greedySolver) heapKey(i int, gain float64) float64 {
	if !s.knapsack || s.cost[i] <= 0 {
		return gain
	}
	return gain / float64(s.cost[i])
}

// lazyEntry 堆元素：行索引 + 可能过期的键值（CELF 懒更新）
type lazyEntry struct {
	idx int
	key float64
}

// lazyHeap 最大堆：键大者先出；键并列时行号小者先出（与朴素贪度取首最大行的口径一致）
type lazyHeap []lazyEntry

func (h lazyHeap) Len() int { return len(h) }
func (h lazyHeap) Less(i, j int) bool {
	if h[i].key != h[j].key {
		return h[i].key > h[j].key
	}
	return h[i].idx < h[j].idx
}
func (h lazyHeap) Swap(i, j int)    { h[i], h[j] = h[j], h[i] }
func (h *lazyHeap) Push(x any)      { *h = append(*h, x.(lazyEntry)) }
func (h *lazyHeap) Pop() any        { old := *h; n := len(old); e := old[n-1]; *h = old[:n-1]; return e }
func (h *lazyHeap) peek() lazyEntry { return (*h)[0] }

// solveLazy lazy greedy（CELF）：弹出堆顶重算真实键值，仍领先则选中，过期则压回
// 复杂度近 O(N log N)；选中集合与朴素贪心一致（等价性由单测保证）
func (s *greedySolver) solveLazy(cands []int) (order []int, gains []float64, runes int) {
	h := make(lazyHeap, 0, len(cands))
	for _, i := range cands {
		g := s.marginal(i)
		h = append(h, lazyEntry{idx: i, key: s.heapKey(i, g)})
	}
	heap.Init(&h)
	for len(order) < s.limit && h.Len() > 0 {
		top := heap.Pop(&h).(lazyEntry)
		g := s.marginal(top.idx)
		key := s.heapKey(top.idx, g)
		if h.Len() > 0 {
			next := h.peek()
			if key < next.key || (key == next.key && next.idx < top.idx) {
				// 过期：真实键值已落后，压回等下一轮
				heap.Push(&h, lazyEntry{top.idx, key})
				continue
			}
		}
		if g <= 0 {
			// 单调性：堆顶真实收益 <= 0 时其余更小，全表已无可增益行
			break
		}
		if runes+s.cost[top.idx] > s.avail {
			// 装不下：可用预算只减不增，此行永不再装，丢弃继续
			continue
		}
		order = append(order, top.idx)
		gains = append(gains, g)
		runes += s.cost[top.idx]
		s.take(top.idx)
	}
	return order, gains, runes
}

// solveNaive 朴素贪心：每轮全扫候选取真实键值最大（并列取行号最小）
// 键值与 lazy 同口径：基数臂按收益、knapsack 臂按收益/行长（heapKey），否则两驱动选择规则不一致，
// 等价性测试会数据依赖地闪挂
// O(N·k)，只作 lazy 等价性测试的基准，产品路径不调用
func (s *greedySolver) solveNaive(cands []int) (order []int, gains []float64, runes int) {
	remaining := append([]int(nil), cands...)
	sort.Ints(remaining)
	for len(order) < s.limit && len(remaining) > 0 {
		best := -1
		var bestKey float64
		var bestGain float64
		for _, i := range remaining {
			g := s.marginal(i)
			if key := s.heapKey(i, g); best < 0 || key > bestKey {
				best, bestKey, bestGain = i, key, g
			}
		}
		if bestKey <= 0 {
			break
		}
		remaining = removeInt(remaining, best)
		if runes+s.cost[best] > s.avail {
			// 装不下：移出候选后继续，更短的行可能仍装得下
			continue
		}
		order = append(order, best)
		gains = append(gains, bestGain)
		runes += s.cost[best]
		s.take(best)
	}
	return order, gains, runes
}

// removeInt 从升序切片移除一个值（保序）；朴素贪心维护候选用
func removeInt(sorted []int, v int) []int {
	for i, x := range sorted {
		if x == v {
			return append(sorted[:i], sorted[i+1:]...)
		}
	}
	return sorted
}

// GreedyPick 执行加权贪心选择：预置头尾锚 → 构造覆盖论域与异常行分 → CELF 装入 → 预算回退
// 空表返回零值结果；退化链见文件头注释
func GreedyPick(rows [][]string, hists []map[string]int, opts GreedyOptions) GreedyPickResult {
	n := len(rows)
	res := GreedyPickResult{K: -1}
	if n == 0 {
		return res
	}
	budget := opts.BudgetRunes
	if budget <= 0 {
		budget = DefaultBudgetRunes
	}
	lambda := opts.Lambda
	if lambda <= 0 {
		lambda = 1
	}

	// 头尾锚：与 fast 路径同取值（各 4 行），硬约束预置不占贪心名额；小表头尾重叠时去重
	headEnd := minInt(LargeTableHead, n)
	tailStart := n - LargeTableTail
	if tailStart < headEnd {
		tailStart = headEnd
	}
	used := make(map[int]struct{}, LargeTableHead+LargeTableTail)
	anchorRunes := 0
	addAnchor := func(i int) {
		used[i] = struct{}{}
		res.Anchors = append(res.Anchors, i)
		anchorRunes += renderedRowRunes(rows[i])
	}
	for i := 0; i < headEnd; i++ {
		addAnchor(i)
	}
	for i := tailStart; i < n; i++ {
		if _, ok := used[i]; !ok {
			addAnchor(i)
		}
	}

	// 每行渲染成本与锚后可用预算
	cost := make([]int, n)
	for i, r := range rows {
		cost[i] = renderedRowRunes(r)
	}
	avail := budget - anchorRunes
	if avail < 0 {
		// 预算连锚都装不下：锚硬约束优先，贪心部分空转
		avail = 0
	}

	cands := make([]int, 0, n)
	for i := 0; i < n; i++ {
		if _, ok := used[i]; !ok {
			cands = append(cands, i)
		}
	}

	// 目标两项：覆盖论域与归一异常行分
	sigCols := SignificantColumns(hists)
	var uni coverUniverse
	if !opts.CoverageOff {
		uni = buildCoverUniverse(rows, sigCols, hists, opts.UniformWeight)
	}
	var anom []float64
	if !opts.AnomalyOff {
		// 异常行分：默认 PCA T²（平方口径）；消融臂换简单统计量（不进 config，bench 注入）
		var scores []float64
		if opts.SimpleStat {
			scores = SimpleStatScores(rows, sigCols, hists)
		} else {
			scores = AnomalyScores(rows, sigCols, hists)
		}
		if scores != nil {
			var sum float64
			for _, v := range scores {
				sum += v
			}
			if sum > 0 {
				anom = make([]float64, n)
				for i, v := range scores {
					anom[i] = lambda * v / sum
				}
			}
		}
	}

	s := &greedySolver{
		uni:     uni,
		covered: make(map[coverKey]struct{}),
		anom:    anom,
		cost:    cost,
		avail:   avail,
		limit:   math.MaxInt32,
	}
	if !opts.Knapsack && len(cands) > 0 {
		// 基数折算：k = ⌊可用预算 / 候选中位行长⌋，1−1/e 落此口径
		candCosts := make([]int, len(cands))
		for i, idx := range cands {
			candCosts[i] = cost[idx]
		}
		if med := medianInt(candCosts); med > 0 {
			s.limit = avail / med
			res.K = s.limit
		}
	}

	order, gains, runes := s.solveLazy(cands)

	// 预算硬顶保险：基数折算 + 装入检查已拦，正常不触发；触发时从最后选中行回退（前缀仍是贪心解）
	for runes > avail && len(order) > 0 {
		last := order[len(order)-1]
		order = order[:len(order)-1]
		gains = gains[:len(gains)-1]
		runes -= cost[last]
	}

	res.Order = order
	res.Gains = gains
	res.Selected = append(append([]int(nil), res.Anchors...), order...)
	sort.Ints(res.Selected)
	res.TotalRunes = anchorRunes + runes
	return res
}

// simpleStatRareFrac 非常规取值的频次阈值：列内出现频次低于行数的该比例视为非常规
const simpleStatRareFrac = 0.05

// SimpleStatScores 简单统计量异常行分（消融对照臂）：行长 |z| + 非常规取值计数
// 行长按渲染口径的总体标准差标准化；某单元格取值在其显著列的频次低于 5% 行数记一次非常规。
// 与 PCA/T² 对照：只用一元边际统计，看不见多列组合异常——证明多元统计必要性的基线
// 纯机械、确定性、无 PCA 依赖（小表也可算）；空表返回 nil
func SimpleStatScores(rows [][]string, sigCols []int, hists []map[string]int) []float64 {
	n := len(rows)
	if n == 0 {
		return nil
	}
	costs := make([]float64, n)
	var sum float64
	for i, r := range rows {
		costs[i] = float64(renderedRowRunes(r))
		sum += costs[i]
	}
	mean := sum / float64(n)
	var ss float64
	for _, c := range costs {
		d := c - mean
		ss += d * d
	}
	sd := math.Sqrt(ss / float64(n)) // 总体口径，与 T² 的方差口径一致
	scores := make([]float64, n)
	for i, r := range rows {
		z := 0.0
		if sd > 0 {
			z = math.Abs(costs[i]-mean) / sd
		}
		rare := 0
		for _, c := range sigCols {
			if c >= len(r) || c >= len(hists) {
				continue
			}
			if float64(hists[c][r[c]]) < simpleStatRareFrac*float64(n) {
				rare++
			}
		}
		scores[i] = z + float64(rare)
	}
	return scores
}
