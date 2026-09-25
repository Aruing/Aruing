package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
)

// 超巨输出的盘上原带留存边界：源工具 stdout 超内存保留上限时全量落盘到
// 会话目录 spool/ 下，Evidence.Raw 内联截断版并携带引用（#18 触顶不截肢；
// #19 盘上文件是原带的延伸，内联部分不回写）。
// 接口由使用方（tools / tools/k8s）定义，store 包给磁盘实现；载荷对 tools 层
// 是不透明的字节流，文件命名与布局归实现私有。

// spool 留存存取：创建写入流与按引用读回
type SpoolStore interface {
	// 在 sessionID 会话目录的 spool/ 下创建一个写入流；同一时刻可并存多个
	Create(ctx context.Context, sessionID string) (SpoolFile, error)
	// 打开已留存的 spool 原文供流式读；ref 来自 SpoolRef.File
	Open(ctx context.Context, sessionID, ref string) (io.ReadCloser, error)
}

// spool 写入流：正常收尾提交为可读文件，异常放弃不留残缺引用
type SpoolFile interface {
	io.Writer
	// 收尾：flush + fsync + 原子命名，返回最终引用名（SpoolRef.File）
	Commit() (ref string, err error)
	// 放弃：关闭并删除未提交内容，盘上不留可被引用的半文件
	Abort()
}

// spool 引用：源工具写入自身 Raw 的公共类型（Tower 与 evidence.read 只认
// 此结构，不解析各后端私有 Raw 形态）；TotalBytes/TotalLines 由捕获侧统计
// 供翻页免二次全扫。行数按物理行计（每个换行符结束一行，末尾无换行的
// 残留内容计一行），与盘上切片读取端同一语义
type SpoolRef struct {
	// 留存归属会话（文件所在会话目录）
	SessionID string `json:"sessionId"`
	// spool 文件名（不含路径；实现私有命名）
	File string `json:"file"`
	// stdout 全量字节数
	TotalBytes int64 `json:"totalBytes"`
	// stdout 全量物理行数
	TotalLines int `json:"totalLines"`
}

// ctx 会话归属标注：调用方（Tower 轮内 / 编排 executeTask 链路）向
// Dispatcher 的 ctx 标注会话，Tool 零会话感知；未标注时源工具不开 spill
type spoolScopeKey struct{}

// 把会话编号标注进 ctx，供下游 Tool 创建 spool 时定位会话目录
func WithSpoolScope(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, spoolScopeKey{}, sessionID)
}

// 读取 ctx 内的会话归属；未标注返回空串
func SpoolScopeFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(spoolScopeKey{}).(string)
	return strings.TrimSpace(v)
}

// 从观察 Raw 中探测超巨 stdout 的盘上引用：只解 stdoutSpool 一个字段，
// 源工具未写该字段或 Raw 非对象形态时返回 nil。Tower 轮首回灌与
// evidence.read 单点共用此探测，不解析各后端私有 Raw 形态
func StdoutSpoolRef(raw []byte) *SpoolRef {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var probe struct {
		StdoutSpool *SpoolRef `json:"stdoutSpool"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil
	}
	// 引用完整（会话与文件都非空白）才命中：半引用不进盘读路径，
	// 也不被 Tower 回灌收进索引（探测契约：完整引用才命中）
	if probe.StdoutSpool == nil ||
		strings.TrimSpace(probe.StdoutSpool.SessionID) == "" ||
		strings.TrimSpace(probe.StdoutSpool.File) == "" {
		return nil
	}
	return probe.StdoutSpool
}
