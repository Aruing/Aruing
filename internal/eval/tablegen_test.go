package eval

import "testing"

// 同参数同种子 → 逐字节相同的表；不同种子根因名不同
func TestGenerateTableDeterministic(t *testing.T) {
	spec := TableSpec{Rows: 200, RootRow: 150, Seed: 7}
	a, err := GenerateTable(spec)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, _ := GenerateTable(spec)
	if a.RootName != b.RootName || len(a.Rows) != len(b.Rows) {
		t.Fatalf("同种子应可复现")
	}
	for i := range a.Rows {
		for j := range a.Rows[i] {
			if a.Rows[i][j] != b.Rows[i][j] {
				t.Fatalf("行 %d 列 %d 不一致", i, j)
			}
		}
	}

	c, _ := GenerateTable(TableSpec{Rows: 200, RootRow: 150, Seed: 8})
	if c.RootName == a.RootName {
		t.Fatal("不同种子根因名应不同")
	}
}

// 根因行固定在指定位且形态正确；越界报错（位置是分桶变量，不允许静默钳位）
func TestGenerateTableRootRow(t *testing.T) {
	tb, err := GenerateTable(TableSpec{Rows: 100, RootRow: 42, Seed: 1})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	row := tb.Rows[42]
	if row[0] != tb.RootName || row[2] != "CrashLoopBackOff" || row[1] != "0/1" {
		t.Fatalf("根因行形态错误：%v（want name=%s）", row, tb.RootName)
	}
	if n := countValues(tb, 2, "CrashLoopBackOff"); n != 1 {
		t.Fatalf("CrashLoopBackOff 应恰 1 行，got %d", n)
	}

	if _, err := GenerateTable(TableSpec{Rows: 100, RootRow: 100, Seed: 1}); err == nil {
		t.Fatal("RootRow 越界应报错")
	}
	if _, err := GenerateTable(TableSpec{Rows: 0, RootRow: 0, Seed: 1}); err == nil {
		t.Fatal("Rows=0 应报错")
	}
}

// 列分布符合「偏斜但非清一色」：Running 为主流、含无害 Pending 与稀有节点
func TestGenerateTableDistribution(t *testing.T) {
	tb, _ := GenerateTable(TableSpec{Rows: 300, RootRow: 150, Seed: 3})
	if n := countValues(tb, 2, "Running"); n < 280 {
		t.Fatalf("Running 应为压倒性主流，got %d/300", n)
	}
	if n := countValues(tb, 2, "Pending"); n == 0 || n > 15 {
		t.Fatalf("Pending 应少量混入，got %d", n)
	}
	if n := countValues(tb, 5, "node-9"); n == 0 || n > 15 {
		t.Fatalf("稀有节点应少量混入，got %d", n)
	}
	// NAME 唯一（高基数列，投影层自动排除）
	names := map[string]int{}
	for _, r := range tb.Rows {
		names[r[0]]++
	}
	if len(names) != 300 {
		t.Fatalf("NAME 应全唯一，got %d", len(names))
	}
}

func countValues(tb GeneratedTable, col int, val string) int {
	n := 0
	for _, r := range tb.Rows {
		if r[col] == val {
			n++
		}
	}
	return n
}
