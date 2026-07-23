// 规划模块负责根据问题结构和已确认目标生成候选猜想与待执行任务
//
// FakePlanner 只依赖核心领域模型，不调用大模型，也不执行任何工具
// 真实现见 LLMPlanner（planner_llm.go）：同一 Plan 边界，单次 LLM 调用，工具规格来自 Registry.Specs
// 调用方必须先完成目标定位，返回的猜想和任务会绑定到同一次运行
// 计划结果只在进程内传递，内部实体仍通过运行编号独立保存
package agent

import (
	"context"
	"fmt"
	"slices"

	"aruing/internal/core"
)

// 汇总一次规划产生的候选猜想和待执行任务，只作为模块间返回值
// 该结构不是新的持久化实体，调用方应分别保存其中的核心数据
type Plan struct {
	// 本轮需要通过证据验证的候选原因列表
	Hypotheses []core.Hypothesis
	// 为定位信息或验证猜想而生成的工具任务列表
	Tasks []core.Task
}

// 保存假规划器使用的固定计划模板，可在多次运行之间安全复用
// 每次规划都会返回独立副本，不推断模板之外的猜想或任务
type FakePlanner struct {
	// 规划时复制的结果模板，其中的运行编号会被当前问题覆盖
	plan Plan
}

// 使用固定计划模板创建可重复使用的假规划器
// 输入会立即复制，创建后修改原结构不会影响后续规划结果
func NewFakePlanner(plan Plan) *FakePlanner {
	return &FakePlanner{plan: clonePlan(plan)}
}

// 校验任务引用并返回绑定当前运行编号的独立计划
// 上下文取消或引用未知数据时返回错误，不产生部分规划结果
func (p *FakePlanner) Plan(ctx context.Context, query core.Query, targets []core.Target) (Plan, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, fmt.Errorf("plan tasks: %w", err)
	}

	plan := clonePlan(p.plan)
	for index := range plan.Hypotheses {
		plan.Hypotheses[index].RunID = query.RunID
	}

	// 引用集合只包含当前输入和本轮猜想，防止任务关联不存在的数据
	knownRefs := make(map[string]struct{}, 1+len(query.Nodes)+len(query.Edges)+len(targets)+len(plan.Hypotheses))
	if query.ID != "" {
		knownRefs[query.ID] = struct{}{}
	}
	for _, node := range query.Nodes {
		if node.ID != "" {
			knownRefs[node.ID] = struct{}{}
		}
	}
	for _, edge := range query.Edges {
		if edge.ID != "" {
			knownRefs[edge.ID] = struct{}{}
		}
	}
	for _, target := range targets {
		if target.RunID != query.RunID {
			return Plan{}, fmt.Errorf("target %q belongs to run %q, not %q", target.ID, target.RunID, query.RunID)
		}
		if target.ID != "" {
			knownRefs[target.ID] = struct{}{}
		}
	}
	for _, hypothesis := range plan.Hypotheses {
		if hypothesis.ID != "" {
			knownRefs[hypothesis.ID] = struct{}{}
		}
	}

	for index := range plan.Tasks {
		task := &plan.Tasks[index]
		for _, ref := range task.Refs {
			if _, exists := knownRefs[ref]; !exists {
				return Plan{}, fmt.Errorf("task %q references unknown data %q", task.ID, ref)
			}
		}
		task.RunID = query.RunID
	}
	return plan, nil
}

// 深复制计划中的所有切片和原始参数，隔离不同运行的可变数据
func clonePlan(plan Plan) Plan {
	cloned := Plan{
		Hypotheses: slices.Clone(plan.Hypotheses),
		Tasks:      slices.Clone(plan.Tasks),
	}
	for index := range cloned.Hypotheses {
		cloned.Hypotheses[index].ExpectedSignals = slices.Clone(plan.Hypotheses[index].ExpectedSignals)
	}
	for index := range cloned.Tasks {
		cloned.Tasks[index].Refs = slices.Clone(plan.Tasks[index].Refs)
		cloned.Tasks[index].Arguments = slices.Clone(plan.Tasks[index].Arguments)
		cloned.Tasks[index].DependsOn = slices.Clone(plan.Tasks[index].DependsOn)
	}
	return cloned
}
