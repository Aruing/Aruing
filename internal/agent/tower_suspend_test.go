package agent_test

// 基线塔挂起快照持久化链路：澄清落盘、跨进程恢复（轮首内存优先 + 盘恢复 +
// 账本守卫）、完成清理、再挂起覆盖、坏载荷降级警告一次。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/agent"
	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 挂起快照假执行器：在假执行器上叠加导出/导入能力，载荷为测试私有信封
type fakeSuspendExecutor struct {
	fakeRunExecutor
	// 挂起快照仓（Export 源）：Execute 产生 Suspension 时自动登记
	snaps map[string][]byte
	// 导入恢复的活挂起（sessionID → 信封）
	live map[string]fakeSnapEntry
	// 导入调用记录（观测）
	imported []fakeSnapEntry
}

// 测试私有快照信封：够 FindSuspended/守卫用即可
type fakeSnapEntry struct {
	RunID     string `json:"runID"`
	SessionID string `json:"sessionID"`
	Note      string `json:"note,omitempty"`
}

func (f *fakeSuspendExecutor) Execute(ctx context.Context, run core.Run) (core.Outcome, error) {
	out, err := f.fakeRunExecutor.Execute(ctx, run)
	if err == nil && out.Suspension != nil {
		if f.snaps == nil {
			f.snaps = make(map[string][]byte)
		}
		b, _ := json.Marshal(fakeSnapEntry{RunID: out.Suspension.RunID, SessionID: out.Suspension.SessionID})
		f.snaps[out.Suspension.RunID] = b
	}
	return out, err
}

func (f *fakeSuspendExecutor) ExportSuspended(runID string) ([]byte, error) {
	b, ok := f.snaps[runID]
	if !ok {
		return nil, fmt.Errorf("no suspended run %q", runID)
	}
	return append([]byte(nil), b...), nil
}

func (f *fakeSuspendExecutor) ImportSuspended(payload []byte) error {
	var e fakeSnapEntry
	if err := json.Unmarshal(payload, &e); err != nil {
		return err
	}
	if e.RunID == "" || e.SessionID == "" {
		return errors.New("bad snapshot entry")
	}
	if f.live == nil {
		f.live = make(map[string]fakeSnapEntry)
	}
	f.live[e.SessionID] = e
	f.imported = append(f.imported, e)
	return nil
}

// 内存优先于导入的活挂起（镜像编排语义）
func (f *fakeSuspendExecutor) FindSuspended(sessionID string) string {
	if e, ok := f.live[sessionID]; ok {
		return e.RunID
	}
	return f.fakeRunExecutor.FindSuspended(sessionID)
}

