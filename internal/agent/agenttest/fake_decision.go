// 决策规划与强度判定的测试替身：固定决策模板 + 按关键词表脚本化的 (d,s) 判定
// 与既有假角色同边界：不调模型、不执行工具，行为确定可断言
package agenttest

import (
	"context"
	"fmt"
	"strings"

	"github.com/Aruing/Aruing/internal/agent"
	"github.com/Aruing/Aruing/internal/core"
)

// 可复用的假决策规划器，始终返回构造时给定的决策模板（按次克隆）
type FakeDecisionPlanner struct {
	// 固定决策模板（按次克隆）
	decision agent.PlanDecision
}

// 使用固定决策模板创建可重复使用的假决策规划器
// 模板中的假设编号应已由测试预填；调用时只补运行绑定
func NewFakeDecisionPlanner(decision agent.PlanDecision) *FakeDecisionPlanner {
	return &FakeDecisionPlanner{decision: clonePlanDecision(decision)}
}

// 校验运行编号后返回绑定当前运行的独立决策
func (p *FakeDecisionPlanner) PlanDecision(ctx context.Context, state agent.PlanState) (agent.PlanDecision, error) {
	if ctx == nil {
		return agent.PlanDecision{}, fmt.Errorf("decision planner requires a context")
	}
	if err := ctx.Err(); err != nil {
		return agent.PlanDecision{}, fmt.Errorf("plan decision: %w", err)
	}
	if p == nil {
		return agent.PlanDecision{}, fmt.Errorf("decision planner is required")
	}
	if strings.TrimSpace(state.Query.RunID) == "" {
		return agent.PlanDecision{}, fmt.Errorf("decision planner requires a run ID")
	}

	decision := clonePlanDecision(p.decision)
	for i := range decision.Hypotheses {
		decision.Hypotheses[i].RunID = state.Query.RunID
	}
	return decision, nil
}

// 强度判定脚本：某假设遇到证据文本命中关键词时回放固定 (d,s)
// 规则按声明顺序匹配，首条命中生效；全部未命中回放无关 (0, 0)
type StrengthRule struct {
	// 触发判定的假设系统编号
	HypothesisID string
	// 证据文本命中关键词即触发（匹配范围：summary / error / commandView / raw）
	Keyword string
	// 回放方向：+1 支持 / 0 无关 / −1 反驳
	Direction int
	// 回放强度 [0,1]
	Strength float64
}

// 按关键词表回放强度判定：逐假设扫描规则，命中即回放，未命中回放无关
// 与真实 JudgeStrength 同契约：每条输入假设恰好一条判定，顺序与输入一致
func (v *FakeVerifier) JudgeStrength(
	ctx context.Context,
	evidence core.Evidence,
	hypotheses []core.Hypothesis,
) ([]agent.StrengthJudgement, error) {
	if ctx == nil {
		return nil, fmt.Errorf("strength judgement requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("judge strength: %w", err)
	}
	if v == nil {
		return nil, fmt.Errorf("verifier is required")
	}
	if len(hypotheses) == 0 {
		return nil, fmt.Errorf("strength judgement requires at least one hypothesis")
	}

	judgements := make([]agent.StrengthJudgement, 0, len(hypotheses))
	for _, h := range hypotheses {
		judgement := agent.StrengthJudgement{HypothesisID: h.ID, Direction: 0, Strength: 0}
		for _, rule := range v.StrengthRules {
			if rule.HypothesisID != h.ID {
				continue
			}
			if strengthEvidenceContains(evidence, rule.Keyword) {
				judgement.Direction = rule.Direction
				judgement.Strength = rule.Strength
				break
			}
		}
		judgements = append(judgements, judgement)
	}
	return judgements, nil
}

// 关键词是否命中证据的可观察文本（raw 以字符串化整体匹配）
func strengthEvidenceContains(evidence core.Evidence, keyword string) bool {
	if keyword == "" {
		return false
	}
	return strings.Contains(evidence.Summary, keyword) ||
		strings.Contains(evidence.Error, keyword) ||
		strings.Contains(evidence.CommandView, keyword) ||
		strings.Contains(string(evidence.Raw), keyword)
}

// 深拷贝决策模板：假设、动作名册与矩阵都不与模板共享底层数组
func clonePlanDecision(decision agent.PlanDecision) agent.PlanDecision {
	cloned := agent.PlanDecision{
		Hypotheses:     make([]core.Hypothesis, len(decision.Hypotheses)),
		Actions:        make([]agent.ActionProposal, len(decision.Actions)),
		DroppedActions: decision.DroppedActions,
	}
	copy(cloned.Hypotheses, decision.Hypotheses)
	for i := range cloned.Hypotheses {
		cloned.Hypotheses[i].ExpectedSignals = append([]string(nil), decision.Hypotheses[i].ExpectedSignals...)
	}
	for i, a := range decision.Actions {
		cloned.Actions[i] = agent.ActionProposal{
			Name:     a.Name,
			Argv:     append([]string(nil), a.Argv...),
			Ask:      a.Ask,
			Purpose:  a.Purpose,
			Cost:     a.Cost,
			Outcomes: append([]string(nil), a.Outcomes...),
		}
		cloned.Actions[i].Matrix = make([][]float64, len(a.Matrix))
		for j, row := range a.Matrix {
			cloned.Actions[i].Matrix[j] = append([]float64(nil), row...)
		}
	}
	return cloned
}
