package eval

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 小矩阵遍历：单元数 = 各维之积；hit 与判分内核同源；观测量字段可聚合
func TestRunBench(t *testing.T) {
	m := BenchMatrix{
		Rows:      []int{100, 200},
		Budgets:   []int{512},
		Positions: []int{5, 50},
		Seeds:     []int64{1, 2},
		Methods:   []string{"greedy", "head-tail"},
	}
	res, err := RunBench(m)
	if err != nil {
		t.Fatalf("run bench: %v", err)
	}
	if len(res) != 16 {
		t.Fatalf("单元数应为 16（2×1×2×2×2），got %d", len(res))
	}
	for _, r := range res {
		if r.WallMS < 0 || r.ProjectedRows <= 0 || r.ProjectRunes <= 0 {
			t.Fatalf("观测量非法：%+v", r)
		}
		if r.RootRow != r.N*r.PositionBucket/100 {
			t.Fatalf("根因行号应按百分比折算：%+v", r)
		}
	}
	// hit 与判分内核同源：重放首个 greedy 单元核对
	first := res[0]
	tbl, err := GenerateTable(TableSpec{Rows: first.N, RootRow: first.RootRow, Seed: first.Seed})
	if err != nil {
		t.Fatalf("regen table: %v", err)
	}
	text, _, err := renderArm(first.Method, tbl, first.Budget, first.Seed)
	if err != nil {
		t.Fatalf("re-render: %v", err)
	}
	if first.Hit != ProjectionHit(text, tbl.RootName) {
		t.Fatalf("hit 列应与 ProjectionHit 一致：%+v", first)
	}
}

// CSV 写出：表头 + 每单元一行，hit 列 0/1
func TestWriteBenchCSV(t *testing.T) {
	res := []BenchResult{
		{Method: "greedy", N: 100, Budget: 512, PositionBucket: 50, RootRow: 50, Seed: 1, Hit: true, ProjectedRows: 12, ProjectRunes: 500, WallMS: 1},
		{Method: "head-tail", N: 100, Budget: 512, PositionBucket: 50, RootRow: 50, Seed: 1, Hit: false, ProjectedRows: 8, ProjectRunes: 400, WallMS: 0},
	}
	var buf bytes.Buffer
	if err := WriteBenchCSV(&buf, res); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("应 1 表头 + 2 行，got %d", len(lines))
	}
	wantHeader := "method,N,budget,position_bucket,root_row,seed,hit,projected_rows,project_runes,wall_ms"
	if lines[0] != wantHeader {
		t.Fatalf("表头不符：%s", lines[0])
	}
	if !strings.HasPrefix(lines[1], "greedy,100,512,50,50,1,1,") || !strings.HasPrefix(lines[2], "head-tail,100,512,50,50,1,0,") {
		t.Fatalf("数据行不符：%s / %s", lines[1], lines[2])
	}
}

// 矩阵校验：llm-rerank 不是机械臂（走产品路径）；未知方法、空维、越界位置都报错
func TestBenchMatrixValidate(t *testing.T) {
	if err := DefaultBenchMatrix().Validate(); err != nil {
		t.Fatalf("默认矩阵应合法：%v", err)
	}
	if got := DefaultBenchMatrix().Units(); got != 2400 {
		t.Fatalf("默认矩阵应 2400 单元，got %d", got)
	}
	cases := []struct {
		name string
		m    BenchMatrix
		want string
	}{
		{"llm-rerank 拒绝", BenchMatrix{Rows: []int{10}, Budgets: []int{512}, Positions: []int{50}, Seeds: []int64{1}, Methods: []string{"llm-rerank"}}, "llm-rerank"},
		{"未知方法", BenchMatrix{Rows: []int{10}, Budgets: []int{512}, Positions: []int{50}, Seeds: []int64{1}, Methods: []string{"magic"}}, "unknown method"},
		{"空维", BenchMatrix{Methods: []string{"fast"}}, "non-empty"},
		{"位置越界", BenchMatrix{Rows: []int{10}, Budgets: []int{512}, Positions: []int{150}, Seeds: []int64{1}, Methods: []string{"fast"}}, "percent"},
	}
	for _, c := range cases {
		err := c.m.Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s：期望报错含 %q，got %v", c.name, c.want, err)
		}
	}
}

// 矩阵文件加载：YAML 解析 + 校验联动
func TestLoadBenchMatrix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "matrix.yaml")
	yaml := `rows: [50]
budgets: [256]
positions: [50]
seeds: [7]
methods: [greedy, random]
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write matrix: %v", err)
	}
	m, err := LoadBenchMatrix(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Units() != 2 || m.Seeds[0] != 7 {
		t.Fatalf("矩阵字段不符：%+v", m)
	}
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("methods: [nope]\n"), 0o644); err != nil {
		t.Fatalf("write bad matrix: %v", err)
	}
	if _, err := LoadBenchMatrix(bad); err == nil {
		t.Fatalf("非法方法应在加载期报错")
	}
}
