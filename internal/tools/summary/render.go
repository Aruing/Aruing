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

// Render 渲染表格投影为紧凑多行文本：标签 · 行数 · 列名，其后每行两空格缩进
// 小表全行写出；大表进入「列频次 + 头/稀有/尾抽样」模式，全量仍在 Raw（#18）
// 不按列名（如 STATUS/READY）改变逻辑，不解释取值含义，纯机械统计（#16/#19）
func Render(label string, columns []string, rows [][]string, hasMore bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %d 行 · 列: %s\n", label, len(rows), strings.Join(columns, " "))

	if len(rows) > LargeTableThreshold {
		RenderLarge(&b, columns, rows)
	} else {
		WriteRows(&b, rows)
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

// WriteAnomalyRows 渲染异常段：每行单元格后标 T² 量化分（让模型一眼看到「这行偏离多少 σ」）
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
