package summary

// ColumnHistograms 列频次统计：每列返回 value → count；空串保留为单独 key，渲染时呈现为 (empty)
func ColumnHistograms(columns []string, rows [][]string) []map[string]int {
	hists := make([]map[string]int, len(columns))
	for i := range columns {
		hists[i] = make(map[string]int)
	}
	for _, r := range rows {
		for i := 0; i < len(columns) && i < len(r); i++ {
			hists[i][r[i]]++
		}
	}
	return hists
}
