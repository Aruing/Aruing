package store

import (
	"errors"
	"strings"
	"testing"
)

// 注入短写故障的假行写器：Seek 返回内部大小，Truncate 记录回滚目标
type fakeLineWriter struct {
	// 当前文件大小（Seek 返回值）
	size int64
	// 大于 0 时 Write 只报告该字节数（短写）；否则报全部写入
	shortN int
	// Write 返回的错误（可与部分写入并存）
	writeErr error
	// Truncate 返回的错误（回滚失败形态）
	truncErr error
	// 每次 Truncate 收到的目标大小
	truncated []int64
}

func (f *fakeLineWriter) Write(p []byte) (int, error) {
	if f.shortN > 0 {
		f.size += int64(f.shortN)
		return f.shortN, f.writeErr
	}
	f.size += int64(len(p))
	return len(p), f.writeErr
}

func (f *fakeLineWriter) Seek(int64, int) (int64, error) {
	return f.size, nil
}

func (f *fakeLineWriter) Truncate(size int64) error {
	if f.truncErr != nil {
		return f.truncErr
	}
	f.truncated = append(f.truncated, size)
	f.size = size
	return nil
}

// 写失败必须截断回写前末尾：半行被后续追加顶成中间行会让整个会话拒载
func TestWriteJSONLineRollback(t *testing.T) {
	marshalErr := errors.New

	t.Run("write error with partial bytes", func(t *testing.T) {
		f := &fakeLineWriter{size: 5, shortN: 3, writeErr: marshalErr("disk full")}
		err := writeJSONLine(f, map[string]string{"k": "v"})
		if err == nil || !strings.Contains(err.Error(), "disk full") {
			t.Fatalf("want write error, got %v", err)
		}
		if len(f.truncated) != 1 || f.truncated[0] != 5 {
			t.Fatalf("want truncate back to 5, got %v", f.truncated)
		}
	})

	t.Run("short write without error", func(t *testing.T) {
		f := &fakeLineWriter{size: 5, shortN: 2}
		err := writeJSONLine(f, map[string]string{"k": "v"})
		if err == nil || !strings.Contains(err.Error(), "short write") {
			t.Fatalf("want short write error, got %v", err)
		}
		if len(f.truncated) != 1 || f.truncated[0] != 5 {
			t.Fatalf("want truncate back to 5, got %v", f.truncated)
		}
	})

	t.Run("rollback failure surfaces both errors", func(t *testing.T) {
		f := &fakeLineWriter{size: 5, shortN: 3, writeErr: errors.New("io err"), truncErr: errors.New("truncate err")}
		err := writeJSONLine(f, map[string]string{"k": "v"})
		if err == nil || !strings.Contains(err.Error(), "io err") || !strings.Contains(err.Error(), "truncate err") {
			t.Fatalf("want both errors surfaced, got %v", err)
		}
	})

	t.Run("happy path no truncate", func(t *testing.T) {
		f := &fakeLineWriter{size: 5}
		if err := writeJSONLine(f, map[string]string{"k": "v"}); err != nil {
			t.Fatalf("write: %v", err)
		}
		if len(f.truncated) != 0 {
			t.Fatalf("happy path must not truncate, got %v", f.truncated)
		}
	})
}