// 组装带挂起快照存储的塔（测试共用）
func newSuspendTower(t *testing.T, exec session.RunExecutor, ledger session.RunLedger, reply string) (*agent.TowerResponder, *store.MemorySuspensionStore, *bytes.Buffer) {
	t.Helper()
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"action":"reply","content":"`+reply+`","question":""}`)
	})
	tower, err := agent.NewTowerResponder(client, newTestFactory(t), exec, ledger, nil, nil, nil)
	if err != nil {
		t.Fatalf("new tower: %v", err)
	}
	susp := store.NewMemorySuspensionStore()
	tower.SetSuspensionStore(susp)
	buf := &bytes.Buffer{}
	tower.SetProgress(buf)
	return tower, susp, buf
}

// 澄清挂起 → 落盘；新进程（新塔 + 新执行器）同会话首条回复即自盘恢复 Resume
func TestTowerSuspendPersistAndRestoreAcrossTowers(t *testing.T) {
	ctx := context.Background()
	ledger := store.NewMemoryRunLedger()

	// 进程一：升格 → 挂起澄清 → 快照落盘
	exec1 := &fakeSuspendExecutor{fakeRunExecutor: fakeRunExecutor{suspension: &core.Suspension{
		Stage:    core.StageInvestigate,
		Question: "故障从何时开始？",
		Options:  []string{"今天", "上周"},
	}}}
	client1 := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"action":"escalate","content":"","question":"为什么慢"}`)
	})
	tower1, err := agent.NewTowerResponder(client1, newTestFactory(t), exec1, ledger, nil, nil, nil)
	if err != nil {
		t.Fatalf("new tower1: %v", err)
	}
	susp := store.NewMemorySuspensionStore()
	tower1.SetSuspensionStore(susp)

	out, err := tower1.Respond(ctx, session.RespondInput{SessionID: "sess_x", UserText: "为什么慢"})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Mode != session.ModeClarify {
		t.Fatalf("want clarify, got %+v", out)
	}
	runID, payload, found, err := susp.GetSuspension(ctx, "sess_x")
	if err != nil || !found {
		t.Fatalf("suspension not persisted: found=%v err=%v", found, err)
	}
	var entry fakeSnapEntry
	err = json.Unmarshal(payload, &entry)
	if err != nil || entry.RunID != runID {
		t.Fatalf("payload = %q err=%v", payload, err)
	}

	// 进程二：内存空，同账本同存储 → 轮首自盘恢复，用户原文作澄清答复
	exec2 := &fakeSuspendExecutor{fakeRunExecutor: fakeRunExecutor{resumeOutcome: &core.Outcome{
		Report: &core.Report{Title: "恢复完成", Summary: "续跑出报告"},
	}}}
	tower2, _, _ := newSuspendTower(t, exec2, ledger, "不应走到基线回复")
	// 跨进程共享同一快照存储（等价于同一数据目录）
	tower2.SetSuspensionStore(susp)
	out2, err := tower2.Respond(ctx, session.RespondInput{SessionID: "sess_x", UserText: "今天早上开始"})
	if err != nil {
		t.Fatalf("respond2: %v", err)
	}
	if out2.Mode != session.ModeDiagnostic || out2.RunID != runID {
		t.Fatalf("restored output: %+v (want diagnostic %s)", out2, runID)
	}
	if exec2.resumeAns != "今天早上开始" {
		t.Fatalf("resume answer: %q", exec2.resumeAns)
	}
	if len(exec2.imported) != 1 {
		t.Fatalf("import calls = %d, want 1", len(exec2.imported))
	}
	// 完成即清理：盘上无挂起
	if _, _, found, _ := susp.GetSuspension(ctx, "sess_x"); found {
		t.Fatal("suspension should be cleared after completed resume")
	}
}

// 内存优先：进程内已有挂起时不读盘（同进程新鲜度最高）
func TestTowerPrefersMemoryOverDisk(t *testing.T) {
	ctx := context.Background()
	ledger := store.NewMemoryRunLedger()
	exec := &fakeSuspendExecutor{fakeRunExecutor: fakeRunExecutor{
		suspendedID:   "run_mem",
		resumeOutcome: &core.Outcome{Report: &core.Report{Title: "内存恢复"}},
	}}
	// 盘上放一个不同的旧挂起：不应被消费
	old, _ := json.Marshal(fakeSnapEntry{RunID: "run_disk", SessionID: "sess_m"})
	tower, susp, _ := newSuspendTower(t, exec, ledger, "不应走到基线回复")
	if err := susp.PutSuspension(ctx, "sess_m", "run_disk", old); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := tower.Respond(ctx, session.RespondInput{SessionID: "sess_m", UserText: "答复"})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if exec.resumeRunID != "run_mem" {
		t.Fatalf("resumed runID = %q, want memory's run_mem", exec.resumeRunID)
	}
	if out.RunID != "run_mem" {
		t.Fatalf("output runID = %q", out.RunID)
	}
}

