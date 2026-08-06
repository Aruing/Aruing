package session

import (
	"context"
	"errors"

	"aruing/internal/core"
)

// 按 RunID 找不到正式诊断记录时由 RunLedger.Get 返回
var ErrRunNotFound = errors.New("run not found")

// 一次正式诊断在进程内的可追溯记录（Report + Evidence）
// 权威源不是 Message 摘要；进程退出即丢，非磁盘持久化
type DiagnosticRecord struct {
	// 正式诊断 Run 编号
	RunID string
	// 所属会话；升格路径写入
	SessionID string
	// 建 Run 时的用户问题（或 escalate 重写的 question）
	Question string
	// 诊断报告
	Report core.Report
	// Execute 返回的全部证据（含 RunID 的正式链）
	Evidence []core.Evidence
}

// 正式诊断结果的进程内账本；接口挂在使用方，实现在 internal/store
// Put 成功后 Get 可读回；同 RunID 重复 Put 覆盖；不按条数淘汰（#18）
type RunLedger interface {
	// 写入或覆盖一条诊断记录；调用方持有的切片后续改动不得影响已存数据
	Put(ctx context.Context, rec DiagnosticRecord) error
	// 按 RunID 读回拷贝；不存在时返回 ErrRunNotFound
	Get(ctx context.Context, runID string) (DiagnosticRecord, error)
	// 返回该会话下全部诊断记录的拷贝（无条数 cap）；无记录时返回空切片
	ListBySession(ctx context.Context, sessionID string) ([]DiagnosticRecord, error)
}
