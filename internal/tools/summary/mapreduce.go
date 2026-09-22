// 大表全覆盖 map-reduce 投影：两遍分片（Pass 1 全局基准 → 片内全局基准投影 → Reduce 归并）
//
// 单遍投影（fast / greedy）的实例行名额全局竞争：表越大稀有值论域越大而名额不变，
// 中段信号被挤掉的概率随表规模单调上升——最坏是「存在性不可见」（模型不知道有异常，
// 一脸认真误诊一切正常）。本方法用两遍结构把最坏情况压到「有存在、有地址、无本人行」：
//
//	Pass 1  全表机械扫描：列频次（ColumnHistograms）+ 稀有论域（复用 greedy 的
//	        buildCoverUniverse，w = log2(N/count)，N = 全表行数）+ 全局 T² 行分
//	分片    等行数切 S 片；S 由 chooseShardCount 候选循环精确记账动态定——把预算
//	        全部换成地址粒度；「每片 ≥1 代表行」是结构性地板（零数据行的片节纯费标题）
//	片内    判「有趣」的基准是全局基准而非片内基准（防各片自发明异常口径漂移、
//	        防全表稀有值在某片内缺席无从标记）：阶段 A 每个片内出现的全局稀有值取
//	        一个携带行（权重序，一行可代多值）；阶段 B 残余名额按全局 T² 降序 /
//	        均匀步长兜底
//	Reduce  机械归并：全局频次头 + 每片一节（片头行区间 + 稀有命中报数 + 代表行
//	        （0 基全表行号，与 evidence.read 的 SliceQuery.Offset 同口径）+ 溢出
//	        显式标注）
//
// 保证结构：G1 存在性 = 频次段全量统计；G2 定位性 = 片节报数全量（与名额无关）；
// G3 可达性 = 全表行号贯通（narrow / evidence.read / spill 盘读三路跟进）；
// G4 默认不变 = 仅显式 method=map-reduce 走本路径（专门能力非默认出口，arc 脊柱 4）。
// 全程机械、确定、零 LLM 零业务判断（#16/#18/#19）；Raw 不可变原带不动

package summary

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// MapReduceMaxShards S 候选上限：防御巨预算等病态配置（正常预算先绑）。
	// G1/G2 不依赖 S——上限只影响地址粒度，片内溢出报数仍全量
	MapReduceMaxShards = 256
	// MapReduceRareListMax 片内稀有清单展示上限：存在性由全局频次段全量保证，
	// 清单只做定位辅助；截断显式标注「另有 j 值」（#18 降级可见即不叫漏）
	MapReduceRareListMax = 8
)

// 片节各行的格式串：writeShardSection 的渲染与 chooseShardCount 的记账预留共用同一份，
// 保证「预算里扣掉的」与「实际写出来的」逐字符同源，不留口径缝
const (
	mrHeaderFmt   = "  ── 片 %d/%d · 行 %d–%d（%d 行）\n"
	mrRareFmt     = "  稀有命中 %d 行：%s\n"
	mrRowFmt      = "  #%d  %s\n"
	mrOverflowFmt = "  （命中 %d 行仅展示 %d 行；可 evidence.read offset=%d limit=%d 或按上述稀有值 --field-selector 窄化）\n"
	mrTailFmt     = "  （共 %d 片 · 代表 %d 行 / %d 行；全量在 raw）\n"
)

// mapBaseline Pass 1 全局基准：稀有论域与全局异常行分，各片共用。
// 论域复用 greedy 的覆盖论域构造（显著列非主流取值，w = log2(N/count)，N = 全表行数）；
// 不能把片内行直接喂 GreedyPick 的原因正在权重分母——其论域构造假设 hists 与 rows 同表，
// 全局 hists 配片内行会算出负权重 log2(片行数/全局计数)
type mapBaseline struct {
	uni     coverUniverse // 每行稀有键集（rowSets）+ 值权重（weight），全局口径
	anom    []float64     // 全局 T²（平方口径）；nil = PCA 不可用（行数不足 / 协方差奇异）
	rowCost []int         // 每行渲染整行 rune 长（#i 前缀含）；候选循环逐片反复装入，预计算免 35 万次 Sprintf
}

