// 判断模块负责把候选猜想和已有证据转换为可回溯的验证结果
//
// 当前假实现只返回固定判断，不调用大模型，也不补充证据之外的事实
// 调用方必须提供已保存的猜想和证据，所有引用都要属于同一次运行
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
	// 验证时复制的结果模板，其中的运行编号会由对应猜想决定
	verdicts []core.Verdict
}

// 使用固定判断模板创建可重复使用的假判断器
// 输入会立即复制，创建后修改原切片不会影响后续验证结果
func NewFakeVerifier(verdicts []core.Verdict) *FakeVerifier {
	return &FakeVerifier{verdicts: cloneVerdicts(verdicts)}
}

// 校验判断引用的猜想和证据，并返回绑定正确运行编号的独立结果
// 上下文取消、引用悬空或证据跨运行时返回错误，不产生部分结果
func (v *FakeVerifier) Verify(
	ctx context.Context,
	hypotheses []core.Hypothesis,
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
	evidenceByID := make(map[string]core.Evidence, len(evidence))
	for _, item := range evidence {
		if item.ID != "" {
			evidenceByID[item.ID] = item
		}
	}

	verdicts := cloneVerdicts(v.verdicts)
	for index := range verdicts {
		verdict := &verdicts[index]
		hypothesis, exists := hypothesesByID[verdict.HypothesisID]
		if !exists {
			return nil, fmt.Errorf("verdict %q references unknown hypothesis %q", verdict.ID, verdict.HypothesisID)
		}
		if len(verdict.EvidenceIDs) == 0 {
			return nil, fmt.Errorf("verdict %q requires evidence", verdict.ID)
		}
		for _, evidenceID := range verdict.EvidenceIDs {
			item, exists := evidenceByID[evidenceID]
			if !exists {
				return nil, fmt.Errorf("verdict %q references unknown evidence %q", verdict.ID, evidenceID)
			}
			if item.RunID != hypothesis.RunID {
				return nil, fmt.Errorf("evidence %q belongs to run %q, not %q", item.ID, item.RunID, hypothesis.RunID)
			}
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
