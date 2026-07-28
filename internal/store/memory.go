// 存储包负责持久化实现：会话消息等业务数据，以及后续诊断运行与证据
//
// 接口定义在使用方（如 session.Store），本包只提供实现
// 当前为进程内内存实现，进程退出即丢失；装配层可换成文件或嵌入式数据库而不改 Turn
// 证据和验证结果必须能通过编号追溯，方便报告引用；Run 级持久化仍可后续在本包扩展
package store

import (
	"context"
	"fmt"
	"sync"

	"aruing/internal/session"
)

// 进程内会话与消息存储，实现 session.Store
// 临时方案：不写文件、不写数据库；并发安全，但同一会话业务上仍约定串行 Turn
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]session.Session
	// 按会话编号保存消息追加顺序
	messages map[string][]session.Message
}

// 创建空的内存会话存储
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions: make(map[string]session.Session),
		messages: make(map[string][]session.Message),
	}
}

// 写入新建会话；编号为空或已存在时返回错误
func (s *MemoryStore) CreateSession(ctx context.Context, sess *session.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sess == nil {
		return fmt.Errorf("session is nil")
	}
	if sess.ID == "" {
		return fmt.Errorf("session id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sess.ID]; ok {
		return fmt.Errorf("session already exists: %s", sess.ID)
	}
	// 存值拷贝，避免调用方后续改动共享 map 内数据
	s.sessions[sess.ID] = *sess
	s.messages[sess.ID] = nil
	return nil
}

// 按编号返回会话拷贝；不存在时返回 session.ErrSessionNotFound
func (s *MemoryStore) GetSession(ctx context.Context, id string) (*session.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[id]
	if !ok {
		return nil, session.ErrSessionNotFound
	}
	out := sess
	return &out, nil
}

// 覆盖更新已有会话；不存在时返回 session.ErrSessionNotFound
func (s *MemoryStore) UpdateSession(ctx context.Context, sess *session.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sess == nil {
		return fmt.Errorf("session is nil")
	}
	if sess.ID == "" {
		return fmt.Errorf("session id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sess.ID]; !ok {
		return session.ErrSessionNotFound
	}
	s.sessions[sess.ID] = *sess
	return nil
}

// 按追加顺序写入消息；所属会话不存在时返回 session.ErrSessionNotFound
func (s *MemoryStore) AppendMessage(ctx context.Context, message *session.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if message == nil {
		return fmt.Errorf("message is nil")
	}
	if message.SessionID == "" {
		return fmt.Errorf("message session id is required")
	}
	if message.ID == "" {
		return fmt.Errorf("message id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[message.SessionID]; !ok {
		return session.ErrSessionNotFound
	}
	s.messages[message.SessionID] = append(s.messages[message.SessionID], *message)
	return nil
}

// 按追加顺序返回消息拷贝；会话不存在时返回 session.ErrSessionNotFound
func (s *MemoryStore) ListMessages(ctx context.Context, sessionID string) ([]session.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return nil, session.ErrSessionNotFound
	}
	src := s.messages[sessionID]
	if len(src) == 0 {
		return nil, nil
	}
	out := make([]session.Message, len(src))
	copy(out, src)
	return out, nil
}
