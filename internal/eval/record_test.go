package eval

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/llm"
)

// 成功路径：结论 / 工具调用 / 引用并集 / token / 轮次 / 耗时全部入记录
func TestBuildRunRecordSuccess(t *testing.T) {
	report := &core.Report{
		ID: "rep_1",
		Conclusions: []core.Conclusion{
			{HypothesisID: "h1", Result: core.VerdictSupported, Reason: "demo-api 镜像 tag 不存在", EvidenceIDs: []string{"ev_2", "ev_1"}},
			{HypothesisID: "h2", Result: core.VerdictRefuted, Reason: "无关", EvidenceIDs: []string{"ev_1"}},
		},
	}
	evidence := []core.Evidence{
		{ID: "ev_1", ToolName: "k8s", CommandView: "kubectl get pods -n demo", CreatedAt: time.Unix(1000, 0), Summary: "pods · 3 行"},
		{ID: "ev_2", ToolName: "k8s", CommandView: "kubectl describe pod", CreatedAt: time.Unix(2000, 0), Summary: "非表格"},
	}
	tokens := map[string]llm.UsageTotals{
		"parser": {PromptTokens: 10, CompletionTokens: 5, Calls: 1},
	}

	rec := BuildRunRecord("run_x", "为什么", "test-model", "greedy",
		AcquireRecordInfo{Method: "ours", MaxRounds: 3, Seed: 0, Exit: "supported"},
		true, "", report, evidence, tokens, 2, 1500*time.Millisecond)

	if rec.SchemaVersion != SchemaVersion || rec.RunID != "run_x" || !rec.Completed {
		t.Fatalf("基础字段错误：%+v", rec)
	}
	if rec.ProjectionMethod != "greedy" || rec.Model != "test-model" {
		t.Fatalf("方法/模型字段错误：%+v", rec)
	}
	if len(rec.RootCauses) != 2 || rec.RootCauses[0].Reason != "demo-api 镜像 tag 不存在" {
		t.Fatalf("结论提取错误：%+v", rec.RootCauses)
	}
	if len(rec.ToolCalls) != 2 || rec.ToolCalls[0].Command != "kubectl get pods -n demo" {
		t.Fatalf("工具调用提取错误：%+v", rec.ToolCalls)
	}
	// 引用并集去重且保序
	if len(rec.EvidenceCited) != 2 || rec.EvidenceCited[0] != "ev_2" || rec.EvidenceCited[1] != "ev_1" {
		t.Fatalf("引用并集错误：%v", rec.EvidenceCited)
	}
	if rec.Tokens["parser"].In != 10 || rec.Tokens["parser"].Out != 5 {
		t.Fatalf("token 提取错误：%+v", rec.Tokens)
	}
	if rec.Rounds != 2 || rec.WallTimeMS != 1500 {
		t.Fatalf("轮次/耗时错误：%d %d", rec.Rounds, rec.WallTimeMS)
	}
	if rec.AcquireMethod != "ours" || rec.AcquireMaxRounds != 3 || rec.AcquireExit != "supported" || rec.AcquireSeed != 0 {
		t.Fatalf("取证决策分组/出口字段错误：%s %d %d %q", rec.AcquireMethod, rec.AcquireMaxRounds, rec.AcquireSeed, rec.AcquireExit)
	}
}

