package session

import (
	"context"
	"errors"
)

// 会话不存在时由 GetSession / AppendMessage / ListMessages / UpdateSession 返回
var ErrSessionNotFound = errors.New("session not found")

// 会话与消息的持久化边界；调用方只依赖本接口，不绑定内存或数据库
// 实现放在 internal/store（当前为进程内内存，可换成文件或数据库）
// 同一会话本步约定串行调用 Turn，实现可不做消息级并发写保护，但应避免数据竞争
type Store interface {
	// 写入新建会话；ID 须已由调用方通过 Factory 填好
	CreateSession(ctx context.Context, session *Session) error
	// 按编号读取会话；不存在时返回 ErrSessionNotFound
	GetSession(ctx context.Context, id string) (*Session, error)
	// 覆盖更新已有会话（至少用于刷新 UpdatedAt）；不存在时返回 ErrSessionNotFound
	UpdateSession(ctx context.Context, session *Session) error
	// 按追加顺序写入一条消息；所属会话不存在时返回 ErrSessionNotFound
	AppendMessage(ctx context.Context, message *Message) error
	// 按追加顺序返回会话全部消息；会话不存在时返回 ErrSessionNotFound
	ListMessages(ctx context.Context, sessionID string) ([]Message, error)
}
