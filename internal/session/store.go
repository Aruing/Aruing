package session

import (
	"context"
	"errors"
	"time"
)

// 会话不存在时由获取会话、追加消息、列出消息、更新会话等操作返回
var ErrSessionNotFound = errors.New("session not found")

// 会话与消息的持久化边界；调用方只依赖本接口，不绑定内存或数据库
// 实现放在存储包（进程内内存与磁盘两套，装配层按数据目录分派）
// 同一会话本步约定串行调用轮次，实现可不做消息级并发写保护，但应避免数据竞争
type Store interface {
	// 写入新建会话；编号须已由调用方通过编号工厂填好
	CreateSession(ctx context.Context, session *Session) error
	// 按编号读取会话；不存在时返回会话未找到错误
	GetSession(ctx context.Context, id string) (*Session, error)
	// 覆盖更新已有会话（至少用于刷新最近写入时间）；不存在时返回会话未找到错误
	UpdateSession(ctx context.Context, session *Session) error
	// 按追加顺序写入一条消息；所属会话不存在时返回会话未找到错误
	AppendMessage(ctx context.Context, message *Message) error
	// 按追加顺序返回会话全部消息；会话不存在时返回会话未找到错误
	ListMessages(ctx context.Context, sessionID string) ([]Message, error)
}

// 会话列表条目：会话发现命令消费的只读汇总。
// 首问携带全文（无用户消息时空串），展示层截断归列表命令，不在这里做
type SessionSummary struct {
	ID        string
	CreatedAt time.Time
	// 最近活跃时间，与同实现 GetSession 同口径（磁盘由最后消息推导）
	UpdatedAt time.Time
	// 消息总数，与 ListMessages 同口径（含 checkpoint / clarify）
	MessageCount int
	// 首条 user 消息正文全文
	FirstQuestion string
}

// 会话发现能力：列出全部会话的只读汇总，按最近活跃降序（并列按编号降序）。
// 可选能力而非 Store 的一部分，测试假存储不被迫实现；实现方为存储层
type SessionLister interface {
	ListSessions(ctx context.Context) ([]SessionSummary, error)
}
