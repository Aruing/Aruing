package store

// 正式诊断账本的磁盘存储：每个诊断记录一个 JSON 文件，按会话目录归组
// （runs/<sessionId>/<runId>.json）。写入走同目录临时文件加 rename 原子替换，
// 断电时旧文件与完整新文件二选一，不存在半截形态；残留临时文件加载时忽略。
//
// 打开时只扫描各会话 runs/ 子目录的文件名建 runID → 会话目录索引，不读文件
// 正文（Get 时才读）。按会话列出的顺序取目录名字典序（runID 为 UUIDv7，字典
// 序即创建时间序），与内存实现的首现序语义一致。
//
// run 与 chat 统一为会话模型后，诊断记录的会话编号必填（空值视为调用方错误）。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Aruing/Aruing/internal/session"
)

// 会话目录内诊断记录子目录名
const runsDirName = "runs"

// 诊断记录临时文件的目录内前缀（残留文件按此前缀忽略）
const runTmpPrefix = ".run-tmp-"

// 正式诊断账本的磁盘存储，实现诊断账本接口
// 并发安全（镜像内存实现的单锁语义）；单进程假设，不加文件锁
type DiskRunLedger struct {
	// 保护两张索引映射
	mu sync.Mutex
	// 数据根目录
	root string
	// 运行编号 → 所属会话编号（记录文件所在目录）
	byRun map[string]string
	// 会话编号 → 该会话写入过的运行编号（首现序，覆盖不换位）
	bySession map[string][]string
}

// 创建磁盘诊断账本：建立数据根目录并扫描既有记录文件名建索引
func NewDiskRunLedger(ctx context.Context, root string) (*DiskRunLedger, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root == "" {
		return nil, fmt.Errorf("data dir is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir %s: %w", root, err)
	}
	l := &DiskRunLedger{
		root:      root,
		byRun:     make(map[string]string),
		bySession: make(map[string][]string),
	}
	if err := l.scanLocked(); err != nil {
		return nil, err
	}
	return l, nil
}

// 写入或覆盖一条诊断记录：临时文件写全后 rename 原子替换；
// 同运行编号覆盖不改变会话内首现序（镜像内存实现）
func (l *DiskRunLedger) Put(ctx context.Context, rec session.DiagnosticRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rec.RunID == "" {
		return fmt.Errorf("run id is required")
	}
	if rec.SessionID == "" {
		// run 与 chat 统一为会话模型后必填；空值是调用方接线错误，明确失败
		return fmt.Errorf("run record requires a session id (run and chat both create sessions)")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.writeRecordLocked(rec); err != nil {
		return err
	}
	if _, exists := l.byRun[rec.RunID]; !exists {
		l.bySession[rec.SessionID] = append(l.bySession[rec.SessionID], rec.RunID)
	}
	l.byRun[rec.RunID] = rec.SessionID
	return nil
}

// 按运行编号返回记录拷贝；不存在时返回未找到错误
func (l *DiskRunLedger) Get(ctx context.Context, runID string) (session.DiagnosticRecord, error) {
	if err := ctx.Err(); err != nil {
		return session.DiagnosticRecord{}, err
	}
	if runID == "" {
		return session.DiagnosticRecord{}, session.ErrRunNotFound
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	return l.readLocked(runID)
}

// 按会话返回全部记录拷贝（首现序）；无记录时返回空切片
func (l *DiskRunLedger) ListBySession(ctx context.Context, sessionID string) ([]session.DiagnosticRecord, error) {
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
		rec, err := l.readLocked(id)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// 扫描各会话 runs/ 子目录的记录文件名建索引（不读文件正文）
func (l *DiskRunLedger) scanLocked() error {
	sessionDirs, err := os.ReadDir(l.root)
	if err != nil {
		return fmt.Errorf("scan data dir %s: %w", l.root, err)
	}
	for _, sd := range sessionDirs {
		if !sd.IsDir() {
			continue
		}
		runsDir := filepath.Join(l.root, sd.Name(), runsDirName)
		files, err := os.ReadDir(runsDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("scan runs dir %s: %w", runsDir, err)
		}
		for _, f := range files {
			name := f.Name()
			if f.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, runTmpPrefix) {
				continue
			}
			runID := strings.TrimSuffix(name, ".json")
			if _, dup := l.byRun[runID]; dup {
				return fmt.Errorf("duplicate run record %s across sessions in %s", runID, l.root)
			}
			l.byRun[runID] = sd.Name()
			l.bySession[sd.Name()] = append(l.bySession[sd.Name()], runID)
		}
	}
	return nil
}

// 诊断记录文件路径
func (l *DiskRunLedger) recordPath(sessionID, runID string) string {
	return filepath.Join(l.root, sessionID, runsDirName, runID+".json")
}

// 写记录：序列化到同目录临时文件后 rename 原子替换（调用方持锁）
func (l *DiskRunLedger) writeRecordLocked(rec session.DiagnosticRecord) error {
	dir := filepath.Join(l.root, rec.SessionID, runsDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create runs dir %s: %w", dir, err)
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal run record %s: %w", rec.RunID, err)
	}
	tmp, err := os.CreateTemp(dir, runTmpPrefix+"*.json")
	if err != nil {
		return fmt.Errorf("create temp run record: %w", err)
	}
	tmpName := tmp.Name()
	// 失败路径清理临时文件（成功 rename 后已不存在，Remove 报错忽略）
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp run record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp run record: %w", err)
	}
	target := l.recordPath(rec.SessionID, rec.RunID)
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("replace run record %s: %w", target, err)
	}
	return nil
}

// 按索引定位并读回记录拷贝（调用方持锁）；文件内容归属不匹配视为损坏
func (l *DiskRunLedger) readLocked(runID string) (session.DiagnosticRecord, error) {
	sessionID, ok := l.byRun[runID]
	if !ok {
		return session.DiagnosticRecord{}, session.ErrRunNotFound
	}
	data, err := os.ReadFile(l.recordPath(sessionID, runID))
	if err != nil {
		if os.IsNotExist(err) {
			return session.DiagnosticRecord{}, session.ErrRunNotFound
		}
		return session.DiagnosticRecord{}, fmt.Errorf("read run record %s: %w", runID, err)
	}
	var rec session.DiagnosticRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return session.DiagnosticRecord{}, fmt.Errorf("parse run record %s: %w", l.recordPath(sessionID, runID), err)
	}
	if rec.RunID != runID {
		return session.DiagnosticRecord{}, fmt.Errorf("run record %s: content id %q does not match file name", l.recordPath(sessionID, runID), rec.RunID)
	}
	return cloneDiagnosticRecord(rec), nil
}
