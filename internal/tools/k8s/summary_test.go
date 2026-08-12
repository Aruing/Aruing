package k8s

import (
	"fmt"
	"strings"
	"testing"
)

// 构造 N 行表格：默认所有行 STATUS=commonVal、READY=readyVal；可选 midIdx 行单列异常
// 用于验证异常代表行能否从中段被模型看清
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

// 构造 N 行三列表：除指定 midIdx 行外其余全 (READY=readyOK, STATUS=statusOK)
// midIdx 行同时取 readyBad + statusBad（多列组合异常）
// 用于验证 PCA 能抓单维度算法漏掉的组合异常
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

// 把字符串右补空格到指定列宽，构造列对齐的表格行，模拟 kubectl 默认表格输出
func padCell(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

// 按列宽拼一行，列间两个空格，保证表头与数据行列起点一致
func tableLine(cells []string, widths []int) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		parts[i] = padCell(c, widths[i])
	}
	return strings.Join(parts, "  ")
}

// 各投影路径应把工具输出压成模型一眼可读的摘要，且不静默丢行
func TestProjectSummary(t *testing.T) {
	podWidths := []int{14, 6, 16, 9, 5}
	podHeader := tableLine([]string{"NAME", "READY", "STATUS", "RESTARTS", "AGE"}, podWidths)
	podRow1 := tableLine([]string{"demo-api-7c8f9", "0/1", "CrashLoopBackOff", "7", "30m"}, podWidths)
	podRow2 := tableLine([]string{"demo-api-9d1ab", "1/1", "Running", "0", "10m"}, podWidths)

	// 70 行的大表，用于验证头尾截断与 narrow 引导
	var largeRows strings.Builder
	largeRows.WriteString(tableLine([]string{"NAME", "STATUS"}, []int{10, 10}))
	for i := 0; i < 70; i++ {
		largeRows.WriteString("\n" + tableLine([]string{fmt.Sprintf("row-%02d", i), "Running"}, []int{10, 10}))
	}

	// get all 多段输出：首段表格后跟空行与第二段
	multiWidths := []int{16, 8, 8, 5}
	multi := tableLine([]string{"NAME", "READY", "STATUS", "AGE"}, multiWidths) + "\n" +
		tableLine([]string{"pod/demo-a", "1/1", "Running", "5m"}, multiWidths) + "\n\n" +
		tableLine([]string{"NAME", "DESIRED", "CURRENT", "AGE"}, multiWidths) + "\n" +
		tableLine([]string{"deployment/x", "1", "1", "5m"}, multiWidths) + "\n"

	jsonTable := `{"kind":"Table","apiVersion":"meta.k8s.io/v1",` +
		`"columnDefinitions":[{"name":"NAME"},{"name":"READY"},{"name":"STATUS"}],` +
		`"rows":[{"cells":["demo-api","0/1","CrashLoopBackOff"]},{"cells":["web","1/1","Running"]}]}`

	tests := []struct {
		name     string
		argv     []string
		stdout   string
		stderr   string
		exitCode int
		wantHas  []string // 摘要应包含的关键片段
		wantNot  []string // 摘要不应包含的片段
	}{
		{
			name:    "json table projected",
			argv:    []string{"get", "pods", "-o", "json"},
			stdout:  jsonTable,
			wantHas: []string{"pods · 2 行", "NAME READY STATUS", "CrashLoopBackOff", "demo-api"},
			wantNot: []string{"执行完成"},
		},
		{
			name:    "text table projected",
			argv:    []string{"get", "pods"},
			stdout:  podHeader + "\n" + podRow1 + "\n" + podRow2 + "\n",
			wantHas: []string{"pods · 2 行", "NAME READY STATUS RESTARTS AGE", "CrashLoopBackOff", "demo-api-9d1ab"},
			wantNot: []string{"执行完成"},
		},
		{
			name:    "describe falls back",
			argv:    []string{"describe", "pod", "demo-api"},
			stdout:  "Name:         demo-api\nNamespace:    default\n",
			wantHas: []string{"非表格", "见 raw", "demo-api"},
			wantNot: []string{"行 · 列"},
		},
		{
			name:    "logs falls back",
			argv:    []string{"logs", "demo-api"},
			stdout:  "2026-08-11T01:02:03Z starting\n2026-08-11T01:02:04Z ready\n",
			wantHas: []string{"非表格", "见 raw"},
			wantNot: []string{"行 · 列"},
		},
		{
			name:    "large table shows frequency + PCA anomaly + coverage sampling",
			argv:    []string{"get", "pods"},
			stdout:  largeRows.String(),
			wantHas: []string{"70 行", "大表：PCA 异常排序", "--field-selector", "头 4 行", "尾 4 行", "覆盖抽样", "STATUS: Running×70", "70 distinct（略）", "row-00", "row-69"},
			wantNot: []string{"仅展示前"},
		},
		{
			name:    "multi section projects first table and notes more",
			argv:    []string{"get", "all"},
			stdout:  multi,
			wantHas: []string{"all · 1 行", "pod/demo-a", "输出含更多段落，见 raw"},
			wantNot: []string{"deployment/x"},
		},
		{
			name:     "non-zero exit yields error summary with stderr",
			argv:     []string{"get", "ns"},
			stdout:   "",
			stderr:   "Error from server: not found\n",
			exitCode: 3,
			wantHas:  []string{"kubectl 退出码 3", "Error from server: not found"},
			wantNot:  []string{"执行完成"},
		},
		{
			name:    "empty stdout success",
			argv:    []string{"get", "pods"},
			stdout:  "",
			wantHas: []string{"exitCode=0", "无输出"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := projectSummary(tt.argv, tt.stdout, tt.stderr, tt.exitCode)
			for _, want := range tt.wantHas {
				if !strings.Contains(got, want) {
					t.Errorf("summary missing %q\ngot:\n%s", want, got)
				}
			}
			for _, bad := range tt.wantNot {
				if strings.Contains(got, bad) {
					t.Errorf("summary should not contain %q\ngot:\n%s", bad, got)
				}
			}
		})
	}
}

