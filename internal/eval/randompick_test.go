package eval

import (
	"reflect"
	"testing"
)

// 随机消融臂：同种子逐字节可复现；结果升序、在界、不重复；k 越界钳位
func TestRandomPick(t *testing.T) {
	a := RandomPick(100, 10, 42)
	b := RandomPick(100, 10, 42)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("同种子应可复现：%v vs %v", a, b)
	}
	if len(a) != 10 {
		t.Fatalf("应抽 10 行，got %d", len(a))
	}
	seen := map[int]bool{}
	for i, x := range a {
		if x < 0 || x >= 100 {
			t.Fatalf("行号越界：%d", x)
		}
		if seen[x] {
			t.Fatalf("行号重复：%d", x)
		}
		seen[x] = true
		if i > 0 && a[i-1] > x {
			t.Fatalf("结果应升序：%v", a)
		}
	}
	// k 越界钳位：超界取 n，非正返回空
	if got := RandomPick(100, 500, 1); len(got) != 100 {
		t.Fatalf("k > n 应钳位到 n，got %d", len(got))
	}
	if got := RandomPick(100, 0, 1); got != nil {
		t.Fatalf("k <= 0 应返回空，got %v", got)
	}
	if got := RandomPick(0, 5, 1); got != nil {
		t.Fatalf("n <= 0 应返回空，got %v", got)
	}
}
