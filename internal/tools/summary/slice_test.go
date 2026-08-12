package summary

import (
	"testing"
)

// 典型窗口：offset/limit 切出子区间，total 为全表行数
func TestSliceRowsBasic(t *testing.T) {
	cols := []string{"NAME", "STATUS"}
	rows := [][]string{
		{"a", "Running"},
		{"b", "Pending"},
		{"c", "Error"},
		{"d", "Running"},
	}
	outCols, outRows, total, offset, limit := SliceRows(cols, rows, 1, 2)
	if total != 4 || offset != 1 || limit != 2 {
		t.Fatalf("meta total=%d offset=%d limit=%d", total, offset, limit)
	}
	if len(outCols) != 2 || outCols[0] != "NAME" {
		t.Fatalf("columns: %#v", outCols)
	}
	if len(outRows) != 2 || outRows[0][0] != "b" || outRows[1][0] != "c" {
		t.Fatalf("rows: %#v", outRows)
	}
}

// 边界：负 offset、超界 offset、非正 limit、超 MaxSliceLimit
func TestSliceRowsClamps(t *testing.T) {
	cols := []string{"N"}
	rows := [][]string{{"0"}, {"1"}, {"2"}}

	_, out, total, offset, limit := SliceRows(cols, rows, -5, 0)
	if total != 3 || offset != 0 || limit != DefaultSliceLimit {
		t.Fatalf("neg/zero: total=%d offset=%d limit=%d", total, offset, limit)
	}
	if len(out) != 3 {
		t.Fatalf("default limit should take all 3 rows, got %d", len(out))
	}

	_, out, _, offset, _ = SliceRows(cols, rows, 99, 10)
	if offset != 3 || len(out) != 0 {
		t.Fatalf("past end: offset=%d len=%d", offset, len(out))
	}

	_, _, _, _, limit = SliceRows(cols, rows, 0, MaxSliceLimit+50)
	if limit != MaxSliceLimit {
		t.Fatalf("limit clamp: got %d want %d", limit, MaxSliceLimit)
	}
}

// 空表
func TestSliceRowsEmpty(t *testing.T) {
	_, out, total, offset, limit := SliceRows(nil, nil, 0, 10)
	if total != 0 || offset != 0 || limit != 10 || len(out) != 0 {
		t.Fatalf("empty: total=%d offset=%d limit=%d len=%d", total, offset, limit, len(out))
	}
}
