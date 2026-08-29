// 随机选择消融臂：均匀随机抽行（种子驱动可复现）
//
// 「贪心 vs 框架内随机」消融的对照：同预算折算基数下的均匀随机选行。
// 不是产品方法，不进 config 与方法开关——只由 bench（实验侧）调用

package eval

import (
	"math/rand"
	"sort"
)

// RandomPick 从 n 行里均匀随机抽 k 行，返回升序行号
// 同 (n, k, seed) 逐次调用结果相同（可复现）；k 越界钳位：k <= 0 返回空，k > n 取 n
// 抽完按表序排序：渲染口径与其他方法一致（升序行号）
func RandomPick(n, k int, seed int64) []int {
	if n <= 0 || k <= 0 {
		return nil
	}
	if k > n {
		k = n
	}
	rng := rand.New(rand.NewSource(seed))
	perm := rng.Perm(n)
	picked := append([]int(nil), perm[:k]...)
	sort.Ints(picked)
	return picked
}