// mrRareColumns map-reduce 稀有论域的列选择：低基数且非单一取值（≥2 distinct）。
// 不用 SignificantColumns 的 dominant/N < 0.999 均匀性阈——那是 PCA 数值稳定的守护，
// 与稀有计数无关；5000 行里仅 2 次 CrashLoopBackOff 的列恰是最该进论域的，
// 主流占比 99.96% 会被 0.999 阈值深误杀。频次段渲染口径不变（RenderColumnFreq
// 本就列出全部 ≤24 distinct 值），T² 行分仍用 SignificantColumns（稳定性间题归它）
func mrRareColumns(hists []map[string]int) []int {
	var cols []int
	for i, h := range hists {
		if len(h) >= 2 && len(h) <= MaxDistinctForHist {
			cols = append(cols, i)
		}
	}
	return cols
}

// buildMapBaseline 全表机械扫描产出全局基准；零 LLM、零业务判断（#19）
func buildMapBaseline(rows [][]string, hists []map[string]int) mapBaseline {
	cost := make([]int, len(rows))
	for i, r := range rows {
		cost[i] = mrRowRunes(i, r)
	}
	return mapBaseline{
		uni:     buildCoverUniverse(rows, mrRareColumns(hists), hists, false),
		anom:    AnomalyScores(rows, SignificantColumns(hists), hists),
		rowCost: cost,
	}
}

// shardRange 全表行区间 [From, To)；行区间分片是需求钦定（G3 的区间地址）
type shardRange struct{ From, To int }

// splitShards 等行数切 S 片：边界 ⌊j·n/S⌋（整数除法，片大小差 ≤1，确定性，余数摊给尾部）
// S ≤ 0 或 n ≤ 0 返回 nil；S > n 收敛为每片 1 行
func splitShards(n, shards int) []shardRange {
	if n <= 0 || shards <= 0 {
		return nil
	}
	if shards > n {
		shards = n
	}
	out := make([]shardRange, 0, shards)
	prev := 0
	for j := 1; j <= shards; j++ {
		to := n
		if j < shards {
			to = j * n / shards
		}
		if to > prev {
			out = append(out, shardRange{prev, to})
		}
		prev = to
	}
	return out
}

// shardAgg 片内稀有聚合（全量清点，先于任何名额决策——G2 的兑现点）
type shardAgg struct {
	rareRows int              // 命中稀有值的行数
	keys     []coverKey       // 片内出现的稀有值，权重降序（并列 (col,val) 字典序）
	counts   map[coverKey]int // 稀有值 → 片内行数
	rareList []rareEntry      // 与 keys 同序的渲染形态
}

// rareEntry 片内稀有值计数：该值在片内出现的行数（定位辅助，供 --field-selector 窄化）
type rareEntry struct {
	Col   int
	Val   string
	Count int
}

// aggregateShard 片内稀有值聚合：清点不占名额，输出按全局权重降序的确定性顺序
func aggregateShard(rows [][]string, r shardRange, uni coverUniverse) shardAgg {
	var agg shardAgg
	if r.From < 0 || r.From >= r.To || r.To > len(rows) {
		return agg
	}
	agg.counts = make(map[coverKey]int)
	for i := r.From; i < r.To; i++ {
		if len(uni.rowSets[i]) > 0 {
			agg.rareRows++
		}
		for _, k := range uni.rowSets[i] {
			agg.counts[k]++
		}
	}
	for k := range agg.counts {
		agg.keys = append(agg.keys, k)
	}
	sort.Slice(agg.keys, func(i, j int) bool {
		wi, wj := uni.weight[agg.keys[i]], uni.weight[agg.keys[j]]
		if wi != wj {
			return wi > wj
		}
		if agg.keys[i].col != agg.keys[j].col {
			return agg.keys[i].col < agg.keys[j].col
		}
		return agg.keys[i].val < agg.keys[j].val
	})
	for _, k := range agg.keys {
		agg.rareList = append(agg.rareList, rareEntry{Col: k.col, Val: k.val, Count: agg.counts[k]})
	}
	return agg
}

// shardPick 单片选择结果；行号均为全表 0 基（与 evidence.read 的 SliceQuery.Offset 同口径）
type shardPick struct {
	Rows      []int       // 代表行（阶段 A + B 合并，表序）
	RareRows  int         // 命中稀有值的行数——片内全量清点，与名额无关（G2）
	RareList  []rareEntry // 片内稀有值计数，权重降序（RareListMax 截断在渲染层）
	ShownHits int         // 展示行中命中稀有值的行数（溢出判定：RareRows > ShownHits）
}

