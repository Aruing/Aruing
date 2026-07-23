// 定位模块负责把问题中的未验证线索转换为后续诊断可以使用的已确认目标
//
// 假实现不查询真实环境、不执行工具，首轮直接根据 Query 节点提交目标
// 真实定位由 LLMResolver 通过编排可见循环提议工具并消费证据
// Target 的系统编号由编排边界发放，本模块只产出 ProposedTarget 内容
package agent

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"aruing/internal/core"
)

// 保存假定位器使用的固定身份模板，可在多次运行之间安全复用
// 每次定位按 Query 节点顺序套用模板属性，NodeID 与 RunID 始终来自当前输入
// 从而关闭 L-1：下游不再依赖解析器固定节点编号
type FakeResolver struct {
	// 按节点下标套用的目标身份模板；NodeID / RunID / EvidenceIDs 在 Next 时忽略
	templates []core.Target
}

// 使用固定身份模板创建可重复使用的假定位器
// 输入会立即复制，创建后修改原切片或属性不会影响后续定位结果
// 模板可以为空：此时仍会为每个问题节点生成仅含 NodeID 的目标
func NewFakeResolver(templates []core.Target) *FakeResolver {
	return &FakeResolver{templates: cloneTargets(templates)}
}

// 首轮直接提交基于当前问题节点的目标，忽略已有证据与轮次
// 每个节点对应一个目标：NodeID 取自节点，Type/Attrs 优先取模板同下标副本
// 上下文取消时返回错误；问题无节点时返回 fail 动作而非半成品目标
func (r *FakeResolver) Next(ctx context.Context, state ResolveState) (ResolveAction, error) {
	if err := ctx.Err(); err != nil {
		return ResolveAction{}, fmt.Errorf("resolve next: %w", err)
	}
	if r == nil {
		return ResolveAction{}, fmt.Errorf("resolve next: resolver is required")
	}
	if len(state.Query.Nodes) == 0 {
		return ResolveAction{
			Action: ResolveActionFail,
			Reason: "query has no nodes",
			Error:  "query has no nodes to resolve",
		}, nil
	}

	targets := make([]ProposedTarget, 0, len(state.Query.Nodes))
	for index, node := range state.Query.Nodes {
		if node.ID == "" {
			return ResolveAction{
				Action: ResolveActionFail,
				Reason: "node missing id",
				Error:  fmt.Sprintf("query node at index %d has empty id", index),
			}, nil
		}
		proposed := ProposedTarget{
			NodeID: node.ID,
			Type:   "resource",
			Attrs:  map[string]string{},
		}
		if index < len(r.templates) {
			tmpl := r.templates[index]
			if tmpl.Type != "" {
				proposed.Type = tmpl.Type
			}
			proposed.Attrs = maps.Clone(tmpl.Attrs)
			if proposed.Attrs == nil {
				proposed.Attrs = map[string]string{}
			}
		}
		// 假路径不调用工具，允许空 EvidenceIDs；真路径由 LLM 引用本阶段证据
		targets = append(targets, proposed)
	}

	return ResolveAction{
		Action:  ResolveActionSubmitTargets,
		Reason:  "fake resolver submits targets from query nodes",
		Targets: targets,
	}, nil
}

// 深复制目标列表中的映射和证据编号，隔离不同运行的可变数据
func cloneTargets(targets []core.Target) []core.Target {
	cloned := slices.Clone(targets)
	for index := range cloned {
		cloned[index].Attrs = maps.Clone(targets[index].Attrs)
		cloned[index].EvidenceIDs = slices.Clone(targets[index].EvidenceIDs)
	}
	return cloned
}
