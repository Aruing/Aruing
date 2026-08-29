// 工具无关的表格投影渲染：把 columns/rows 压成模型一眼可读的紧凑摘要
//
// 本包只做机械投影与导航抽样，不识别资源类型、不下健康判断（#16/#19）
// 调用方负责把后端 stdout 解析成 columns/rows；本包不感知 k8s/prometheus 等后端

package summary

import (
	"fmt"
	"sort"
	"strings"
)

// 大表阈值：行数超过该值时 Summary 进入「列频次 + 头/稀有/尾抽样」模式
// 全量行始终保留在 Raw，不静默丢行（#18）；展示只是派生投影，不替代 Raw
const (
	LargeTableThreshold  = 64
	LargeTableHead       = 4
	LargeTableTail       = 4
	LargeTableShowBudget = 24 // 大表实例段总展示行硬顶，避免上下文爆炸
	MaxDistinctForHist   = 24 // 列取值数超过该值时只标 distinct 总数，不列直方图
)

// 大表实例段预算分配：异常段固定 + 头尾各固定 + 剩余给覆盖段
const (
	AnomalyShowBudget = 8
	CoverShowBudget   = 12
)

// Render 渲染表格投影（fast 默认路径）；零值选项，保持既有调用方与测试零改
func Render(label string, columns []string, rows [][]string, hasMore bool) string {
	return RenderWithOptions(label, columns, rows, hasMore, RenderOptions{})
}

