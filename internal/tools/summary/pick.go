package summary

import "sort"

// LargeTablePicks 大表实例段抽样结果：Anomaly / Head / Cover / Tail 的行索引切片
// Anomaly 按 T² score 降序（最异常在前）；AnomalyScores 与 rows 等长，nil 表示 PCA 未启用
type LargeTablePicks struct {
	Anomaly       []int
	AnomalyScores []float64
	Head          []int
	Cover         []int
	Tail          []int
}

// PickLargeTableRows 大表实例段抽样：先固定头/尾，再用 PCA + T² 选异常段，最后用覆盖段（取值代表 + 均匀步长）填中段
// 三段通过 used 索引集合去重；同一物理行不重复出现
// 异常段在覆盖段之前选：异常行优先占异常段，避免覆盖段把名额花光
func PickLargeTableRows(columns []string, rows [][]string, hists []map[string]int) LargeTablePicks {
	n := len(rows)
	headEnd := minInt(LargeTableHead, n)
	tailStart := n - LargeTableTail
	if tailStart < headEnd {
		tailStart = headEnd
	}
	used := make(map[int]struct{}, LargeTableShowBudget+AnomalyShowBudget)

	head := make([]int, 0, headEnd)
	for i := 0; i < headEnd; i++ {
		head = append(head, i)
		used[i] = struct{}{}
	}
	tail := make([]int, 0, n-tailStart)
	for i := tailStart; i < n; i++ {
		tail = append(tail, i)
		used[i] = struct{}{}
	}

	anomaly := PickAnomalyRows(rows, hists, headEnd, tailStart, AnomalyShowBudget, used)
	cover := PickCoverRows(rows, hists, headEnd, tailStart, CoverShowBudget, used)

	scores := anomalyScoresForRender(rows, hists)
	return LargeTablePicks{Anomaly: anomaly, AnomalyScores: scores, Head: head, Cover: cover, Tail: tail}
}

// anomalyScoresForRender 重新计算一次 T² 分数供渲染标注用
// 与 PickAnomalyRows 内部调用一致；为保持函数职责单一，渲染所需 scores 单独算
// 若 PCA 未启用（数据不足、协方差奇异）返回 nil
func anomalyScoresForRender(rows [][]string, hists []map[string]int) []float64 {
	sigCols := SignificantColumns(hists)
	if len(sigCols) == 0 {
		return nil
	}
	return AnomalyScores(rows, sigCols, hists)
}

// PickAnomalyRows 异常段：在 [from, to) 区间内按 PCA + Hotelling T² 排序选至多 budget 行
// 失败（数据不足、协方差奇异等见 AnomalyScores 兜底）时返回 nil
// 选出的行立刻加入 used，避免被后续覆盖段重复挑中
func PickAnomalyRows(rows [][]string, hists []map[string]int, from, to, budget int, used map[int]struct{}) []int {
	if budget <= 0 || from >= to {
		return nil
	}
	sigCols := SignificantColumns(hists)
	if len(sigCols) == 0 {
		return nil
	}
	scores := AnomalyScores(rows, sigCols, hists)
	if scores == nil {
		return nil
	}
	type scored struct {
		idx   int
		score float64
	}
	cands := make([]scored, 0, to-from)
	for i := from; i < to; i++ {
		cands = append(cands, scored{i, scores[i]})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].idx < cands[j].idx
	})
	picked := make([]int, 0, budget)
	for _, c := range cands {
		if len(picked) >= budget {
			break
		}
		if _, ok := used[c.idx]; ok {
			continue
		}
		picked = append(picked, c.idx)
		used[c.idx] = struct{}{}
	}
	// 不做 sort.Ints：保留 score 降序，让最异常的行排在异常段顶部（产品语义）
	return picked
}

// PickCoverRows 覆盖段：先满足「每个区分性列的每个非主流取值至少 1 行代表」硬约束，剩余预算按中段均匀步长补
// 区分性列 = 低基数（distinct ≤ MaxDistinctForHist）且非清一色的列（见 SignificantColumns）
// 同一物理行不重复出现（通过 used 去重）
func PickCoverRows(rows [][]string, hists []map[string]int, from, to, budget int, used map[int]struct{}) []int {
	if budget <= 0 || from >= to {
		return nil
	}
	sigCols := SignificantColumns(hists)
	picked := make([]int, 0, budget)
	// 阶段 1：取值覆盖——每个区分性列的每个非主流取值选第一个出现的行
	for _, c := range sigCols {
		if c >= len(hists) {
			continue
		}
		// 列内众数（count 最大）= 主流；其余为非主流，要保证代表
		var dominantCount int
		for _, cnt := range hists[c] {
			if cnt > dominantCount {
				dominantCount = cnt
			}
		}
		seenValues := make(map[string]struct{})
		for i := from; i < to && len(picked) < budget; i++ {
			if _, ok := used[i]; ok {
				continue
			}
			if c >= len(rows[i]) {
				continue
			}
			v := rows[i][c]
			cnt := hists[c][v]
			if cnt == dominantCount {
				continue // 主流取值由头尾覆盖，不在覆盖段重复
			}
			if _, dup := seenValues[v]; dup {
				continue
			}
			seenValues[v] = struct{}{}
			picked = append(picked, i)
			used[i] = struct{}{}
		}
	}
	// 阶段 2：中段均匀步长补全剩余预算
	if len(picked) < budget {
		gap := to - from
		step := max(1, gap/(budget+1))
		for i := from + step; i < to && len(picked) < budget; i += step {
			if _, ok := used[i]; ok {
				continue
			}
			picked = append(picked, i)
			used[i] = struct{}{}
		}
	}
	sort.Ints(picked)
	return picked
}

// RowsByIndex 把若干行索引对应的行按序挑出，供 WriteRows 直接消费
func RowsByIndex(rows [][]string, idxs []int) [][]string {
	out := make([][]string, 0, len(idxs))
	for _, i := range idxs {
		if i >= 0 && i < len(rows) {
			out = append(out, rows[i])
		}
	}
	return out
}
