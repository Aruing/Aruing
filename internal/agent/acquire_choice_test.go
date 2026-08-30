package agent

import (
	"testing"

	"github.com/Aruing/Aruing/internal/agent/acquire"
)

// B2 种子随机选择：同种子同信念必同选择（纯函数，与调用次序无关）；索引落在候选域内
func TestSeededAcquireChoiceReproducible(t *testing.T) {
	belief, err := acquire.NewUniformBelief(2)
	if err != nil {
		t.Fatalf("build belief: %v", err)
	}
	acts := make([]acquire.Action, 4)
	for i := range acts {
		// 矩阵形状只影响构造合法性；选择只依赖 (seed, 信念, 候选数)
		act, aerr := acquire.NewAction(
			[]string{"a", "b", "c", "d"}[i],
			[]string{"o1", "o2"},
			[][]float64{{0.5, 0.5}, {0.5, 0.5}},
			1,
		)
		if aerr != nil {
			t.Fatalf("build action %d: %v", i, aerr)
		}
		acts[i] = act
	}

	first := seededAcquireChoice(7, belief, acts)
	for range 3 {
		if got := seededAcquireChoice(7, belief, acts); got != first {
			t.Fatalf("同种子同信念选择不稳定：%d vs %d", got, first)
		}
	}
	if first < 0 || first >= len(acts) {
		t.Fatalf("选择越界：%d", first)
	}
	// 单候选无需随机，恒 0
	if got := seededAcquireChoice(7, belief, acts[:1]); got != 0 {
		t.Fatalf("单候选应恒 0，got %d", got)
	}
}

// B4 最低成本选择：恒选 argmin cost，并列取索引小
func TestCheapestAcquireChoice(t *testing.T) {
	proposals := []ActionProposal{
		{Name: "expensive", Cost: 5},
		{Name: "mid", Cost: 3},
		{Name: "cheap-tie-a", Cost: 1},
		{Name: "cheap-tie-b", Cost: 1},
	}
	// poolIdx 与 acts 对齐：全部候选参与
	poolIdx := []int{0, 1, 2, 3}
	if got := cheapestAcquireChoice(poolIdx, proposals); got != 2 {
		t.Fatalf("并列最低成本应取索引小者：got %d, want 2", got)
	}
	// 候选被过滤（矩阵失配跳过）时按剩余集合选
	if got := cheapestAcquireChoice([]int{0, 1}, proposals); got != 1 {
		t.Fatalf("argmin cost 选择错误：got %d, want 1", got)
	}
}
