// 定义用户问题在理解阶段使用的开放数据结构
//
// 这些结构只保存从原始问题中提取的目标、现象和关系，不表示相关对象已经在真实环境中得到确认
// 资源类型、关系类型和扩展属性都使用开放值，避免新增场景时持续修改核心字段
package core

import "time"

// 保存理解模块对一次用户问题的结构化表达，并通过运行编号关联原始问题
// 该结构可以包含不完整或存在歧义的信息，后续定位模块负责确认真实目标
type Query struct {
	// 问题结构编号，格式为问题前缀加时间有序全局唯一标识，创建时生成
	ID string `json:"id"`
	// 所属运行编号，用于关联原始问题和后续定位结果
	RunID string `json:"runId"`
	// 用户希望完成的目标，使用自然语言表达，不限制为预定义操作
	Goal string `json:"goal,omitempty"`
	// 从问题中提取的对象和现象，类型及属性允许按领域扩展
	Nodes []Node `json:"nodes,omitempty"`
	// 对象和现象之间的有向关系，关系类型不受枚举限制
	Edges []Edge `json:"edges,omitempty"`
	// 用户明确提供的时间范围，空表示问题中没有时间约束
	TimeRange *TimeRange `json:"timeRange,omitempty"`
	// 问题结构创建时间，用于记录理解结果的产生顺序
	CreatedAt time.Time `json:"createdAt"`
}

// 表达用户问题中出现的任意对象或现象，只记录线索而不声明其真实存在
// 具体领域可以通过开放类型和带前缀的属性补充信息，不需要修改该结构
type Node struct {
	// 节点编号，格式为节点前缀加时间有序全局唯一标识，供关系和后续任务引用
	ID string `json:"id"`
	// 开放的节点类型，例如资源或症状，未知类型也可以原样保存
	Type string `json:"type,omitempty"`
	// 用户问题中与该节点相关的原始文字或简短表达
	Text string `json:"text,omitempty"`
	// 领域扩展属性，键应使用稳定前缀避免不同系统之间重名
	Attrs map[string]string `json:"attrs,omitempty"`
}

// 表达两个节点之间的有向关系，用于保留调用、依赖或现象归属等上下文
// 关系本身仍是未验证线索，后续模块只能把它作为定位或诊断依据
type Edge struct {
	// 关系编号，格式为关系前缀加时间有序全局唯一标识，供任务和证据准确引用
	ID string `json:"id"`
	// 起点节点编号，表示关系从哪个对象出发
	From string `json:"from"`
	// 终点节点编号，表示关系指向哪个对象
	To string `json:"to"`
	// 开放的关系类型，例如调用或依赖，不限制为预定义集合
	Type string `json:"type,omitempty"`
	// 关系扩展属性，用于保存症状、协议或其他领域信息
	Attrs map[string]string `json:"attrs,omitempty"`
}