// RenderWithOptions 按方法分发渲染；零值选项 = fast（小表全行，大表三段式）
// full / head-tail / uniform 不受大表阈值影响：实验基线按配置直达，保证方法间口径一致
func RenderWithOptions(label string, columns []string, rows [][]string, hasMore bool, opts RenderOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %d 行 · 列: %s\n", label, len(rows), strings.Join(columns, " "))
	switch opts.Method {
	case MethodFull:
		WriteRows(&b, rows)
	case MethodHeadTail:
		renderHeadTail(&b, rows, opts.budget())
	case MethodUniform:
		renderUniform(&b, rows, opts.budget())
	case MethodGreedy, MethodGreedyKnapsack:
		RenderGreedy(&b, columns, rows, opts)
	default:
		// fast：小表全行，大表三段式（历史行为）
		if len(rows) > LargeTableThreshold {
			RenderLarge(&b, columns, rows)
		} else {
			WriteRows(&b, rows)
		}
	}
	if hasMore {
		b.WriteString("  （输出含更多段落，见 raw）\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderLarge 渲染大表投影：列频次段给整体分布、异常段按 Hotelling T² 高低给出最偏离的代表行、覆盖段按分层抽样保中段可见
// 异常段用 PCA + T²（见 anomaly.go）抓单列及多列组合异常；覆盖段保证每个非主流取值至少有 1 行代表 + 中段均匀步长补全
// 同一物理行不重复出现在三段；T² 失败（数据不足、协方差奇异）时异常段为空，全靠覆盖段兜底
func RenderLarge(b *strings.Builder, columns []string, rows [][]string) {
	b.WriteString("  （大表：PCA 异常排序 + 取值覆盖抽样；全量在 raw；可用 --field-selector / -o jsonpath 收窄）\n")

	hists := ColumnHistograms(columns, rows)
	for i, col := range columns {
		if line := RenderColumnFreq(col, hists[i]); line != "" {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	picks := PickLargeTableRows(columns, rows, hists)
	if len(picks.Anomaly) > 0 {
		fmt.Fprintf(b, "  异常高 T² %d 行（PCA 主成分空间，最偏离在前）：\n", len(picks.Anomaly))
		WriteAnomalyRows(b, rows, picks.Anomaly, picks.AnomalyScores)
	}
	fmt.Fprintf(b, "  头 %d 行：\n", LargeTableHead)
	WriteRows(b, RowsByIndex(rows, picks.Head))
	b.WriteString("  …\n")
	fmt.Fprintf(b, "  覆盖抽样 %d 行（取值代表 + 中段均匀步长）：\n", len(picks.Cover))
	WriteRows(b, RowsByIndex(rows, picks.Cover))
	b.WriteString("  …\n")
	fmt.Fprintf(b, "  尾 %d 行：\n", LargeTableTail)
	WriteRows(b, RowsByIndex(rows, picks.Tail))
}

// 加权贪心渲染：列频次段（全貌统计）+ 代表性子集（行号升序）+ 省略标注
// 频次段本身是「还有多少没展示」的全貌信息；子集按表序排列，模型可据此定位与 drill（#19 双结构）
func RenderGreedy(b *strings.Builder, columns []string, rows [][]string, opts RenderOptions) {
	b.WriteString("  （大表：加权贪心代表性投影——覆盖 + T² 异常 + 头尾锚；全量在 raw；可用 --field-selector / -o jsonpath / evidence.read 收窄）\n")

	hists := ColumnHistograms(columns, rows)
	for i, col := range columns {
		if line := RenderColumnFreq(col, hists[i]); line != "" {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	picks := GreedyPick(rows, hists, GreedyOptions{
		BudgetRunes:   opts.BudgetRunes,
		Lambda:        opts.Lambda,
		UniformWeight: opts.UniformWeight,
		Knapsack:      opts.Method == MethodGreedyKnapsack,
	})
	fmt.Fprintf(b, "  代表性子集 %d 行（头尾锚 + 加权贪心，行号升序）：\n", len(picks.Selected))
	WriteRows(b, RowsByIndex(rows, picks.Selected))
	if omitted := len(rows) - len(picks.Selected); omitted > 0 {
		fmt.Fprintf(b, "  （已省略 %d 行；全量在 raw，可用 evidence.read 翻页读取）\n", omitted)
	}
}

// renderHeadTail C2 头尾截断基线：预算对半分头尾，按行序装入，至少各保 1 行（预算允许时）
// 中段省略即业界默认形态的失真所在——位置偏差实验的对照源
func renderHeadTail(b *strings.Builder, rows [][]string, budget int) {
	n := len(rows)
	half := budget / 2
	headIdx := make([]int, 0, 8)
	used := 0
	for i := 0; i < n; i++ {
		c := renderedRowRunes(rows[i])
		if len(headIdx) > 0 && used+c > half {
			break
		}
		headIdx = append(headIdx, i)
		used += c
	}
	tailIdx := make([]int, 0, 8)
	for i := n - 1; i >= 0; i-- {
		// 头是前缀，尾倒序扫描与之相遇即全表装下
		if len(headIdx) > 0 && i == headIdx[len(headIdx)-1] {
			break
		}
		c := renderedRowRunes(rows[i])
		if len(tailIdx) > 0 && used+c > budget {
			break
		}
		tailIdx = append(tailIdx, i)
		used += c
	}
	WriteRows(b, RowsByIndex(rows, headIdx))
	if omitted := n - len(headIdx) - len(tailIdx); omitted > 0 {
		fmt.Fprintf(b, "  …（已省略 %d 行，见 raw）\n", omitted)
	}
	WriteRows(b, RowsByIndex(rows, reverseInts(tailIdx)))
}

// renderUniform C3 均匀采样基线：中位行长折算目标行数，等步长抽行，预算硬顶
// 确定性步长采样（非随机）：可复现是 #19 的卖点；实验靠根因位置变化而非采样种子制造方差
func renderUniform(b *strings.Builder, rows [][]string, budget int) {
	n := len(rows)
	target := 1
	if med := medianRowRunes(rows); med > 0 {
		target = budget / med
	}
	if target < 1 {
		target = 1
	}
	if target > n {
		target = n
	}
	stride := n / target
	if stride < 1 {
		stride = 1
	}
	idxs := make([]int, 0, target)
	used := 0
	for i := 0; i < n && len(idxs) < target; i += stride {
		c := renderedRowRunes(rows[i])
		if len(idxs) > 0 && used+c > budget {
			break
		}
		idxs = append(idxs, i)
		used += c
	}
	WriteRows(b, RowsByIndex(rows, idxs))
	if omitted := n - len(idxs); omitted > 0 {
		fmt.Fprintf(b, "  （均匀采样 %d 行，已省略 %d 行，见 raw）\n", len(idxs), omitted)
	}
}

// WriteAnomalyRows 渲染异常段：每行单元格后标 T² 量化分（平方口径，越大越离群）
// scores 为与 rows 等长的全表 T² 数组，按 idx 查；缺失时退化成无标注
func WriteAnomalyRows(b *strings.Builder, rows [][]string, idxs []int, scores []float64) {
	for _, i := range idxs {
		if i < 0 || i >= len(rows) {
			continue
		}
		b.WriteString("  ")
		b.WriteString(strings.Join(rows[i], "  "))
		if i < len(scores) {
			fmt.Fprintf(b, "  ← T²=%.2f", scores[i])
		}
		b.WriteByte('\n')
	}
}

// RenderColumnFreq 单列频次行的紧凑渲染：低基数列列「值×次数」（按次数降序、并列按原值升序）；高基数列只标 distinct 数
// 不解释值含义、不识别列名；返回空串表示该列全空（不渲染，避免噪音）
func RenderColumnFreq(col string, hist map[string]int) string {
	if len(hist) == 0 {
		return ""
	}
	if len(hist) > MaxDistinctForHist {
		return fmt.Sprintf("%s: %d distinct（略）", col, len(hist))
	}
	type entry struct {
		val   string
		count int
	}
	entries := make([]entry, 0, len(hist))
	for v, c := range hist {
		entries = append(entries, entry{v, c})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].val < entries[j].val
	})
	parts := make([]string, len(entries))
	for i, e := range entries {
		v := e.val
		if v == "" {
			v = "(empty)"
		}
		parts[i] = fmt.Sprintf("%s×%d", v, e.count)
	}
	return col + ": " + strings.Join(parts, " / ")
}

// WriteRows 把若干行单元格以两空格连接、两空格缩进写入构造器
func WriteRows(b *strings.Builder, rows [][]string) {
	for _, r := range rows {
		b.WriteString("  ")
		b.WriteString(strings.Join(r, "  "))
		b.WriteByte('\n')
	}
}
