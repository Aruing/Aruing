// 定义定位阶段通过真实证据确认的目标结构
//
// 目标只保存稳定的来源关系、开放类型和身份属性，不绑定具体资源系统或工具实现
package core

import "time"

// 表达定位模块已经确认的真实对象，可以作为后续规划和诊断的输入
// 每个目标都应保留原始问题线索和定位证据，确保确认过程可以回溯
type Target struct {
	// 目标编号，格式为 target_ + UUIDv7，创建时生成
	ID string `json:"id"`
	// 所属运行编号，用于关联问题结构和后续诊断数据
	RunID string `json:"runId"`
	// 产生该目标的节点编号，用于回溯用户问题中的原始线索
	NodeID string `json:"nodeId"`

	// 开放的目标类型，由具体领域定义，不限制为预定义资源集合
	Type string `json:"type"`
	// 已确认的身份属性，键应使用稳定前缀避免不同系统之间重名
	Attrs map[string]string `json:"attrs"`
	// 定位过程中用于确认该目标的证据编号
	EvidenceIDs []string `json:"evidenceIds"`

	// 目标创建时间，用于记录定位结果的产生顺序
	CreatedAt time.Time `json:"createdAt"`
}
