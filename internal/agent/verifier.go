// 判断模块负责把候选猜想、执行任务和已有证据转换为可回溯的验证结果
//
// 当前假实现只返回固定判断，不调用大模型，也不补充证据之外的事实
// 调用方必须提供同一轮规划的猜想、任务和证据，所有引用都要属于同一次运行
package agent

import (
	"context"
	"fmt"
	"slices"

	"aruing/internal/core"
)

// 保存假判断器使用的固定结果模板，可在多次运行之间安全复用
// 每次验证都会返回独立副本，不根据输入内容重新推断结论
type FakeVerifier struct {
	// 验证时复制的结果模板，运行编号和证据编号会根据实际输入重新绑定
	verdicts []core.Verdict
}

// 使用固定判断模板创建可重复使用的假判断器
// 输入会立即复制，创建后修改原切片不会影响后续验证结果
func NewFakeVerifier(verdicts []core.Verdict) *FakeVerifier {
	return &FakeVerifier{verdicts: cloneVerdicts(verdicts)}
}

// 根据任务引用找到每个猜想的实际证据，并返回绑定动态证据编号的独立结果
// 上下文取消、任务没有产出证据或数据跨运行时返回错误，不产生部分结果
func (v *FakeVerifier) Verify(
	ctx context.Context,
	hypotheses []core.Hypothesis,
	tasks []core.Task,
	evidence []core.Evidence,
) ([]core.Verdict, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("verify evidence: %w", err)
	}

	hypothesesByID := make(map[string]core.Hypothesis, len(hypotheses))
	for _, hypothesis := range hypotheses {
		if hypothesis.ID != "" {
			hypothesesByID[hypothesis.ID] = hypothesis
		}
	}
	evidenceByTaskID := make(map[string]core.Evidence, len(evidence))
	for _, item := range evidence {
		if item.TaskID != "" {
			evidenceByTaskID[item.TaskID] = item
		}
	}

	verdicts := cloneVerdicts(v.verdicts)
	for index := range verdicts {
		verdict := &verdicts[index]
		hypothesis, exists := hypothesesByID[verdict.HypothesisID]
		if !exists {
			return nil, fmt.Errorf("verdict %q references unknown hypothesis %q", verdict.ID, verdict.HypothesisID)
		}

		verdict.EvidenceIDs = verdict.EvidenceIDs[:0]
		for _, task := range tasks {
			if !slices.Contains(task.Refs, hypothesis.ID) {
				continue
			}
			if task.RunID != hypothesis.RunID {
				return nil, fmt.Errorf("task %q belongs to run %q, not %q", task.ID, task.RunID, hypothesis.RunID)
			}
			item, exists := evidenceByTaskID[task.ID]
			if !exists {
				return nil, fmt.Errorf("task %q has no evidence", task.ID)
			}
			if item.ID == "" {
				return nil, fmt.Errorf("task %q produced evidence without an ID", task.ID)
			}
			if item.RunID != hypothesis.RunID {
				return nil, fmt.Errorf("evidence %q belongs to run %q, not %q", item.ID, item.RunID, hypothesis.RunID)
			}
			verdict.EvidenceIDs = append(verdict.EvidenceIDs, item.ID)
		}
		if len(verdict.EvidenceIDs) == 0 {
			return nil, fmt.Errorf("verdict %q requires evidence", verdict.ID)
		}
		verdict.RunID = hypothesis.RunID
	}
	return verdicts, nil
}

// 深复制判断结果中的证据编号列表，隔离不同运行的可变数据
func cloneVerdicts(verdicts []core.Verdict) []core.Verdict {
	cloned := slices.Clone(verdicts)
	for index := range cloned {
		cloned[index].EvidenceIDs = slices.Clone(verdicts[index].EvidenceIDs)
	}
	return cloned
}
