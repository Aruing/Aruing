package session

// 挂起快照的持久化边界与组合助手：挂起 Run 落盘、跨进程恢复导入、完成后清理。
//
// 快照载荷是执行器侧（编排器）私有的序列化格式，会话与存储层只搬运字节
// 不解释内容；接线形态为可选能力接口（照 SuspendedRunner 模式）：内存装配
// 不配存储时一切 no-op，行为与进程内形态完全一致。
//
// 恢复链路（读盘 → 账本守卫 → 导入 → Resume）内联在基线塔轮首，见 agent 包；
// 本文件只放跨应答器共用的落盘与清理助手。

import (
	"context"
	"fmt"
)

// 挂起快照的存取边界；实现放在存储包（磁盘与内存两套），装配层按数据目录分派
// 单会话约定至多一条挂起记录；Put 为覆盖写，Delete 清理会话全部挂起
type SuspensionStore interface {
	// 写入或覆盖会话的挂起快照载荷；载荷格式归执行器侧私有
	PutSuspension(ctx context.Context, sessionID, runID string, payload []byte) error
	// 读取会话当前挂起快照；无记录时 found 为假；读失败明确报错
	GetSuspension(ctx context.Context, sessionID string) (runID string, payload []byte, found bool, err error)
	// 清理会话的挂起快照；无记录不报错
	DeleteSuspension(ctx context.Context, sessionID string) error
}

// 可选能力：执行器可导出挂起快照载荷（编排器实现）
type SuspendExporter interface {
	// 序列化指定挂起运行的快照；runID 不存在时明确报错
	ExportSuspended(runID string) ([]byte, error)
}

// 可选能力：执行器可导入挂起快照回进程内挂起索引（编排器实现）
type SuspendImporter interface {
	// 校验并归位快照载荷；坏载荷明确报错，降级策略归调用方
	ImportSuspended(payload []byte) error
}

// 挂起或再挂起后落盘快照：执行器未实现导出能力或未配存储时 no-op
// （内存装配行为不变）；导出或写盘失败明确报错，由调用方决定失败口径
func PersistSuspension(
	ctx context.Context,
	executor RunExecutor,
	store SuspensionStore,
	sessionID, runID string,
) error {
	if store == nil {
		return nil
	}
	exporter, ok := executor.(SuspendExporter)
	if !ok {
		return nil
	}
	payload, err := exporter.ExportSuspended(runID)
	if err != nil {
		return fmt.Errorf("persist suspension: %w", err)
	}
	if err := store.PutSuspension(ctx, sessionID, runID, payload); err != nil {
		return fmt.Errorf("persist suspension: %w", err)
	}
	return nil
}

// 恢复完成后清理会话的挂起快照；未配存储时 no-op；无记录不报错
func ClearSuspension(ctx context.Context, store SuspensionStore, sessionID string) error {
	if store == nil {
		return nil
	}
	return store.DeleteSuspension(ctx, sessionID)
}
