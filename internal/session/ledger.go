package session

import (
	"context"
	"errors"

	"aruing/internal/core"
)

// 按运行编号找不到正式诊断记录时由诊断账本查询返回
var ErrRunNotFound = errors.New("run not found")

// 一次正式诊断在进程内的可追溯记录（报告加证据）
// 权威源不是消息摘要；进程退出即丢，非磁盘持久化
type DiagnosticRecord struct {
	// 正式诊断运行编号
	RunID string
	// 所属会话；升格路径写入
	SessionID string
	// 建运行时的用户问题（或升格时重写的问题）
	Question string
	// 诊断报告
	Report core.Report
	// 执行返回的全部证据（含运行编号的正式链）
	Evidence []core.Evidence
}

// 正式诊断结果的进程内账本；接口挂在使用方，实现在存储包
// 写入成功后可按编号读回；同运行编号重复写入覆盖；不按条数淘汰
type RunLedger interface {
	// 写入或覆盖一条诊断记录；调用方持有的切片后续改动不得影响已存数据
	Put(ctx context.Context, rec DiagnosticRecord) error
	// 按运行编号读回拷贝；不存在时返回未找到错误
	Get(ctx context.Context, runID string) (DiagnosticRecord, error)
	// 返回该会话下全部诊断记录的拷贝（无条数上限）；无记录时返回空切片
	ListBySession(ctx context.Context, sessionID string) ([]DiagnosticRecord, error)
}
