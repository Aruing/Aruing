// 集群工具输出的机械投影：把 kubectl 的已知输出格式压成模型一眼可读的紧凑摘要
//
// 本文件只做按格式的机械投影，不识别资源类型、不下健康判断（判断归模型 + 编排，见硬约束 #16/#19）
// 原始完整输出仍由 tool.go 写入证据 Raw，不可变；此处产出的 Summary 是派生投影，不回写 Raw
// 表格渲染与导航算法在 internal/tools/summary（工具无关）；本包只负责解析 kubectl 输出格式

package k8s

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Aruing/Aruing/internal/tools/summary"
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
		return summary.Render(label, p.columns, p.rows, false)
	}
	if p, ok := parseTextTable(stdout); ok {
		return summary.Render(label, p.columns, p.rows, p.hasMore)
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

// 非零退出码的摘要：退出码加 stderr 首行，便于上层从摘要即看到失败原因
func errorSummary(exitCode int, stderr string) string {
	s := fmt.Sprintf("kubectl 退出码 %d", exitCode)
	if first := firstLine(stderr); first != "" {
		s += "：" + first
	}
	return s
}

// 非表格成功输出（describe、logs 等）的 fallback：首尾行预览加总行数，并提示可翻页
// 不把整段文本复制进 Summary；模型需要细节时经 evidence.read 翻页或从 Raw 读取
// 总行数按物理行计（含空行），与 evidence.read 行级切片的 total 一致，模型可据此定位窗口
func fallbackSummary(stdout string) string {
	lines := nonBlankLines(stdout)
	if len(lines) == 0 {
		return "kubectl 执行完成（非表格输出）"
	}
	total := len(strings.Split(strings.TrimRight(stdout, "\r\n"), "\n"))
	first := truncateRunes(strings.TrimSpace(lines[0]), 80)
	s := fmt.Sprintf("kubectl 输出（非表格，共 %d 行）：%s", total, first)
	if len(lines) > 1 {
		last := truncateRunes(strings.TrimSpace(lines[len(lines)-1]), 80)
		s += " … 尾行：" + last
	}
	s += " … 可用 evidence.read 按 offset/limit 翻页读原文（有 evidenceId 时）"
	return s
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