// 决策轨迹随 AcquireRecordInfo 入记录（ours 系得分/信念列 + B3 理由列同构携带）
func TestBuildRunRecordDecisionTrace(t *testing.T) {
	acq := AcquireRecordInfo{
		Method: "b3-react", MaxRounds: 5, Exit: "supported",
		Trace: []DecisionTraceEntry{
			{Round: 1, Chosen: "check-pods", Reason: "最便宜的区分点",
				Scores: []ActionScore{{Name: "check-pods", Score: 0.71}, {Name: "ask", Score: 0.1}}},
			{Round: 2, Sufficient: true, Reason: "已收敛"},
		},
	}
	rec := BuildRunRecord("run_x", "q", "m", "fast", acq, true, "", nil, nil, nil, 2, time.Second)
	if len(rec.DecisionTrace) != 2 {
		t.Fatalf("trace = %d, want 2", len(rec.DecisionTrace))
	}
	first := rec.DecisionTrace[0]
	if first.Chosen != "check-pods" || len(first.Scores) != 2 || first.Scores[0].Name != "check-pods" {
		t.Errorf("trace[0] = %+v", first)
	}
	if !rec.DecisionTrace[1].Sufficient || rec.DecisionTrace[1].Reason != "已收敛" {
		t.Errorf("trace[1] = %+v", rec.DecisionTrace[1])
	}
	// JSON 序列化可过（非有限得分已在编排侧封顶；此处验证 schema 面合法）
	if _, err := json.Marshal(rec); err != nil {
		t.Fatalf("marshal record with trace: %v", err)
	}

	// 空轨迹省略字段（旧记录兼容面）：不带 Trace 的记录序列化后无 decision_trace 键
	empty := BuildRunRecord("run_y", "q", "m", "fast", AcquireRecordInfo{Method: "b1-serial"}, true, "", nil, nil, nil, 1, time.Second)
	raw, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "decision_trace") {
		t.Errorf("b1 record should omit decision_trace, got %s", raw)
	}
	// 旧记录（无该字段）反序列化照常读，轨迹为零值
	var legacy RunRecord
	if err := json.Unmarshal([]byte(`{"schema_version":1,"run_id":"r","question":"q","model":"m","projection_method":"fast","acquire_method":"ours","completed":true,"rounds":1}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if legacy.DecisionTrace != nil {
		t.Errorf("legacy trace = %+v, want nil", legacy.DecisionTrace)
	}
}

// 失败路径：report 为 nil 也能落盘（completed=false + 错误信息照实）
func TestBuildRunRecordFailure(t *testing.T) {
	rec := BuildRunRecord("run_x", "q", "m", "fast", AcquireRecordInfo{Method: "b1-serial", Exit: "insufficient", Gap: "预算尽"}, false, "boom", nil, nil, nil, 0, 0)
	if rec.Completed || rec.Error != "boom" {
		t.Fatalf("失败记录字段错误：%+v", rec)
	}
	if rec.RootCauses == nil || rec.ToolCalls == nil || rec.EvidenceCited == nil {
		t.Fatal("切片字段应初始化为空切片而非 nil（JSON 落盘为数组）")
	}
}

// 无引用结论的 evidence_ids 归一为空切片：JSON 必须是 [] 不是 null（schema 数组契约）
func TestBuildRunRecordNilEvidenceIDsNormalize(t *testing.T) {
	report := &core.Report{
		Conclusions: []core.Conclusion{
			{HypothesisID: "h1", Result: core.VerdictSupported, Reason: "无引用结论"},
		},
	}
	rec := BuildRunRecord("run_x", "q", "m", "fast", AcquireRecordInfo{Method: "ours"}, true, "", report, nil, nil, 0, 0)
	if rec.RootCauses[0].EvidenceIDs == nil {
		t.Fatal("无引用结论的 evidence_ids 应归一为空切片")
	}
}

// 旧记录（无 acquire 字段）向后兼容：反序列化照常，新字段零值，judge 不受影响
func TestRunRecordLegacyJSONCompat(t *testing.T) {
	legacy := `{"schema_version":1,"run_id":"run_old","question":"q","model":"m",` +
		`"projection_method":"fast","completed":true,"verdict_root_cause":[],` +
		`"tool_calls":[],"evidence_cited":[],"tokens":{},"rounds":2,"wall_time_ms":100}`
	var rec RunRecord
	if err := json.Unmarshal([]byte(legacy), &rec); err != nil {
		t.Fatalf("旧记录反序列化失败：%v", err)
	}
	if rec.AcquireMethod != "" || rec.AcquireExit != "" || rec.AcquireSeed != 0 {
		t.Fatalf("旧记录 acquire 字段应为零值：%+v", rec)
	}
	res := JudgeRecord(rec, GroundTruth{ResourceName: "demo-web"})
	if res.RunID != "run_old" || res.AcquireMethod != "" || res.Rounds != 2 {
		t.Fatalf("旧记录判分透传错误：%+v", res)
	}
}
