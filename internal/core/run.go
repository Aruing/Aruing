// 定义诊断系统的领域模型
//
// 这里保存所有模块共同理解的数据结构，包括运行、目标范围、故障猜想、证据任务、证据、验证结果和报告
// 核心模型不依赖命令行、网络入口、集群客户端、大模型客户端或存储实现
//
// 当前阶段只定义假闭环需要的最小结构，先保证问题、猜想、证据、结论、报告能串起来
//
// 所有实体的 ID 字段采用"前缀 + UUIDv7"格式，例如 run_01945a3b-8c4e-7f3a-2b99-4d1e6a0c5b22
// 前缀标明实体类型，UUIDv7 提供时间有序性和全局唯一性
// ID 在实体创建时由 Go 侧生成，同时直接作为数据库主键使用，不做自增 ID 和业务 ID 的翻译
// 选择 UUIDv7 而非随机字符串，是因为 UUIDv7 前半部分是毫秒时间戳，B-tree 写入接近顺序追加
// 避免了纯随机字符串主键在大数据量下的页分裂和缓冲池命中率下降问题
package core

import (
	"encoding/json"
	"time"
)

// 标记一次运行当前推进到的阶段，空值表示调用方尚未设置状态
type RunStatus string

const (
	// 尚未设置状态时使用，通常只会出现在零值结构或反序列化缺省数据中
	RunStatusUnknown RunStatus = ""
	// 运行已经创建，但还没有进入规划和取证流程
	RunStatusCreated RunStatus = "created"
	// 运行正在执行，内部可能处于解析、规划、取证、验证或报告生成阶段
	RunStatusRunning RunStatus = "running"
	// 报告已经生成，调用方可以直接展示或保存
	RunStatusReported RunStatus = "reported"
	// 运行遇到不可恢复错误，报告可能不存在或只包含部分信息
	RunStatusFailed RunStatus = "failed"
)

// 标记某个故障猜想被证据验证后的结果，空值表示调用方尚未完成判断
type VerdictResult string

const (
	// 尚未设置验证结果时使用，避免零值被误认为有效判断
	VerdictUnknown VerdictResult = ""
	// 现有证据支持对应猜想，可以作为报告中的候选原因
	VerdictSupported VerdictResult = "supported"
	// 现有证据否定对应猜想，应在报告中作为排除项说明
	VerdictRefuted VerdictResult = "refuted"
	// 现有证据不足以支持或否定对应猜想，需要继续取证
	VerdictInsufficient VerdictResult = "insufficient"
)

// 承载一次运行从用户问题到最终报告的主体
//
// 该结构只保存运行自身的属性，不嵌装问题、目标、猜想、任务、证据和验证结果
// 子实体通过 RunID 反向关联到运行，编排层和存储层按 RunID 组装和查询
// 这样未来引入迭代层或会话层时，只需要给子实体补充新的父级编号，不必改动运行结构
//
// 运行不限定于故障诊断，编排层根据问题内容自行决定执行流程
// 如果未来需要表达运行意图，应使用不受枚举限制的自由字段，而不是预定义类型
type Run struct {
	// 运行唯一标识，格式为 run_ + UUIDv7，创建时生成，同时作为数据库主键
	ID string `json:"id"`
	// 所属会话编号，当前最小闭环不使用会话，留空即可
	// 引入多轮对话后由入口层填充，运行结构本身不需要改动
	SessionID string `json:"sessionId,omitempty"`

	// 用户输入的原始问题，用于报告展示和后续复盘
	Question string `json:"question"`

	// 当前运行阶段，调用方可以据此判断是否已经生成报告
	Status RunStatus `json:"status"`

	// 运行创建时间，使用调用方统一传入的时间值
	CreatedAt time.Time `json:"createdAt"`
	// 最近一次状态变化时间，便于后续持久化和审计
	UpdatedAt time.Time `json:"updatedAt"`
}

// 描述日志、事件和指标查询使用的时间窗口
// 当前先使用字符串表达，后续接入真实工具时再统一解析和校验
type TimeRange struct {
	// 相对当前时间向前查看的窗口，例如 30m 或 1h
	Since string `json:"since,omitempty"`
	// 绝对开始时间，只有调用方明确传入时才使用
	Start *time.Time `json:"start,omitempty"`
	// 绝对结束时间，nil 表示查询到当前时间
	End *time.Time `json:"end,omitempty"`
}

// 表达一个可被证据验证的候选故障原因
// 猜想只说明待验证方向，不应直接作为最终结论
type Hypothesis struct {
	// 猜想编号，格式为 h_ + UUIDv7，创建时生成，同时作为数据库主键
	// 后续任务和验证结果通过它建立关联
	ID string `json:"id"`
	// 所属运行编号，用于存储层和编排层按运行查询猜想
	RunID string `json:"runId"`

	// 面向人阅读的候选原因描述
	Statement string `json:"statement"`
	// 生成该猜想的简短依据，帮助用户理解排查顺序
	Reason string `json:"reason,omitempty"`
	// 如果猜想成立，通常应该观察到的信号列表
	ExpectedSignals []string `json:"expectedSignals,omitempty"`

	// 猜想创建时间
	CreatedAt time.Time `json:"createdAt"`
}

