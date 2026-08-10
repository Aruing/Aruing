package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"aruing/internal/core"
	"aruing/internal/session"
)

// 进程内正式诊断账本，实现会话诊断账本接口
// 不写磁盘；并发安全；同运行编号覆盖；进程退出即丢
type MemoryRunLedger struct {
	// 保护按运行与按会话索引的互斥锁
	mu sync.Mutex
	// 运行编号 → 记录（值拷贝）
	byRun map[string]session.DiagnosticRecord
	// 会话编号 → 该会话写入过的运行编号顺序（覆盖时不重复追加）
	bySession map[string][]string
}

// 创建空的内存诊断账本
func NewMemoryRunLedger() *MemoryRunLedger {
	return &MemoryRunLedger{
		byRun:     make(map[string]session.DiagnosticRecord),
		bySession: make(map[string][]string),
	}
}

// 写入或覆盖；运行编号为空时返回错误
func (l *MemoryRunLedger) Put(ctx context.Context, rec session.DiagnosticRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rec.RunID == "" {
		return fmt.Errorf("run id is required")
	}

	stored := cloneDiagnosticRecord(rec)

	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.byRun[rec.RunID]; !exists && rec.SessionID != "" {
		l.bySession[rec.SessionID] = append(l.bySession[rec.SessionID], rec.RunID)
	}
	l.byRun[rec.RunID] = stored
	return nil
}

// 按运行编号返回拷贝；不存在时返回未找到错误
func (l *MemoryRunLedger) Get(ctx context.Context, runID string) (session.DiagnosticRecord, error) {
	if err := ctx.Err(); err != nil {
		return session.DiagnosticRecord{}, err
	}
	if runID == "" {
		return session.DiagnosticRecord{}, session.ErrRunNotFound
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	rec, ok := l.byRun[runID]
	if !ok {
		return session.DiagnosticRecord{}, session.ErrRunNotFound
	}
	return cloneDiagnosticRecord(rec), nil
}

// 按会话返回记录拷贝；无记录时返回空切片（不是空指针错误）
func (l *MemoryRunLedger) ListBySession(ctx context.Context, sessionID string) ([]session.DiagnosticRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	ids := l.bySession[sessionID]
	if len(ids) == 0 {
		return nil, nil
	}
	out := make([]session.DiagnosticRecord, 0, len(ids))
	for _, id := range ids {
		rec, ok := l.byRun[id]
		if !ok {
			continue
		}
		out = append(out, cloneDiagnosticRecord(rec))
	}
	return out, nil
}

func cloneDiagnosticRecord(rec session.DiagnosticRecord) session.DiagnosticRecord {
	out := rec
	out.Report = cloneReport(rec.Report)
	out.Evidence = cloneEvidenceSlice(rec.Evidence)
	return out
}

func cloneReport(r core.Report) core.Report {
	out := r
	if len(r.Conclusions) > 0 {
		out.Conclusions = make([]core.Conclusion, len(r.Conclusions))
		for i, c := range r.Conclusions {
			out.Conclusions[i] = c
			if len(c.EvidenceIDs) > 0 {
				out.Conclusions[i].EvidenceIDs = append([]string(nil), c.EvidenceIDs...)
			}
		}
	}
	if len(r.Suggestions) > 0 {
		out.Suggestions = append([]string(nil), r.Suggestions...)
	}
	return out
}

func cloneEvidenceSlice(in []core.Evidence) []core.Evidence {
	if len(in) == 0 {
		return nil
	}
	out := make([]core.Evidence, len(in))
	for i, e := range in {
		out[i] = e
		if len(e.Raw) > 0 {
			out[i].Raw = append(json.RawMessage(nil), e.Raw...)
		}
	}
	return out
}

var _ session.RunLedger = (*MemoryRunLedger)(nil)
