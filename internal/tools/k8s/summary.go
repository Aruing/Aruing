// 集群工具输出的机械投影：把 kubectl 的已知输出格式压成模型一眼可读的紧凑摘要
//
// 本文件只做按格式的机械投影，不识别资源类型、不下健康判断（判断归模型 + 编排，见硬约束 #16/#19）
// 原始完整输出仍由 tool.go 写入证据 Raw，不可变；此处产出的 Summary 是派生投影，不回写 Raw

package k8s

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// 大表阈值：行数超过该值时 Summary 进入「列频次 + 头/稀有/尾抽样」模式
// 全量行始终保留在 Raw，不静默丢行（#18）；展示只是派生投影，不替代 Raw
const (
	largeTableThreshold  = 64
	largeTableHead       = 4
	largeTableTail       = 4
	largeTableShowBudget = 24 // 大表实例段总展示行硬顶，避免上下文爆炸
	maxDistinctForHist   = 24 // 列取值数超过该值时只标 distinct 总数，不列直方图
)

var (
	// 连续非空白片段，用于定位表头各列名与起始位置
	nonSpaceRun = regexp.MustCompile(`\S+`)
)

// 表格投影结果，JSON Table 与文本表两种解析路径统一汇入该结构后渲染
type tableProjection struct {
	// 列名，按输出顺序
	columns []string
	// 数据行，每行已切成与 columns 等长的单元格字符串
	rows [][]string
	// 输出在首张表之后还含更多段落（如 `get all` 多资源），提醒模型见 Raw
	hasMore bool
}

// 计算一次集群调用的紧凑摘要
// 解析顺序：非零退出码先走错误摘要；再尝试 JSON Table；再尝试文本表；都不匹配走非表格 fallback
// 入参为已截断到保留上限的 stdout/stderr；输出缓冲截断的提示由调用方在末尾追加，与本函数无关
func projectSummary(argv []string, stdout, stderr string, exitCode int) string {
	if exitCode != 0 {
		return errorSummary(exitCode, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		return "kubectl 执行完成，exitCode=0（无输出）"
	}
	label := tableLabel(argv)
	if p, ok := parseJSONTable(stdout); ok {
		return renderTable(label, p.columns, p.rows, false)
	}
	if p, ok := parseTextTable(stdout); ok {
		return renderTable(label, p.columns, p.rows, p.hasMore)
	}
	return fallbackSummary(stdout)
}

// 机械回显资源标签：返回 `get` 之后首个位置参数（资源类型）原样
// 判定规则：跳过以 `-` 开头的标志；若上一词是标志，则当前词视为该标志的值一并跳过
// 不识别具体标志名（不枚举 kubectl 旗标），因此布尔标志后跟的资源会被漏掉，此时回落到 table
// 找不到时回落到通用标签 table，绝不把标志值误标成资源
func tableLabel(argv []string) string {
	getIdx := -1
	for i, a := range argv {
		if a == "get" {
			getIdx = i
			break
		}
	}
	if getIdx < 0 {
		return "table"
	}
	prevWasFlag := false
	for j := getIdx + 1; j < len(argv); j++ {
		tok := argv[j]
		if strings.HasPrefix(tok, "-") {
			prevWasFlag = true
			continue
		}
		if prevWasFlag {
			prevWasFlag = false
			continue
		}
		return tok
	}
	return "table"
}

// 解析 `-o json` 的 meta.k8s.io Table 对象为投影
// 仅读取 columnDefinitions.name 与 rows.cells；未知字段忽略以兼容自定义列与扩展
// 非 Table 或缺列定义时返回 ok=false，交由后续路径处理
func parseJSONTable(stdout string) (tableProjection, bool) {
	trimmed := strings.TrimSpace(stdout)
	if !strings.HasPrefix(trimmed, "{") {
		return tableProjection{}, false
	}
	var doc jsonTable
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		return tableProjection{}, false
	}
	if !strings.EqualFold(doc.Kind, "Table") || len(doc.ColumnDefinitions) == 0 {
		return tableProjection{}, false
	}
	columns := make([]string, len(doc.ColumnDefinitions))
	for i, c := range doc.ColumnDefinitions {
		columns[i] = c.Name
	}
	rows := make([][]string, 0, len(doc.Rows))
	for _, r := range doc.Rows {
		cells := make([]string, len(r.Cells))
		for i, raw := range r.Cells {
			cells[i] = stringifyCell(raw)
		}
		rows = append(rows, cells)
	}
	return tableProjection{columns: columns, rows: rows}, true
}