// selectShardRows 片内两阶段选择（判有趣的基准 = 全局基准，非片内基准），budgetRunes 为
// 代表行 rune 预算（片节脚手架已由 writeShardSection 扣除）：
//
//	阶段 A 稀有代表：agg.keys 权重序，每值取首个装得下的携带行——一行可代多值（其携带的
//	  其余值一并视为已代表）；某携带行装不下时继续扫同值后续行，值无装得下的携带行才判
//	  未代表（由溢出标注兜底）
//	阶段 B 异常补位：残余预算按全局 T² 降序（并列行号升序）装入；T² 不可用（nil）→
//	  均匀步长兜底（空间采样网，组合异常的兜底路径）；T² 全零时降序退化为行号序
//
// 装入按 RowRunes 口径实测；全片首行强制（照 renderLLMRerank 先例——「每片 ≥1 行」
// 结构性地板的来源）
func selectShardRows(rows [][]string, r shardRange, base *mapBaseline, agg shardAgg, budgetRunes int) shardPick {
	pick := shardPick{RareRows: agg.rareRows, RareList: agg.rareList}
	if r.From < 0 || r.From >= r.To || r.To > len(rows) {
		return pick
	}
	uni := base.uni
	// 行成本查表（测试直接构造的 baseline 无预计算时回退现算）
	costAt := func(i int) int {
		if i < len(base.rowCost) {
			return base.rowCost[i]
		}
		return mrRowRunes(i, rows[i])
	}

	chosen := make(map[int]bool, 4)
	covered := make(map[coverKey]bool, len(agg.keys))
	used := 0
	take := func(i int) bool {
		c := costAt(i)
		if len(chosen) > 0 && used+c > budgetRunes {
			return false
		}
		chosen[i] = true
		used += c
		return true
	}

	// 阶段 A：每值一个携带行
	for _, k := range agg.keys {
		if covered[k] {
			continue
		}
		for i := r.From; i < r.To; i++ {
			if chosen[i] || !rowHasKey(uni.rowSets[i], k) {
				continue
			}
			if take(i) {
				for _, kk := range uni.rowSets[i] {
					covered[kk] = true
				}
				break
			}
			// 装不下：继续找同值更短的携带行
		}
	}

	// 阶段 B：全局 T² 降序补位；不可用则均匀步长
	if base.anom != nil {
		cands := make([]int, 0, r.To-r.From)
		for i := r.From; i < r.To; i++ {
			if !chosen[i] {
				cands = append(cands, i)
			}
		}
		sort.Slice(cands, func(i, j int) bool {
			ai, aj := base.anom[cands[i]], base.anom[cands[j]]
			if ai != aj {
				return ai > aj
			}
			return cands[i] < cands[j]
		})
		for _, i := range cands {
			take(i) // 装不下的行自然跳过，更短的可能仍装得下
		}
	} else {
		// 均匀步长兜底：目标行数按剩余预算与片中位行长折算（与 renderUniform 同思路）
		rem := budgetRunes - used
		costs := make([]int, 0, r.To-r.From)
		for i := r.From; i < r.To; i++ {
			costs = append(costs, costAt(i))
		}
		target := 1
		if med := medianInt(costs); med > 0 {
			target = rem / med
		}
		if target < 1 {
			target = 1
		}
		step := (r.To - r.From) / target
		if step < 1 {
			step = 1
		}
		for i := r.From; i < r.To; i += step {
			if !chosen[i] {
				take(i)
			}
		}
	}

	for i := range chosen {
		if len(uni.rowSets[i]) > 0 {
			pick.ShownHits++
		}
		pick.Rows = append(pick.Rows, i)
	}
	sort.Ints(pick.Rows)
	return pick
}

// pickShardRows 聚合 + 选择的便捷组合（budgetRunes 为代表行预算）；单测与实验直调用
func pickShardRows(rows [][]string, r shardRange, base *mapBaseline, budgetRunes int) shardPick {
	return selectShardRows(rows, r, base, aggregateShard(rows, r, base.uni), budgetRunes)
}

// mrRowLine 代表行渲染行（含 #行号 前缀与换行）；预算记账与渲染共用同一格式串——
// 装入预算的口径必须是渲染后的整行长度，不是裸行内容（#i 前缀占 ~30–40%，低估即系统性超预算）
func mrRowLine(i int, row []string) string {
	return fmt.Sprintf(mrRowFmt, i, strings.Join(row, "  "))
}

