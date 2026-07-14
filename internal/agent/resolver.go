// 定位模块负责把问题中的未验证线索转换为后续诊断可以使用的已确认目标
//
// 当前假实现只依赖核心领域模型，不查询真实环境，也不执行工具任务
// 调用方传入问题结构，定位结果必须引用其中已有节点并绑定同一次运行
// 真实定位流程需要多轮消费任务和证据，因此此处不提前定义公共接口
package agent

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"aruing/internal/core"
)

// 保存假定位器使用的固定目标模板，可在多次运行之间安全复用
// 每次定位都会返回独立副本，不执行查询，也不补充模板之外的身份信息
type FakeResolver struct {
	// 定位时复制的目标模板，其中的运行编号会被当前问题覆盖
	targets []core.Target
}

// 使用固定目标模板创建可重复使用的假定位器
// 输入会立即复制，创建后修改原切片或属性不会影响后续定位结果
func NewFakeResolver(targets []core.Target) *FakeResolver {
	return &FakeResolver{targets: cloneTargets(targets)}
}

// 校验每个目标的来源节点，并返回绑定当前运行编号的独立目标列表
// 上下文取消时不再产生结果，来源节点缺失时返回错误而不是继续诊断
func (r *FakeResolver) Resolve(ctx context.Context, query core.Query) ([]core.Target, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("resolve targets: %w", err)
	}

	// 节点编号是目标回溯用户线索的边界，先建立索引避免逐个目标反复扫描
	nodeIDs := make(map[string]struct{}, len(query.Nodes))
	for _, node := range query.Nodes {
		nodeIDs[node.ID] = struct{}{}
	}

	targets := cloneTargets(r.targets)
	for index := range targets {
		target := &targets[index]
		if _, exists := nodeIDs[target.NodeID]; !exists {
			return nil, fmt.Errorf("target %q references unknown node %q", target.ID, target.NodeID)
		}
		target.RunID = query.RunID
	}
	return targets, nil
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
