package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return path
}

// 场景真值加载：正常解析 + 缺资源名明确报错
func TestLoadGroundTruth(t *testing.T) {
	path := writeTemp(t, "scenario.yaml", `
name: demo
namespace: demo
notes: "人读备注不参与判分"
ground_truth:
  resource_type: deployment
  resource_name: demo-api
  namespace: demo
  fault_type: bad-image-crashloop
`)
	gt, err := LoadGroundTruth(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if gt.ResourceName != "demo-api" || gt.FaultType != "bad-image-crashloop" {
		t.Fatalf("四元组解析错误：%+v", gt)
	}

	bad := writeTemp(t, "bad.yaml", "name: x\n")
	if _, err := LoadGroundTruth(bad); err == nil {
		t.Fatal("缺 resource_name 应报错（不允许静默空判）")
	}
}

// 判分①：supported 结论机械包含匹配；大小写不敏感；refuted 提及不算命中
func TestJudgeRootCause(t *testing.T) {
	gt := GroundTruth{ResourceType: "deployment", ResourceName: "demo-api", Namespace: "demo", FaultType: "x"}

	hit := RunRecord{RunID: "r1", RootCauses: []RootCauseEntry{
		{Result: "refuted", Reason: "demo-api 一切正常"},
		{Result: "supported", Reason: "Demo-Api 的镜像 tag 不存在"},
	}, ToolCalls: []ToolCallEntry{{EvidenceID: "ev_1"}}}
	res := JudgeRecord(hit, gt)
	if !res.RootCauseHit || res.RootCauseHitBy != 1 {
		t.Fatalf("应命中 supported 结论：%+v", res)
	}

	miss := RunRecord{RunID: "r2", RootCauses: []RootCauseEntry{
		{Result: "supported", Reason: "网络插件故障"},
	}}
	if res := JudgeRecord(miss, gt); res.RootCauseHit || res.RootCauseHitBy != -1 {
		t.Fatalf("不应命中：%+v", res)
	}

	// refuted 提及资源名不算根因主张
	refuted := RunRecord{RunID: "r3", RootCauses: []RootCauseEntry{
		{Result: "refuted", Reason: "demo-api 没问题"},
	}}
	if res := JudgeRecord(refuted, gt); res.RootCauseHit {
		t.Fatalf("refuted 提及不应命中：%+v", res)
	}
}

// 判分②：引用编号必须存在于本次证据链
func TestJudgeCitations(t *testing.T) {
	rec := RunRecord{
		RunID: "r",
		RootCauses: []RootCauseEntry{
			{Result: "supported", Reason: "x", EvidenceIDs: []string{"ev_1", "ev_ghost"}},
		},
		ToolCalls:     []ToolCallEntry{{EvidenceID: "ev_1"}, {EvidenceID: "ev_2"}},
		EvidenceCited: []string{"ev_1", "ev_ghost"},
	}
	res := JudgeRecord(rec, GroundTruth{ResourceName: "y"})
	if len(res.CitationViolations) != 1 || res.CitationViolations[0] != "ev_ghost" {
		t.Fatalf("违规列表错误：%v", res.CitationViolations)
	}
}

// 投影命中判定：行级包含，大小写不敏感
func TestProjectionHit(t *testing.T) {
	if !ProjectionHit("  bad-deploy-000123  CrashLoopBackOff", "bad-deploy-000123") {
		t.Fatal("应命中")
	}
	if ProjectionHit("work-0001 Running", "bad-deploy-000123") {
		t.Fatal("不应命中")
	}
	if !ProjectionHit("BAD-DEPLOY-1", "bad-deploy-1") {
		t.Fatal("大小写不应影响命中")
	}
}

// 抽样表：固定种子可复现；n 超过对数时全取；rubric 渲染含表头与行
func TestSampleRubric(t *testing.T) {
	rec := RunRecord{
		RootCauses: []RootCauseEntry{
			{Result: "supported", Reason: "r1", EvidenceIDs: []string{"ev_1"}},
			{Result: "supported", Reason: "r2", EvidenceIDs: []string{"ev_1", "ev_2"}},
		},
		ToolCalls: []ToolCallEntry{
			{EvidenceID: "ev_1", Summary: "s1"},
			{EvidenceID: "ev_2", Summary: "s2"},
		},
	}
	a := SampleRubric(rec, 2, 42)
	b := SampleRubric(rec, 2, 42)
	if len(a) != 2 || len(b) != 2 {
		t.Fatalf("抽样数错误：%d %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("固定种子应可复现：%v vs %v", a[i], b[i])
		}
	}
	if all := SampleRubric(rec, 99, 1); len(all) != 3 {
		t.Fatalf("n 超过对数应全取：%d", len(all))
	}
	if SampleRubric(rec, 0, 1) != nil {
		t.Fatal("n=0 应返回 nil")
	}

	md := RenderRubricMarkdown(a)
	if !strings.Contains(md, "| 结论# |") || !strings.Contains(md, "ev_1") {
		t.Fatalf("rubric 渲染缺内容：\n%s", md)
	}
}