// 资源标签只机械回显 get 后首个非标志参数，不映射、不规范化
func TestTableLabel(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{name: "plain get", argv: []string{"get", "pods"}, want: "pods"},
		{name: "flags before resource", argv: []string{"get", "-n", "default", "deploy", "-o", "wide"}, want: "deploy"},
		{name: "resource with group", argv: []string{"get", "deployment.apps/foo"}, want: "deployment.apps/foo"},
		{name: "non get falls back", argv: []string{"describe", "pod"}, want: "table"},
		{name: "empty argv", argv: nil, want: "table"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tableLabel(tt.argv); got != tt.want {
				t.Errorf("tableLabel(%v) = %q, want %q", tt.argv, got, tt.want)
			}
		})
	}
}

// 大表单列异常代表行：100 行只有 1 行 STATUS=Error 位于中段，Summary 必须把它带入异常段或覆盖段
// 验证模型不 narrow 也能从中段看见异常（诊断主线阻塞项的核心目标）
func TestRenderLargeTableRareMidRowSurfaces(t *testing.T) {
	cols, rows := buildLargeTableRareMid(100, 50, "Running", "Error")
	got := renderTable("pods", cols, rows, false)

	if !strings.Contains(got, "pods · 100 行") {
		t.Fatalf("missing row count\ngot:\n%s", got)
	}
	if !strings.Contains(got, "STATUS: Running×99 / Error×1") {
		t.Fatalf("missing frequency histogram for STATUS\ngot:\n%s", got)
	}
	// p-050 应出现在异常段或覆盖段（取值代表）；头 4 是 p-000..p-003、尾 4 是 p-096..p-099 不含 p-050
	if !strings.Contains(got, "p-050  Error") {
		t.Fatalf("rare mid row p-050/Error not surfaced\ngot:\n%s", got)
	}
	// 同一行只能出现一次（去重）
	if strings.Count(got, "p-050") != 1 {
		t.Fatalf("rare row should appear exactly once, got %d\n%s", strings.Count(got, "p-050"), got)
	}
}

