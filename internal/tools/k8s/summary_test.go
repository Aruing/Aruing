package k8s

import (
	"fmt"
	"strings"
	"testing"
)

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
