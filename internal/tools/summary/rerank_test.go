package summary

import (
	"errors"
	"strings"
	"testing"
)

// C4 对照臂渲染：nil 回调与回调失败都在投影内明确标注，不静默回退；成功回调的行进投影
func TestRenderLLMRerank(t *testing.T) {
	cols, rows := buildLargeTableRareMid(100, 50, "Running", "Error")
	base := RenderOptions{Method: MethodLLMRerank}

	// 装配层已拦 nil；渲染层防御性报错文本（第二层防线）
	if got := RenderWithOptions("pods", cols, rows, false, base); !strings.Contains(got, "配置错误") {
		t.Fatalf("nil 回调应防御性报错，got:\n%s", got)
	}

	// 回调失败：错误进投影文本，Raw 不受影响、诊断可继续
	fail := base
	fail.Rerank = func([]string, [][]string, int) ([]int, error) { return nil, errors.New("boom") }
	if got := RenderWithOptions("pods", cols, rows, false, fail); !strings.Contains(got, "LLM 重排失败") || !strings.Contains(got, "boom") {
		t.Fatalf("回调失败应明确标注错误，got:\n%s", got)
	}

	// 成功回调：选中行进投影 + 省略标注；越界与重复被机械过滤
	call := base
	call.Rerank = func([]string, [][]string, int) ([]int, error) { return []int{50, 999, 50, 0}, nil }
	text, stats := RenderWithStats("pods", cols, rows, false, call)
	if !strings.Contains(text, "p-050") || !strings.Contains(text, "p-000") {
		t.Fatalf("投影应包含回调选中的行，got:\n%s", text)
	}
	if !strings.Contains(text, "已省略") {
		t.Fatalf("未选中行应有省略标注")
	}
	if stats.RowsIncluded != 2 || stats.InstanceRunes <= 0 {
		t.Fatalf("越界/重复过滤后应恰 2 行，got %+v", stats)
	}

	// 预算裁剪：只装得下 1 行时后续行跳过（首行保底，与其他方法的装入语义一致）
	tiny := base
	tiny.BudgetRunes = renderedRowRunes(rows[0])
	tiny.Rerank = func([]string, [][]string, int) ([]int, error) { return []int{0, 50, 1}, nil }
	if _, stats := RenderWithStats("pods", cols, rows, false, tiny); stats.RowsIncluded != 1 {
		t.Fatalf("小预算应只装 1 行，got %+v", stats)
	}
}

// RenderWithStats 与各方法选择器同源：greedy 观测量等于 GreedyPick 结果；full 全行
func TestRenderWithStats(t *testing.T) {
	cols, rows := buildLargeTableRareMid(100, 50, "Running", "Error")
	hists := ColumnHistograms(cols, rows)
	picks := GreedyPick(rows, hists, GreedyOptions{BudgetRunes: 2048})
	text, stats := RenderWithStats("pods", cols, rows, false, RenderOptions{Method: MethodGreedy, BudgetRunes: 2048})
	if stats.RowsIncluded != len(picks.Selected) || stats.InstanceRunes != picks.TotalRunes {
		t.Fatalf("greedy 观测量应与 GreedyPick 同源：stats=%+v picks=%d/%d", stats, len(picks.Selected), picks.TotalRunes)
	}
	if !strings.Contains(text, "p-050") {
		t.Fatalf("贪心投影应包含中段异常行")
	}
	_, full := RenderWithStats("pods", cols, rows, false, RenderOptions{Method: MethodFull})
	if full.RowsIncluded != len(rows) {
		t.Fatalf("full 应统计全行，got %d", full.RowsIncluded)
	}
}