// mrRowRunes 代表行渲染行的 rune 长度（含前缀与换行）——片内装入预算的记账口径
func mrRowRunes(i int, row []string) int {
	return utf8.RuneCountInString(mrRowLine(i, row))
}

// rowHasKey 行的稀有键集中是否含 k（rowSets 构造时已按论域过滤，命中即论域元素）
func rowHasKey(keys []coverKey, k coverKey) bool {
	for _, kk := range keys {
		if kk == k {
			return true
		}
	}
	return false
}

// mrHeaderLine 片头行（区间为闭区间展示：行 a–b，b = To−1）
func mrHeaderLine(si, sTotal int, r shardRange) string {
	return fmt.Sprintf(mrHeaderFmt, si+1, sTotal, r.From, r.To-1, r.To-r.From)
}

// mrRareLine 报数行；无稀有命中返回空串（不渲染，避免噪音）
func mrRareLine(agg shardAgg, columns []string) string {
	if agg.rareRows == 0 {
		return ""
	}
	parts := make([]string, 0, len(agg.rareList))
	for i, e := range agg.rareList {
		if i >= MapReduceRareListMax {
			parts = append(parts, fmt.Sprintf("…另有 %d 值见全局频次段", len(agg.rareList)-i))
			break
		}
		col := "?"
		if e.Col < len(columns) {
			col = columns[e.Col]
		}
		parts = append(parts, fmt.Sprintf("%s=%s×%d", col, e.Val, e.Count))
	}
	return fmt.Sprintf(mrRareFmt, agg.rareRows, strings.Join(parts, " / "))
}

// mrOverflowLine 溢出标注行；全展示（rareRows ≤ shownHits）返回空串
func mrOverflowLine(rareRows, shownHits int, r shardRange) string {
	if rareRows <= shownHits {
		return ""
	}
	return fmt.Sprintf(mrOverflowFmt, rareRows, shownHits, r.From, r.To-r.From)
}

// writeShardSection 把一个片节写入 b 并返回该片选择结果；sectionBudget 为该片节总预算
// （片头 + 报数 + 代表行 + 溢出全部计入——「片节开销计入片预算」的兑现）。
// 代表行预算 = 片预算 − 片头/报数实测 − 溢出行悲观预留（有稀有命中即预留，按
// shownHits = rareRows 的最宽位宽格式化，与实际行共用格式串；全展示时预留只是少装
// 一行，不超预算）。chooseShardCount 的候选记账与正式渲染共用本函数，成本逐字节一致
func writeShardSection(b *strings.Builder, columns []string, rows [][]string, si, sTotal int, r shardRange, base *mapBaseline, sectionBudget int) shardPick {
	agg := aggregateShard(rows, r, base.uni)

	header := mrHeaderLine(si, sTotal, r)
	rareLn := mrRareLine(agg, columns)
	reserve := utf8.RuneCountInString(header) + utf8.RuneCountInString(rareLn)
	if agg.rareRows > 0 {
		// 悲观预留：shownHits 取 rareRows（位宽最宽，实际只会更短）；不经 mrOverflowLine
		// 的无溢出早退——那会把预留塌缩成 0
		reserve += utf8.RuneCountInString(fmt.Sprintf(mrOverflowFmt, agg.rareRows, agg.rareRows, r.From, r.To-r.From))
	}
	rowBudget := sectionBudget - reserve

	pick := selectShardRows(rows, r, base, agg, rowBudget)

	b.WriteString(header)
	if rareLn != "" {
		b.WriteString(rareLn)
	}
	for _, i := range pick.Rows {
		b.WriteString(mrRowLine(i, rows[i]))
	}
	if ov := mrOverflowLine(pick.RareRows, pick.ShownHits, r); ov != "" {
		b.WriteString(ov)
	}
	return pick
}

// mrSectionPool 从 avail 扣除尾注最坏预留后的片节总预算；chooseShardCount 的记账与
// renderMapReduce 的渲染共用，保证两边的 perShard 同源——否则片节装满后尾注永远挤爆预算
func mrSectionPool(avail, n int) int {
	// 尾注按最宽位宽（片数 ≤ MaxShards、代表行数 ≤ n）悲顾预留；实际尾注只会更短
	tailWorst := utf8.RuneCountInString(fmt.Sprintf(mrTailFmt, MapReduceMaxShards, n, n))
	if pool := avail - tailWorst; pool > 0 {
		return pool
	}
	return 0
}