// 大表组合异常：100 行里中段 1 行同时 READY=0/1 + STATUS=CrashLoopBackOff
// 单维度算法可能漏（READY 和 STATUS 各自的少数派不显眼时）；PCA 在主成分空间抓组合偏离
func TestRenderLargeTableComboAnomalySurfaces(t *testing.T) {
	cols, rows := buildLargeTableComboAnomaly(100, 50, "1/1", "Running", "0/1", "CrashLoopBackOff")
	got := renderTable("pods", cols, rows, false)

	if !strings.Contains(got, "pods · 100 行") {
		t.Fatalf("missing row count\ngot:\n%s", got)
	}
	// 异常行必须出现在异常段或覆盖段
	if !strings.Contains(got, "p-050  0/1  CrashLoopBackOff") {
		t.Fatalf("combo-anomaly row p-050 not surfaced\ngot:\n%s", got)
	}
}

// 大表全常态：所有行同值，无异常信号；PCA 走兜底（方差为 0）
// 此时应由覆盖段均匀步长铺满中段，不能退化成只头尾可见
func TestRenderLargeTableAllNormalCoversMid(t *testing.T) {
	cols := []string{"NAME", "STATUS"}
	rows := make([][]string, 100)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("p-%03d", i), "Running"}
	}
	got := renderTable("pods", cols, rows, false)

	// 中段必须有覆盖段且行数 = coverShowBudget
	if !strings.Contains(got, fmt.Sprintf("覆盖抽样 %d 行", coverShowBudget)) {
		t.Fatalf("missing coverage section with budget=%d\ngot:\n%s", coverShowBudget, got)
	}
	// 覆盖段索引应跨越中段（不全是头几个）：至少出现 p-050 之后的某行
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
// 不解释值含义、不识别列名（#16/#19）
func TestColumnHistogramsAndRenderFreq(t *testing.T) {
	cols := []string{"NAME", "STATUS", "NOTE"}
	rows := [][]string{
		{"a", "Running", ""},
		{"b", "Running", ""},
		{"c", "Error", ""},
		{"d", "Running", ""},
	}
	hists := columnHistograms(cols, rows)
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
			if got := renderColumnFreq(tt.col, tt.hist); got != tt.want {
				t.Errorf("renderColumnFreq = %q, want %q", got, tt.want)
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
	hists := columnHistograms(cols, rows)

	picks := pickLargeTableRows(cols, rows, hists)
	if len(picks.head) != largeTableHead {
		t.Errorf("head len = %d, want %d", len(picks.head), largeTableHead)
	}
	if len(picks.tail) != largeTableTail {
		t.Errorf("tail len = %d, want %d", len(picks.tail), largeTableTail)
	}
	// V 列 dominant=100/100=1.0，不构成区分性列；NAME 100 distinct 超阈值
	// → 无区分性列 → 异常段为空、覆盖段走均匀步长补全 coverShowBudget 行
	if len(picks.cover) != coverShowBudget {
		t.Errorf("cover len = %d, want %d", len(picks.cover), coverShowBudget)
	}

	// 所有段互斥：同一物理行不重复
	seen := make(map[int]string)
	check := func(name string, idxs []int) {
		for _, i := range idxs {
			if _, ok := seen[i]; ok {
				t.Errorf("index %d duplicated in %s and %s", i, seen[i], name)
			}
			seen[i] = name
		}
	}
	check("anomaly", picks.anomaly)
	check("head", picks.head)
	check("cover", picks.cover)
	check("tail", picks.tail)

	// 头必须是前 head 个；尾必须是后 tail 个
	if picks.head[0] != 0 || picks.head[len(picks.head)-1] != largeTableHead-1 {
		t.Errorf("head indices = %v, want 0..%d", picks.head, largeTableHead-1)
	}
	if picks.tail[0] != 100-largeTableTail || picks.tail[len(picks.tail)-1] != 99 {
		t.Errorf("tail indices = %v, want %d..99", picks.tail, 100-largeTableTail)
	}
}

func mapFor(vals ...string) map[string]int {
	m := make(map[string]int, len(vals))
	for _, v := range vals {
		m[v] = 1
	}
	return m
}
