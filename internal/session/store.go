package session

import (
	"context"
	"errors"
)

// 会话不存在时由获取会话、追加消息、列出消息、更新会话等操作返回
var ErrSessionNotFound = errors.New("session not found")

// 会话与消息的持久化边界；调用方只依赖本接口，不绑定内存或数据库
// 实现放在存储包（当前为进程内内存，可换成文件或数据库）
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