// 集群命令 -o json 输出的 Table 对象结构，只声明投影所需字段
type jsonTable struct {
	// 资源种类，Table 表示表格化结果
	Kind string `json:"kind"`
	// 列定义，每列的名称用于表头
	ColumnDefinitions []jsonColumnDef `json:"columnDefinitions"`
	// 数据行，cells 与 columnDefinitions 按位置对应
	Rows []jsonTableRow `json:"rows"`
}

// 表格列定义，只取名称
type jsonColumnDef struct {
	Name string `json:"name"`
}

// 表格行，单元格保留原始 JSON 以便按类型还原成紧凑文本
type jsonTableRow struct {
	// 与列定义按位置对应的单元格原始 JSON 值
	Cells []json.RawMessage `json:"cells"`
}

// 把单个单元格的原始 JSON 还原成紧凑文本
// 字符串去引号；数字、布尔、null 原样；对象、数组保留紧凑 JSON（罕见，模型可查 Raw 取细节）
func stringifyCell(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return string(raw)
}

// 解析默认文本表格输出为投影
// 用表头各列起始位置切分数据行，可容忍单元格填满列宽压缩列间空格的情况
// 只投影首段连续表格；遇到空行或结构不符的行即止，若其后仍有内容则置 hasMore 提醒见 Raw
func parseTextTable(stdout string) (tableProjection, bool) {
	lines := strings.Split(strings.TrimRight(stdout, "\r\n"), "\n")

	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	if start >= len(lines) {
		return tableProjection{}, false
	}

	names, colStarts, ok := headerColumns(lines[start])
	if !ok {
		return tableProjection{}, false
	}

	rows := make([][]string, 0)
	hasMore := false
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			hasMore = anyNonBlankAfter(lines, i+1)
			break
		}
		cells := splitByStarts(line, colStarts)
		if countNonEmpty(cells) < 2 {
			hasMore = true
			break
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return tableProjection{}, false
	}
	return tableProjection{columns: names, rows: rows, hasMore: hasMore}, true
}

// 判断表头是否像表格：至少两个列名、且列名之间由两个以上空格分隔
// 满足时返回列名与每个列名的起始字节偏移；否则该行不当作表头
func headerColumns(header string) (names []string, starts []int, ok bool) {
	matches := nonSpaceRun.FindAllStringIndex(header, -1)
	if len(matches) < 2 {
		return nil, nil, false
	}
	for i := 1; i < len(matches); i++ {
		gap := matches[i][0] - matches[i-1][1]
		if gap < 2 {
			return nil, nil, false
		}
	}
	names = make([]string, len(matches))
	starts = make([]int, len(matches))
	for i, m := range matches {
		name := header[m[0]:m[1]]
		// 列名以冒号结尾多为 describe 式 key:value，不是资源表格表头
		if strings.HasSuffix(name, ":") {
			return nil, nil, false
		}
		names[i] = name
		starts[i] = m[0]
	}
	return names, starts, true
}

// 按列起始偏移切分一行：末列取行尾其余，其余列取到下一列起点
// 超出行长的起点对应空单元格
func splitByStarts(line string, starts []int) []string {
	cells := make([]string, len(starts))
	for i, s := range starts {
		if s >= len(line) {
			cells[i] = ""
			continue
		}
		seg := line[s:]
		if i+1 < len(starts) {
			end := starts[i+1]
			if end > len(line) {
				end = len(line)
			}
			seg = line[s:end]
		}
		cells[i] = strings.TrimSpace(seg)
	}
	return cells
}