// chooseShardCount 候选循环精确记账动态定 S（2026-09-21 评审定稿，替代开销估计公式）：
// S 自 1 递增，逐候选按 writeShardSection 的真实输出计量各片节 rune 开销，取
// 「total ≤ avail 且每片 ≥1 代表行」的最大 S；上限 min(n, MapReduceMaxShards)。
// 无魔法常数——「每片 ≥1 行」是结构性地板而非偏好。total 随 S 近单调升（片头主导，
// 极端非单调回 dip 损失的只是粒度、无损 G1–G3），首个超限候选后停止扫描；
// avail 装不下单片时退化为 S=1 + 强制首行（诚实超出预算，#18 降级可见）
func chooseShardCount(columns []string, rows [][]string, base *mapBaseline, avail int) int {
	n := len(rows)
	if n == 0 {
		return 0
	}
	capS := minInt(n, MapReduceMaxShards)
	pool := mrSectionPool(avail, n)
	best := 0
	var scratch strings.Builder
	for s := 1; s <= capS; s++ {
		shards := splitShards(n, s)
		if len(shards) == 0 {
			break
		}
		perShard := pool / len(shards)
		if perShard < 0 {
			perShard = 0
		}
		scratch.Reset()
		ok := true
		totalRows := 0
		for si, r := range shards {
			pick := writeShardSection(&scratch, columns, rows, si, len(shards), r, base, perShard)
			if len(pick.Rows) == 0 {
				ok = false // 结构性地板被破坏（非空片至少强制 1 行，正常不可达）：该候选起全部无效
				break
			}
			totalRows += len(pick.Rows)
		}
		if !ok {
			break
		}
		// 尾注与片节同源计入候选记账（渲染时写同一行），产物总 rune 的口径才闭合
		fmt.Fprintf(&scratch, mrTailFmt, len(shards), totalRows, n)
		if utf8.RuneCountInString(scratch.String()) > avail {
			break // 近单调：更大 S 只会更超，停止扫描
		}
		best = s
	}
	if best == 0 {
		return 1 // avail 装不下单片：单片 + 强制首行，输出诚实超出
	}
	return best
}

// renderMapReduce 渲染两遍分片投影并返回观测量（RowsIncluded = 各片代表行合计；
// InstanceRunes = 行内容 rune，#行号前缀与标注行不计——与 fast 的 T² 标注不计同口径）。
// 预算口径 = 产物总 rune（标签行 + 频次段 + 全部片节 ≤ B，强制首行例外），
// 较 greedy 的实例行口径更严——同 B 下总注入不超预算
func renderMapReduce(b *strings.Builder, columns []string, rows [][]string, opts RenderOptions) RenderStats {
	b.WriteString("  （大表：map-reduce 两遍分片全覆盖——全局频次 + 分片代表行；全量在 raw；行号为全表 0 基，可 evidence.read offset 翻页 / --field-selector 窄化）\n")

	hists := ColumnHistograms(columns, rows)
	base := buildMapBaseline(rows, hists)

	// Pass 1 渲染面：全局频次段（G1 的存在性出口；与其他方法同口径，零新渲染逻辑）
	for i, col := range columns {
		if line := RenderColumnFreq(col, hists[i]); line != "" {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	n := len(rows)
	if n == 0 {
		return RenderStats{}
	}
	avail := opts.budget() - utf8.RuneCountInString(b.String())
	if avail < 0 {
		avail = 0
	}
	shards := splitShards(n, chooseShardCount(columns, rows, &base, avail))
	perShard := 0
	if len(shards) > 0 {
		perShard = mrSectionPool(avail, n) / len(shards)
	}

	var stats RenderStats
	for si, r := range shards {
		pick := writeShardSection(b, columns, rows, si, len(shards), r, &base, perShard)
		stats.RowsIncluded += len(pick.Rows)
		for _, i := range pick.Rows {
			stats.InstanceRunes += RowRunes(rows[i])
		}
	}
	if len(shards) > 0 {
		fmt.Fprintf(b, mrTailFmt, len(shards), stats.RowsIncluded, n)
	}
	return stats
}

// RenderMapReduce 渲染 map-reduce 两遍分片投影（公有 wrapper，照 RenderLarge / RenderGreedy
// 先例）；产品路径经 RenderWithOptions 方法分发到达，本 wrapper 供测试与实验直调
func RenderMapReduce(b *strings.Builder, columns []string, rows [][]string, opts RenderOptions) {
	renderMapReduce(b, columns, rows, opts)
}
