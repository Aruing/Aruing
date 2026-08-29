package eval

import (
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

	rec := BuildRunRecord("run_x", "为什么", "test-model", "greedy", true, "", report, evidence, tokens, 2, 1500*time.Millisecond)

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
}

// 失败路径：report 为 nil 也能落盘（completed=false + 错误信息照实）
func TestBuildRunRecordFailure(t *testing.T) {
	rec := BuildRunRecord("run_x", "q", "m", "fast", false, "boom", nil, nil, nil, 0, 0)
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
	rec := BuildRunRecord("run_x", "q", "m", "fast", true, "", report, nil, nil, 0, 0)
	if rec.RootCauses[0].EvidenceIDs == nil {
		t.Fatal("无引用结论的 evidence_ids 应归一为空切片")
	}
}
