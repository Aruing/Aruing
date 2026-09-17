package store

// 超巨输出的磁盘留存：每会话一个 spool/ 子目录（<data>/<sessionId>/spool/），
// 每次 spill 一个文件，内容是源工具 stdout 全量原带（从首字节起）。
// 实现工具包定义的 SpoolStore；文件命名归本层私有（CreateTemp 随机名去前缀
// 转正），引用（SpoolRef.File）即文件名。
//
// 写入模式与 runs/ 记录、挂起快照同款：临时文件写全 + Sync + rename +
// 父目录同步，断电时只有旧文件或完整新文件二选一，不存在可被引用的半文件。
// 文件永久留存（审计原带延伸，#18 留存不淘汰；空间治理出本层边界），
// 随会话目录删除而删除。无跨文件读-选操作，不加锁（文件系统操作自协同）。

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Aruing/Aruing/internal/tools"
)

// 会话目录内 spool 子目录名
const spoolDirName = "spool"

// spool 临时文件前缀（残留文件按此前缀忽略）
const spoolTmpPrefix = ".spool-tmp-"

// spool 文件转正后的名字前缀（与临时前缀区分，加载侧忽略临时残留）
const spoolFinalPrefix = "spool-"

// spool 留存的磁盘存储，实现 tools.SpoolStore
type DiskSpoolStore struct {
	// 数据根目录
	root string
}

// 创建磁盘 spool 留存：建立数据根目录；spool 会话子目录随首次创建写入流建立
func NewDiskSpoolStore(ctx context.Context, root string) (*DiskSpoolStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root == "" {
		return nil, fmt.Errorf("data dir is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir %s: %w", root, err)
	}
	return &DiskSpoolStore{root: root}, nil
}

// 在会话 spool/ 目录创建写入流；临时文件名即最终引用名的来源（去前缀转正，
// 随机源同目录内碰撞概率可忽略）
func (s *DiskSpoolStore) Create(ctx context.Context, sessionID string) (tools.SpoolFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := requireSpoolSession(sessionID); err != nil {
		return nil, err
	}
	dir := filepath.Join(s.root, sessionID, spoolDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create spool dir %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, spoolTmpPrefix+"*")
	if err != nil {
		return nil, fmt.Errorf("create temp spool: %w", err)
	}
	return &diskSpoolFile{f: f, path: f.Name()}, nil
}

// 打开已留存的 spool 原文供流式读；文件不存在或含路径成分时明确报错
func (s *DiskSpoolStore) Open(ctx context.Context, sessionID, ref string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := requireSpoolSession(sessionID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("spool ref is required")
	}
	if err := checkStorageID(ref); err != nil {
		return nil, err
	}
	path := filepath.Join(s.root, sessionID, spoolDirName, ref)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open spool %s: %w", ref, err)
	}
	return f, nil
}

// 会话编号非空且不含路径成分（编号直接充当目录与文件名，同 runs/ 守卫）
func requireSpoolSession(sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("spool requires a session id")
	}
	return checkStorageID(sessionID)
}

// spool 写入流：包装临时文件，Commit 时落盘转正，Abort 时删除半文件。
// path 跟踪当前实际文件名（rename 后更新），失败路径据此自清理，不留孤儿
type diskSpoolFile struct {
	// 临时文件句柄
	f *os.File
	// 当前实际文件名（未转正为临时名，rename 后为终名）
	path string
}

func (w *diskSpoolFile) Write(p []byte) (int, error) {
	return w.f.Write(p)
}

// 收尾：Sync + Close + rename 转正（临时名去临时前缀）+ 父目录同步；
// 返回最终引用名。任一步失败时自清理当前文件（spool 文件名随机不复用，
// 失败残留不会被后续覆盖，必须就地删除，否则反复失败无限积累）
func (w *diskSpoolFile) Commit() (string, error) {
	if err := w.f.Sync(); err != nil {
		w.f.Close()
		w.removeCurrent()
		return "", fmt.Errorf("sync spool: %w", err)
	}
	tmpName := w.f.Name()
	if err := w.f.Close(); err != nil {
		w.removeCurrent()
		return "", fmt.Errorf("close spool: %w", err)
	}
	base := filepath.Base(tmpName)
	ref := spoolFinalPrefix + strings.TrimPrefix(base, spoolTmpPrefix)
	target := filepath.Join(filepath.Dir(tmpName), ref)
	if err := os.Rename(tmpName, target); err != nil {
		w.removeCurrent()
		return "", fmt.Errorf("finalize spool: %w", err)
	}
	w.path = target
	if err := dirSync(filepath.Dir(tmpName)); err != nil {
		// rename 已完成：目录同步失败报错，由上层 Abort 按当前终名清理
		return "", fmt.Errorf("sync spool dir: %w", err)
	}
	return ref, nil
}

// 放弃：关闭并删除当前文件（未提交的临时文件或 dirSync 失败后的终名孤儿）
func (w *diskSpoolFile) Abort() {
	w.f.Close()
	w.removeCurrent()
}

// 删除当前实际文件；不存在时静默（幂等）
func (w *diskSpoolFile) removeCurrent() {
	if w.path != "" {
		_ = os.Remove(w.path)
	}
}
