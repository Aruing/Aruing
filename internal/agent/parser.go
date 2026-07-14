// 解析模块负责把运行中的原始问题转换为未验证的问题结构
//
// 当前实现只依赖核心领域模型，不查询真实环境、不调用工具，也不确认目标
// 调用方必须传入有效的运行数据和可取消上下文，返回结果只供后续定位阶段使用
// 假实现通过固定模板验证数据流，后续接入大模型时保持相同调用边界
package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"aruing/internal/core"
)

// 保存假解析器使用的固定问题模板，可在多次运行之间安全复用
// 每次解析都会返回独立副本，不解释自然语言，也不产生已确认目标
type FakeParser struct {
	// 解析时复制的问题模板，其中的运行编号会被当前输入覆盖
	query core.Query
}

// 使用固定问题模板创建可重复使用的假解析器
// 输入会立即复制，创建后修改原结构不会影响后续解析结果
func NewFakeParser(query core.Query) *FakeParser {
	return &FakeParser{query: cloneQuery(query)}
}

// 校验运行身份和原始问题，返回绑定当前运行编号的问题结构
// 返回值不与内部模板共享可变数据，上下文取消时直接返回对应错误
func (p *FakeParser) Parse(ctx context.Context, run core.Run) (core.Query, error) {
	if ctx == nil {
		return core.Query{}, errors.New("parser requires a context")
	}
	if err := ctx.Err(); err != nil {
		return core.Query{}, fmt.Errorf("parse query: %w", err)
	}
	if p == nil {
		return core.Query{}, errors.New("parser is required")
	}
	if strings.TrimSpace(run.ID) == "" {
		return core.Query{}, errors.New("parser requires a run ID")
	}
	if strings.TrimSpace(run.Question) == "" {
		return core.Query{}, errors.New("parser requires a question")
	}
	if strings.TrimSpace(p.query.ID) == "" {
		return core.Query{}, errors.New("parser requires a query ID")
	}

	query := cloneQuery(p.query)
	query.RunID = run.ID
	return query, nil
}

// 深复制问题结构中的切片、映射和时间指针，隔离不同运行的可变数据
func cloneQuery(query core.Query) core.Query {
	cloned := query
	// 节点和关系内部包含映射，逐项复制才能避免共享底层可变数据
	cloned.Nodes = slices.Clone(query.Nodes)
	for index := range cloned.Nodes {
		cloned.Nodes[index].Attrs = maps.Clone(query.Nodes[index].Attrs)
	}
	cloned.Edges = slices.Clone(query.Edges)
	for index := range cloned.Edges {
		cloned.Edges[index].Attrs = maps.Clone(query.Edges[index].Attrs)
	}
	// 时间端点使用指针，单独复制可防止调用方通过解引用修改模板
	if query.TimeRange != nil {
		timeRange := *query.TimeRange
		if query.TimeRange.Start != nil {
			start := *query.TimeRange.Start
			timeRange.Start = &start
		}
		if query.TimeRange.End != nil {
			end := *query.TimeRange.End
			timeRange.End = &end
		}
		cloned.TimeRange = &timeRange
	}
	return cloned
}
