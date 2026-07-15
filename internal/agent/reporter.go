// 报告模块负责把验证结果和证据引用整理为最终用户输出
//
// 当前假实现只返回固定报告，不调用大模型，也不复制证据原始内容
// 调用方必须提供同一运行中的判断和证据，报告不能改变已有判断结果
package agent

import (
	"context"
	"fmt"
	"slices"

	"aruing/internal/core"
)

// 保存假报告器使用的固定报告模板，可在多次运行之间安全复用
// 每次生成都会返回独立副本，不添加模板之外的结论和建议
type FakeReporter struct {
	// 生成时复制的报告模板，运行编号和结论证据会根据实际输入重新绑定
	report core.Report
}

// 使用固定报告模板创建可重复使用的假报告器
// 输入会立即复制，创建后修改原结构不会影响后续报告
func NewFakeReporter(report core.Report) *FakeReporter {
	return &FakeReporter{report: cloneReport(report)}
}

// 校验报告结论与已有判断和证据一致，并复制判断中的实际证据编号
// 上下文取消、判断矛盾或引用无效时返回错误，不产生不完整报告
func (r *FakeReporter) Report(
	ctx context.Context,
	run core.Run,
	verdicts []core.Verdict,
	evidence []core.Evidence,
) (core.Report, error) {
	if err := ctx.Err(); err != nil {
		return core.Report{}, fmt.Errorf("build report: %w", err)
	}

	verdictsByHypothesis := make(map[string]core.Verdict, len(verdicts))
	for _, verdict := range verdicts {
		if verdict.RunID != run.ID {
			return core.Report{}, fmt.Errorf("verdict %q belongs to run %q, not %q", verdict.ID, verdict.RunID, run.ID)
		}
		if verdict.HypothesisID != "" {
			verdictsByHypothesis[verdict.HypothesisID] = verdict
		}
	}
	evidenceByID := make(map[string]core.Evidence, len(evidence))
	for _, item := range evidence {
		if item.ID != "" {
			evidenceByID[item.ID] = item
		}
	}

	report := cloneReport(r.report)
	for index := range report.Conclusions {
		conclusion := &report.Conclusions[index]
		verdict, exists := verdictsByHypothesis[conclusion.HypothesisID]
		if !exists {
			return core.Report{}, fmt.Errorf("report references unknown hypothesis verdict %q", conclusion.HypothesisID)
		}
		if conclusion.Result != verdict.Result {
			return core.Report{}, fmt.Errorf("report result for hypothesis %q does not match verdict", conclusion.HypothesisID)
		}
		if len(verdict.EvidenceIDs) == 0 {
			return core.Report{}, fmt.Errorf("report conclusion for hypothesis %q requires evidence", conclusion.HypothesisID)
		}
		for _, evidenceID := range verdict.EvidenceIDs {
			item, exists := evidenceByID[evidenceID]
			if !exists {
				return core.Report{}, fmt.Errorf("report references unknown evidence %q", evidenceID)
			}
			if item.RunID != run.ID {
				return core.Report{}, fmt.Errorf("evidence %q belongs to run %q, not %q", item.ID, item.RunID, run.ID)
			}
		}
		conclusion.EvidenceIDs = slices.Clone(verdict.EvidenceIDs)
	}
	report.RunID = run.ID
	return report, nil
}

// 深复制报告中的结论、证据编号和建议，隔离不同运行的可变数据
func cloneReport(report core.Report) core.Report {
	cloned := report
	cloned.Conclusions = slices.Clone(report.Conclusions)
	for index := range cloned.Conclusions {
		cloned.Conclusions[index].EvidenceIDs = slices.Clone(report.Conclusions[index].EvidenceIDs)
	}
	cloned.Suggestions = slices.Clone(report.Suggestions)
	return cloned
}