// 描述定位或诊断阶段需要完成的一次工具调用
// 任务只引用相关数据并声明调用参数，不绑定具体处理阶段
type Task struct {
	// 任务编号，格式为 t_ + UUIDv7，创建时生成，同时作为数据库主键
	// 证据通过它回连到具体取证动作
	ID string `json:"id"`
	// 所属运行编号；正式诊断管道必填，基线 tool 环可空（非诊断观察）
	// 空 RunID 的任务与证据不得进入 Verdict 的证据引用链
	RunID string `json:"runId"`

	// 相关数据的编号，可以引用问题节点、确认目标或故障猜想
	Refs []string `json:"refs"`

	// 白名单工具名称，例如 fake.list_pods 或 k8s.list_pods
	ToolName string `json:"toolName"`
	// 传给工具的结构化参数，执行前必须由工具层校验
	Arguments json.RawMessage `json:"arguments,omitempty"`
	// 本次取证要证明或排除什么，报告中可作为工具调用说明
	Purpose string `json:"purpose,omitempty"`

	// 该任务依赖的其他任务编号，空列表表示可以立即执行
	// 当前最小闭环不使用，保留该字段是为了后续支持先查 Pod 名再查日志这类依赖链
	DependsOn []string `json:"dependsOn,omitempty"`
}

// 保存工具执行后得到的证据摘要和原始数据
// 报告和验证结果只能引用已经记录的证据编号
type Evidence struct {
	// 证据编号，格式为 e_ + UUIDv7，创建时生成，同时作为数据库主键
	// 验证结果和报告通过它引用该证据
	ID string `json:"id"`
	// 所属运行编号，通常拷贝自 Task；基线观察可空
	// 空值表示非诊断账本中的观察，不得作为正式 Verdict 的 evidence_ids
	RunID string `json:"runId"`
	// 产生该证据的取证任务编号
	TaskID string `json:"taskId"`

	// 证据来源，例如 fake、kubernetes、prometheus 或 loki
	Source string `json:"source,omitempty"`
	// 实际执行的工具名称，便于审计和复盘
	ToolName string `json:"toolName"`
	// 给用户看的等价查询表达，不代表系统执行了 shell 命令
	CommandView string `json:"commandView,omitempty"`

	// 对原始结果的短摘要，用于报告和验证阶段快速阅读
	Summary string `json:"summary"`
	// 工具返回的原始结构化数据，保留给后续复盘和更细判断
	Raw json.RawMessage `json:"raw,omitempty"`
	// 工具执行失败时保存错误摘要，空值表示该证据不是错误结果
	Error string `json:"error,omitempty"`

	// 证据创建时间
	CreatedAt time.Time `json:"createdAt"`
}

// 表达某个猜想在当前证据下的判断结果
// 每条判断都必须引用支撑它的证据编号
type Verdict struct {
	// 验证结果编号，格式为 v_ + UUIDv7，创建时生成，同时作为数据库主键
	// 报告通过它引用具体判断
	ID string `json:"id"`
	// 所属运行编号，用于存储层和编排层按运行查询验证结果
	RunID string `json:"runId"`

	// 被验证的故障猜想编号
	HypothesisID string `json:"hypothesisId"`

	// 基于证据得到的支持、否定或证据不足结果
	Result VerdictResult `json:"result"`
	// 面向用户解释该判断为什么成立
	Reason string `json:"reason"`
	// 支撑该判断的证据编号列表
	EvidenceIDs []string `json:"evidenceIds,omitempty"`

	// 验证结果创建时间
	CreatedAt time.Time `json:"createdAt"`
}

// 承载最终面向用户展示的运行结果
// 报告字段只保存结论摘要和引用，不复制原始证据数据
type Report struct {
	// 报告编号，格式为 rep_ + UUIDv7，创建时生成，同时作为数据库主键
	// 唯一标识一次运行的最终输出
	ID string `json:"id"`
	// 所属运行编号，用于存储层按运行查询报告
	RunID string `json:"runId"`

	// 报告标题，通常包含目标对象和故障现象
	Title string `json:"title"`
	// 对本次运行结论的简短摘要
	Summary string `json:"summary"`

	// 本次运行的全部结论，按 Result 区分支持、排除和证据不足
	// 使用统一列表而非分散字段，是为了后续追加置信度、多根因等属性时只扩展结论结构
	Conclusions []Conclusion `json:"conclusions,omitempty"`
	// 面向用户的下一步处理建议，不表示系统已经执行修复
	Suggestions []string `json:"suggestions,omitempty"`

	// 报告创建时间
	CreatedAt time.Time `json:"createdAt"`
}

// 表达报告中的一条结论，由验证结果映射而来
// 一条结论必须引用对应的猜想编号和支撑证据编号
type Conclusion struct {
	// 对应的故障猜想编号
	HypothesisID string `json:"hypothesisId,omitempty"`
	// 该结论的判定结果，与验证结果枚举一致
	Result VerdictResult `json:"result"`
	// 面向用户解释该结论的理由
	Reason string `json:"reason"`
	// 支撑该结论的证据编号列表
	EvidenceIDs []string `json:"evidenceIds,omitempty"`
}
