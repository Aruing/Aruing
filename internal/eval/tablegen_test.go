package eval

import (
	"strings"
	"testing"
)

// fleet 模式生成器：载体计数、根因行身份、特征值归属与复现性
func TestGenerateTableFleet(t *testing.T) {
	spec := TableSpec{Rows: 1000, RootRow: 500, Seed: 7, RootFleetPer100: 1}
	tbl, err := GenerateTable(spec)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	want := fleetSize(spec)
	if want != 10 {
		t.Fatalf("fleetSize = %d, want 10", want)
	}
	// 载体计数与归属：CrashLoopBackOff 恰好出现在 fleet 个行上，根因行必在列（钉板口径前提）
	crashRows := 0
	rootSeen := false
	for i, row := range tbl.Rows {
		if strings.Contains(strings.Join(row, " "), "CrashLoopBackOff") {
			crashRows++
			if i == spec.RootRow {
				rootSeen = true
			}
		}
	}
	if crashRows != want || !rootSeen {
		t.Fatalf("crash rows = %d (want %d), root in fleet = %v", crashRows, want, rootSeen)
	}
	if !strings.HasPrefix(tbl.Rows[spec.RootRow][0], "bad-deploy-") {
		t.Fatalf("root name = %q, want bad-deploy-*", tbl.Rows[spec.RootRow][0])
	}
	if len(tbl.RootFeatures) != 1 || tbl.RootFeatures[0] != "CrashLoopBackOff" {
		t.Fatalf("RootFeatures = %#v", tbl.RootFeatures)
	}
}

// 唯一模式（RootFleetPer100=0）与 fleet 模式同参数不同表；同参数同种子逐字节复现
func TestGenerateTableFleetReproducible(t *testing.T) {
	spec := TableSpec{Rows: 500, RootRow: 50, Seed: 3, RootFleetPer100: 1}
	a, _ := GenerateTable(spec)
	b, _ := GenerateTable(spec)
	if strings.Join(a.Rows[100], "|") != strings.Join(b.Rows[100], "|") {
		t.Fatal("same spec+seed must reproduce identical rows")
	}
}

// fleet 计数下限：小表也至少 2 个载体（单载体退化为唯一模式口径就没有挤兑形态）
func TestFleetSizeFloor(t *testing.T) {
	if got := fleetSize(TableSpec{Rows: 50, RootFleetPer100: 1}); got != 2 {
		t.Fatalf("fleet floor = %d, want 2", got)
	}
	if got := fleetSize(TableSpec{Rows: 1000}); got != 1 {
		t.Fatalf("unique mode fleet = %d, want 1", got)
	}
}