// 渲染表格投影为紧凑多行文本：标签 · 行数 · 列名，其后每行两空格缩进
// 小表全行写出；大表进入「列频次 + 头/稀有/尾抽样」模式，全量仍在 Raw（#18）
// 不按列名（如 STATUS/READY）改变逻辑，不解释取值含义，纯机械统计（#16/#19）
func renderTable(label string, columns []string, rows [][]string, hasMore bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %d 行 · 列: %s\n", label, len(rows), strings.Join(columns, " "))

	if len(rows) > largeTableThreshold {
		renderLargeTable(&b, columns, rows)
	} else {
		writeRows(&b, rows)
	}

	if hasMore {
		b.WriteString("  （输出含更多段落，见 raw）\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// 渲染大表投影：列频次段给整体分布、异常段按 Hotelling T² 高低给出最偏离的代表行、覆盖段按分层抽样保中段可见
// 异常段用 PCA + T²（见 anomaly.go）抓单列及多列组合异常；覆盖段保证每个非主流取值至少有 1 行代表 + 中段均匀步长补全
// 同一物理行不重复出现在三段；T² 失败（数据不足、协方差奇异）时异常段为空，全靠覆盖段兜底
func renderLargeTable(b *strings.Builder, columns []string, rows [][]string) {
	b.WriteString("  （大表：PCA 异常排序 + 取值覆盖抽样；全量在 raw；可用 --field-selector / -o jsonpath 收窄）\n")

	hists := columnHistograms(columns, rows)
	for i, col := range columns {
		if line := renderColumnFreq(col, hists[i]); line != "" {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	picks := pickLargeTableRows(columns, rows, hists)
	if len(picks.anomaly) > 0 {
		fmt.Fprintf(b, "  异常高 T² %d 行（PCA 主成分空间，最偏离在前）：\n", len(picks.anomaly))
		writeAnomalyRows(b, rows, picks.anomaly, picks.anomalyScores)
	}
	fmt.Fprintf(b, "  头 %d 行：\n", largeTableHead)
	writeRows(b, rowsByIndex(rows, picks.head))
	b.WriteString("  …\n")
	fmt.Fprintf(b, "  覆盖抽样 %d 行（取值代表 + 中段均匀步长）：\n", len(picks.cover))
	writeRows(b, rowsByIndex(rows, picks.cover))
	b.WriteString("  …\n")
	fmt.Fprintf(b, "  尾 %d 行：\n", largeTableTail)
	writeRows(b, rowsByIndex(rows, picks.tail))
}

// 渲染异常段：每行单元格后标 T² 量化分（让模型一眼看到「这行偏离多少 σ」）
// scores 为与 rows 等长的全表 T² 数组，按 idx 查；缺失时退化成无标注
func writeAnomalyRows(b *strings.Builder, rows [][]string, idxs []int, scores []float64) {
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

// 列频次统计：每列返回 value → count；空串保留为单独 key，渲染时呈现为 (empty)
func columnHistograms(columns []string, rows [][]string) []map[string]int {
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

// 单列频次行的紧凑渲染：低基数列列「值×次数」（按次数降序、并列按原值升序）；高基数列只标 distinct 数
// 不解释值含义、不识别列名；返回空串表示该列全空（不渲染，避免噪音）
func renderColumnFreq(col string, hist map[string]int) string {
	if len(hist) == 0 {
		return ""
	}
	if len(hist) > maxDistinctForHist {
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

// 大表实例段抽样结果：anomaly / head / cover / tail 的行索引切片
// anomaly 按 T² score 降序（最异常在前）；anomalyScores 与 rows 等长，nil 表示 PCA 未启用
type largeTablePicks struct {
	anomaly       []int
	anomalyScores []float64
	head          []int
	cover         []int
	tail          []int
}

// 大表实例段预算分配：异常段固定 + 头尾各固定 + 剩余给覆盖段
const (
	anomalyShowBudget = 8
	coverShowBudget   = 12
)

// 大表实例段抽样：先固定头/尾，再用 PCA + T² 选异常段，最后用覆盖段（取值代表 + 均匀步长）填中段
// 三段通过 used 索引集合去重；同一物理行不重复出现
// 异常段在覆盖段之前选：异常行优先占异常段，避免覆盖段把名额花光
func pickLargeTableRows(columns []string, rows [][]string, hists []map[string]int) largeTablePicks {
	n := len(rows)
	headEnd := minInt(largeTableHead, n)
	tailStart := n - largeTableTail
	if tailStart < headEnd {
		tailStart = headEnd
	}
	used := make(map[int]struct{}, largeTableShowBudget+anomalyShowBudget)

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

	anomaly := pickAnomalyRows(rows, hists, headEnd, tailStart, anomalyShowBudget, used)
	cover := pickCoverRows(rows, hists, headEnd, tailStart, coverShowBudget, used)

	anomalyScores := anomalyScoresForRender(rows, hists)
	return largeTablePicks{anomaly: anomaly, anomalyScores: anomalyScores, head: head, cover: cover, tail: tail}
}

// anomalyScoresForRender 重新计算一次 T² 分数供渲染标注用
// 与 pickAnomalyRows 内部调用一致；为保持函数职责单一，渲染所需 scores 单独算
// 若 PCA 未启用（数据不足、协方差奇异）返回 nil
func anomalyScoresForRender(rows [][]string, hists []map[string]int) []float64 {
	sigCols := significantColumns(hists)
	if len(sigCols) == 0 {
		return nil
	}
	return anomalyScores(rows, sigCols, hists)
}

// 异常段：在 [from, to) 区间内按 PCA + Hotelling T² 排序选至多 budget 行
// 失败（数据不足、协方差奇异等见 anomalyScores 兜底）时返回 nil
// 选出的行立刻加入 used，避免被后续覆盖段重复挑中
func pickAnomalyRows(rows [][]string, hists []map[string]int, from, to, budget int, used map[int]struct{}) []int {
	if budget <= 0 || from >= to {
		return nil
	}
	sigCols := significantColumns(hists)
	if len(sigCols) == 0 {
		return nil
	}
	scores := anomalyScores(rows, sigCols, hists)
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

// 覆盖段：先满足「每个区分性列的每个非主流取值至少 1 行代表」硬约束，剩余预算按中段均匀步长补
// 区分性列 = 低基数（distinct ≤ maxDistinctForHist）且非清一色的列（见 significantColumns）
// 同一物理行不重复出现（通过 used 去重）
func pickCoverRows(rows [][]string, hists []map[string]int, from, to, budget int, used map[int]struct{}) []int {
	if budget <= 0 || from >= to {
		return nil
	}
	sigCols := significantColumns(hists)
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

// 把若干行索引对应的行按序挑出，供 writeRows 直接消费
func rowsByIndex(rows [][]string, idxs []int) [][]string {
	out := make([][]string, 0, len(idxs))
	for _, i := range idxs {
		if i >= 0 && i < len(rows) {
			out = append(out, rows[i])
		}
	}
	return out
}

// 把若干行单元格以两空格连接、两空格缩进写入构造器
func writeRows(b *strings.Builder, rows [][]string) {
	for _, r := range rows {
		b.WriteString("  ")
		b.WriteString(strings.Join(r, "  "))
		b.WriteByte('\n')
	}
}

// 非零退出码的摘要：退出码加 stderr 首行，便于上层从摘要即看到失败原因
func errorSummary(exitCode int, stderr string) string {
	s := fmt.Sprintf("kubectl 退出码 %d", exitCode)
	if first := firstLine(stderr); first != "" {
		s += "：" + first
	}
	return s
}

// 非表格成功输出（describe、logs 等）的 fallback：首行预览加总行数并指向 Raw
// 不把整段文本复制进 Summary，模型需要细节时从 Raw 读取
func fallbackSummary(stdout string) string {
	lines := nonBlankLines(stdout)
	if len(lines) == 0 {
		return "kubectl 执行完成（非表格输出）"
	}
	first := truncateRunes(strings.TrimSpace(lines[0]), 80)
	return fmt.Sprintf("kubectl 输出（非表格，共 %d 行）：%s … 见 raw", len(lines), first)
}

// 取输出中的首个非空行，去掉首尾空白；无内容时返回空串
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return truncateRunes(t, 120)
		}
	}
	return ""
}

// 收集所有非空行，原样保留行内空白，仅按整行空白判定
func nonBlankLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// 判断从指定位置起是否还存在非空行，用于多段输出提醒
func anyNonBlankAfter(lines []string, from int) bool {
	for i := from; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return true
		}
	}
	return false
}

// 统计单元格中非空的数量，用于判定一行是否像表格数据
func countNonEmpty(cells []string) int {
	n := 0
	for _, c := range cells {
		if c != "" {
			n++
		}
	}
	return n
}

// 按码点截断字符串并在截断时加省略号，避免按字节切断多字节字符
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