// 账本守卫：盘上挂起对应的运行已完成（落账后来的崩溃残留）→ 删文件走正常轮
func TestTowerStaleSuspensionLedgerGuard(t *testing.T) {
	ctx := context.Background()
	ledger := store.NewMemoryRunLedger()
	if err := ledger.Put(ctx, session.DiagnosticRecord{
		RunID:     "run_done",
		SessionID: "sess_g",
		Report:    core.Report{ID: "r", Title: "已完成"},
	}); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	exec := &fakeSuspendExecutor{fakeRunExecutor: fakeRunExecutor{resumeOutcome: &core.Outcome{
		Report: &core.Report{Title: "不应恢复"},
	}}}
	tower, susp, _ := newSuspendTower(t, exec, ledger, "正常基线回复")
	stale, _ := json.Marshal(fakeSnapEntry{RunID: "run_done", SessionID: "sess_g"})
	if err := susp.PutSuspension(ctx, "sess_g", "run_done", stale); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out, err := tower.Respond(ctx, session.RespondInput{SessionID: "sess_g", UserText: "新问题"})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if exec.resumeRunID != "" {
		t.Fatalf("stale suspension was resumed: %q", exec.resumeRunID)
	}
	if out.Mode != session.ModeBaseline {
		t.Fatalf("want baseline turn, got %+v", out)
	}
	if _, _, found, _ := susp.GetSuspension(ctx, "sess_g"); found {
		t.Fatal("stale suspension should be cleared by ledger guard")
	}
}

// 坏载荷降级：警告一次（stderr/进度）+ 本轮走正常应答，不报错；下轮不再重复警告
func TestTowerCorruptSuspensionDegrades(t *testing.T) {
	ctx := context.Background()
	ledger := store.NewMemoryRunLedger()
	exec := &fakeSuspendExecutor{}
	tower, susp, progress := newSuspendTower(t, exec, ledger, "降级后的基线回复")
	if err := susp.PutSuspension(ctx, "sess_c", "run_bad", []byte("garbage-not-json")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for i := 0; i < 2; i++ {
		out, err := tower.Respond(ctx, session.RespondInput{SessionID: "sess_c", UserText: "你好"})
		if err != nil {
			t.Fatalf("respond %d: %v", i, err)
		}
		if out.Mode != session.ModeBaseline || !strings.Contains(out.Content, "降级后的基线回复") {
			t.Fatalf("turn %d output: %+v", i, out)
		}
	}
	warns := strings.Count(progress.String(), "aruing: ")
	if warns != 1 {
		t.Fatalf("warnings = %d, want 1 (deduped per session):\n%s", warns, progress.String())
	}
	// 坏文件保留（不删不理解的数据）
	if _, _, found, _ := susp.GetSuspension(ctx, "sess_c"); !found {
		t.Fatal("corrupt payload should be retained for inspection")
	}
}

// 恢复后再挂起：快照覆盖落盘（同会话单文件语义）
func TestTowerReSuspendOverwrites(t *testing.T) {
	ctx := context.Background()
	ledger := store.NewMemoryRunLedger()
	exec := &fakeSuspendExecutor{fakeRunExecutor: fakeRunExecutor{resumeOutcome: &core.Outcome{
		Suspension: &core.Suspension{RunID: "run_r", SessionID: "sess_r", Stage: core.StageInvestigate, Question: "第二次澄清"},
	}}}
	tower, susp, _ := newSuspendTower(t, exec, ledger, "x")

	// 先落一个旧快照（模拟上一轮挂起）
	old, _ := json.Marshal(fakeSnapEntry{RunID: "run_r", SessionID: "sess_r"})
	if err := susp.PutSuspension(ctx, "sess_r", "run_r", old); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 轮首自盘恢复 → Resume 返回再挂起 → settle 覆盖落盘
	// 执行器 Execute 不触发（挂起优先），快照仓为空 → Export 报错走警告？
	// 不：再挂起路径的 Export 源是 Resume 产生的新挂起，测试里由 snaps 提供
	b2, _ := json.Marshal(fakeSnapEntry{RunID: "run_r", SessionID: "sess_r", Note: "second"})
	exec.snaps = map[string][]byte{"run_r": b2}

	out, err := tower.Respond(ctx, session.RespondInput{SessionID: "sess_r", UserText: "答复"})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if out.Mode != session.ModeClarify {
		t.Fatalf("want re-suspend clarify, got %+v", out)
	}
	_, payload, found, err := susp.GetSuspension(ctx, "sess_r")
	if err != nil || !found {
		t.Fatalf("re-suspend not persisted: found=%v err=%v", found, err)
	}
	if !bytes.Contains(payload, []byte("second")) {
		t.Fatalf("payload not overwritten: %q", payload)
	}
}
