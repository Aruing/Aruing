package store

// 挂起快照的进程内存储：镜像磁盘实现的接口语义（单会话至多一条，Put 覆盖，
// Delete 清理全部），供测试与编程式装配注入。

import (
	"context"

	"github.com/Aruing/Aruing/internal/session"
)

// 挂起快照的进程内存储，实现挂起快照存取接口
type MemorySuspensionStore struct {
	// 会话编号 → 当前挂起（运行编号 + 载荷）
	bySession map[string]memorySuspension
}

// 单条挂起的内存形态
type memorySuspension struct {
	runID   string
	payload []byte
}

// 创建进程内挂起快照存储
func NewMemorySuspensionStore() *MemorySuspensionStore {
	return &MemorySuspensionStore{bySession: make(map[string]memorySuspension)}
}

// 写入或覆盖会话的挂起快照载荷
func (m *MemorySuspensionStore) PutSuspension(_ context.Context, sessionID, runID string, payload []byte) error {
	m.bySession[sessionID] = memorySuspension{runID: runID, payload: append([]byte(nil), payload...)}
	return nil
}

// 读取会话当前挂起快照；无记录时 found 为假
func (m *MemorySuspensionStore) GetSuspension(_ context.Context, sessionID string) (string, []byte, bool, error) {
	s, ok := m.bySession[sessionID]
	if !ok {
		return "", nil, false, nil
	}
	return s.runID, append([]byte(nil), s.payload...), true, nil
}

// 清理会话全部挂起快照；无记录不报错
func (m *MemorySuspensionStore) DeleteSuspension(_ context.Context, sessionID string) error {
	delete(m.bySession, sessionID)
	return nil
}

var _ session.SuspensionStore = (*MemorySuspensionStore)(nil)
