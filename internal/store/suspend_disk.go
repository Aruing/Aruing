package store

// 挂起快照的磁盘存储：每会话一个 suspended/ 子目录，快照载荷一个 JSON 文件
// （<data>/<sessionId>/suspended/<runId>.json）。载荷对存储层是不透明字节，
// 格式归执行器侧（编排器快照）私有；本层只负责原子写与按会话存取清理。
//
// 单会话约定至多一条挂起：Put 为覆盖写（同会话再挂起同运行编号，文件名稳定）；
// Get 取目录内字典序最大的运行编号（UUIDv7 字典序即创建序，多条并存属
// 理论不可达的崩溃残留，取最新与单条语义一致，旧文件不读不删——被取到时
// 由调用侧账本守卫自愈，空间治理出本层边界）。写入与 runs/ 记录同款
// 临时文件写全 + Sync + rename + 父目录同步，断电时旧文件与完整新文件二选一。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// 会话目录内挂起快照子目录名
const suspensionsDirName = "suspended"

// 挂起快照临时文件的目录内前缀（残留文件按此前缀忽略）
const suspTmpPrefix = ".susp-tmp-"

// 挂起快照的磁盘存储，实现挂起快照存取接口
// 并发安全（镜像账本的单锁语义）；单进程假设，不加文件锁
type DiskSuspensionStore struct {
	// 保护全部文件操作（Get 的读-选与 Put 的写互斥）
	mu sync.Mutex
	// 数据根目录
	root string
}

// 创建磁盘挂起快照存储：建立数据根目录；不预扫（会话至多一条，读时按需列目录）
func NewDiskSuspensionStore(ctx context.Context, root string) (*DiskSuspensionStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root == "" {
		return nil, fmt.Errorf("data dir is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir %s: %w", root, err)
	}
	return &DiskSuspensionStore{root: root}, nil
}

// 写入或覆盖会话的挂起快照载荷：临时文件写全 + Sync + rename + 父目录同步
func (s *DiskSuspensionStore) PutSuspension(ctx context.Context, sessionID, runID string, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sessionID == "" || runID == "" {
		return fmt.Errorf("suspension requires session and run ids")
	}
	if err := checkStorageID(sessionID); err != nil {
		return err
	}
	if err := checkStorageID(runID); err != nil {
		return err
	}
	if len(payload) == 0 {
		return fmt.Errorf("suspension payload is empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(s.root, sessionID, suspensionsDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create suspensions dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, suspTmpPrefix+"*.json")
	if err != nil {
		return fmt.Errorf("create temp suspension: %w", err)
	}
	tmpName := tmp.Name()
	// 失败路径清理临时文件（成功 rename 后已不存在，Remove 报错忽略）
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp suspension: %w", err)
	}
	// 落盘后再替换：断电时旧快照或完整新快照二选一（同 runs/ 记录裁决）
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp suspension: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp suspension: %w", err)
	}
	target := filepath.Join(dir, runID+".json")
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("replace suspension %s: %w", target, err)
	}
	if err := dirSync(dir); err != nil {
		return fmt.Errorf("sync suspensions dir %s: %w", dir, err)
	}
	return nil
}

// 读取会话当前挂起快照：取目录内字典序最大的运行编号（即最新）；无记录时 found 为假
func (s *DiskSuspensionStore) GetSuspension(ctx context.Context, sessionID string) (string, []byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, false, err
	}
	if sessionID == "" {
		return "", nil, false, nil
	}
	if err := checkStorageID(sessionID); err != nil {
		return "", nil, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	runID, err := s.latestLocked(sessionID)
	if err != nil {
		return "", nil, false, err
	}
	if runID == "" {
		return "", nil, false, nil
	}
	payload, err := os.ReadFile(filepath.Join(s.root, sessionID, suspensionsDirName, runID+".json"))
	if err != nil {
		return "", nil, false, fmt.Errorf("read suspension %s: %w", runID, err)
	}
	return runID, payload, true, nil
}

// 清理会话全部挂起快照（恢复完成后调用）；目录或文件不存在不报错
func (s *DiskSuspensionStore) DeleteSuspension(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sessionID == "" {
		return nil
	}
	if err := checkStorageID(sessionID); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(s.root, sessionID, suspensionsDirName)
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("list suspensions dir %s: %w", dir, err)
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") || strings.HasPrefix(f.Name(), suspTmpPrefix) {
			continue
		}
		// 单会话至多一条，全部删除即会话无挂起；单个删除失败即报错（清理不完整
		// 会被下次读回，明确失败优于静默残留）
		if err := os.Remove(filepath.Join(dir, f.Name())); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove suspension %s: %w", f.Name(), err)
		}
	}
	return nil
}

// 列出会话挂起目录内的合法快照文件名，返回字典序最大的运行编号；无则空串
func (s *DiskSuspensionStore) latestLocked(sessionID string) (string, error) {
	dir := filepath.Join(s.root, sessionID, suspensionsDirName)
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("list suspensions dir %s: %w", dir, err)
	}
	latest := ""
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") || strings.HasPrefix(f.Name(), suspTmpPrefix) {
			continue
		}
		id := strings.TrimSuffix(f.Name(), ".json")
		if id > latest {
			latest = id
		}
	}
	return latest, nil
}
