package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bench 子命令：自定义小矩阵 → CSV + 矩阵快照落盘，摘要行写 stderr、CSV 路径写 stdout
func TestRunBenchCLI(t *testing.T) {
	dir := t.TempDir()
	matrix := filepath.Join(dir, "matrix.yaml")
	yaml := `rows: [60]
budgets: [256]
positions: [50]
seeds: [3]
methods: [greedy, head-tail, random, simplestat]
`
	if err := os.WriteFile(matrix, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write matrix: %v", err)
	}
	out := filepath.Join(dir, "sub", "bench.csv")
	var stdout, stderr bytes.Buffer
	if err := runBench([]string{"--matrix", matrix, "--out", out}, &stdout, &stderr); err != nil {
		t.Fatalf("bench: %v (stderr: %s)", err, stderr.String())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 5 { // 1 表头 + 4 单元
		t.Fatalf("CSV 应 5 行，got %d", len(lines))
	}
	if !strings.HasPrefix(lines[0], "method,N,budget,position_bucket") {
		t.Fatalf("表头不符：%s", lines[0])
	}
	// 矩阵快照与 CSV 同目录同名（-matrix 后缀）
	snap := filepath.Join(dir, "sub", "bench-matrix.yaml")
	if _, err := os.Stat(snap); err != nil {
		t.Fatalf("矩阵快照应落盘：%v", err)
	}
	if !strings.Contains(stderr.String(), "4 units") || !strings.Contains(stdout.String(), out) {
		t.Fatalf("摘要输出不符：stderr=%s stdout=%s", stderr.String(), stdout.String())
	}
}
