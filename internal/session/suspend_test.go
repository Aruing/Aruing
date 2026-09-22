package session_test

// 挂起快照助手：no-op 分支（未配存储 / 执行器无导出能力）与落盘/清理语义；
// 诊断应答器澄清路径的落盘（run 进程退出前的持久化）。

import (
	"context"
	"errors"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 无导出能力的最小执行器（测试 no-op 分支与接线错误）
type plainExecutor struct{}

func (plainExecutor) Execute(_ context.Context, _ core.Run) (core.Outcome, error) {
	return core.Outcome{}, nil
}

// 可导出的假执行器：挂起载荷即运行编号字节
type exportingExecutor struct {
	plainExecutor
	payload []byte
	err     error
}

func (e *exportingExecutor) ExportSuspended(runID string) ([]byte, error) {
	if e.err != nil {
		return nil, e.err
	}
	return append([]byte(nil), e.payload...), nil
}

// 未配存储或执行器未实现导出 → no-op 不报错（内存装配行为不变）
func TestPersistSuspensionNoop(t *testing.T) {
	ctx := context.Background()
	if err := session.PersistSuspension(ctx, plainExecutor{}, nil, "s", "r"); err != nil {
		t.Fatalf("nil store: %v", err)
	}
	mem := store.NewMemorySuspensionStore()
	if err := session.PersistSuspension(ctx, plainExecutor{}, mem, "s", "r"); err != nil {
		t.Fatalf("plain executor: %v", err)
	}
	if _, _, found, _ := mem.GetSuspension(ctx, "s"); found {
		t.Fatal("nothing should be persisted")
	}
	// 清理同 no-op
	if err := session.ClearSuspension(ctx, nil, "s"); err != nil {
		t.Fatalf("clear nil store: %v", err)
	}
}

// 导出成功 → 落盘可读回；导出失败明确报错
func TestPersistSuspensionRoundtrip(t *testing.T) {
	ctx := context.Background()
	mem := store.NewMemorySuspensionStore()
	exec := &exportingExecutor{payload: []byte(`{"v":1}`)}
	if err := session.PersistSuspension(ctx, exec, mem, "sess_p", "run_p"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	runID, payload, found, err := mem.GetSuspension(ctx, "sess_p")
	if err != nil || !found || runID != "run_p" || string(payload) != `{"v":1}` {
		t.Fatalf("get = %q %q found=%v err=%v", runID, payload, found, err)
	}

	fail := &exportingExecutor{err: errors.New("boom")}
	if err := session.PersistSuspension(ctx, fail, mem, "sess_p", "run_x"); err == nil {
		t.Fatal("export error swallowed")
	}
}

// 诊断应答器：升格挂起澄清时落盘快照（run 进程退出前的持久化）；
// 落盘失败明确报错（挂起必丢不如明确失败）
func TestDiagnoseResponderPersistsClarify(t *testing.T) {
	ctx := context.Background()
	factory := core.NewFactory()
	ledger := store.NewMemoryRunLedger()

	suspending := &fakeSuspendingExecutor{}
	mem := store.NewMemorySuspensionStore()
	diag := session.NewDiagnoseResponder(factory, suspending, ledger)
	diag.SetSuspensionStore(mem)

	out, err := diag.Respond(ctx, session.RespondInput{SessionID: "sess_d", UserText: "为什么慢"})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Mode != session.ModeClarify {
		t.Fatalf("want clarify, got %+v", out)
	}
	runID, _, found, err := mem.GetSuspension(ctx, "sess_d")
	if err != nil || !found {
		t.Fatalf("clarify not persisted: found=%v err=%v", found, err)
	}
	if runID != out.RunID {
		t.Fatalf("persisted runID %q != output %q", runID, out.RunID)
	}

	// 落盘失败 → 明确报错
	failing := &fakeSuspendingExecutor{exportErr: errors.New("disk full")}
	diag2 := session.NewDiagnoseResponder(factory, failing, ledger)
	diag2.SetSuspensionStore(mem)
	if _, err := diag2.Respond(ctx, session.RespondInput{SessionID: "sess_d2", UserText: "为什么慢"}); err == nil {
		t.Fatal("persist failure swallowed (run path must fail explicitly)")
	}
}

// 挂起型假执行器：Execute 恒返回澄清挂起，可选注入导出失败
type fakeSuspendingExecutor struct {
	exportErr error
}

func (f *fakeSuspendingExecutor) Execute(_ context.Context, run core.Run) (core.Outcome, error) {
	return core.Outcome{Suspension: &core.Suspension{
		RunID:     run.ID,
		SessionID: run.SessionID,
		Stage:     core.StageResolve,
		Question:  "是哪个命名空间？",
	}}, nil
}

func (f *fakeSuspendingExecutor) ExportSuspended(runID string) ([]byte, error) {
	if f.exportErr != nil {
		return nil, f.exportErr
	}
	return []byte(`{"runID":"` + runID + `"}`), nil
}
