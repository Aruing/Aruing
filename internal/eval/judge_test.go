package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
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

// 判分①特征词面（试点装置修复）：模型结论常述故障机制不复述资源名，
// 特征词与资源名任一命中即根因命中；空特征词列表退回纯资源名口径
func TestJudgeRootCauseFaultSignature(t *testing.T) {
	gt := GroundTruth{ResourceType: "deployment", ResourceName: "demo-api", Namespace: "demo",
		FaultType: "bad-image-crashloop", FaultSignature: []string{"ImagePullBackOff", "this-tag-does-not-exist"}}

	// 结论只述故障机制不点资源名（试点实测形态）：特征词命中
	mech := RunRecord{RunID: "r1", RootCauses: []RootCauseEntry{
		{Result: "supported", Reason: "容器处于 imagepullbackoff，镜像 nginx:this-tag-does-not-exist-aruing 拉取失败"},
	}}
	if res := JudgeRecord(mech, gt); !res.RootCauseHit {
		t.Fatalf("特征词应命中（大小写归一）：%+v", res)
	}

	// 无关故障迷感词不命中：说了 crashloop 家族外的机制且无资源名
	miss := RunRecord{RunID: "r2", RootCauses: []RootCauseEntry{
		{Result: "supported", Reason: "节点磁盘压力导致驱逐"},
	}}
	if res := JudgeRecord(miss, gt); res.RootCauseHit {
		t.Fatalf("无关机制不应命中：%+v", res)
	}

	// 空特征词列表 = 旧口径（纯资源名）：机制描述不命中
	legacy := gt
	legacy.FaultSignature = nil
	if res := JudgeRecord(mech, legacy); res.RootCauseHit {
		t.Fatalf("空特征词应退回纯资源名口径不命中：%+v", res)
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

// 表格单元净化：中文长摘要按码点截断（仍是合法 UTF-8）；理由含换行/竖杠不打穿表格行
func TestRenderRubricMarkdownSanitize(t *testing.T) {
	rows := []RubricRow{
		{
			ConclusionIdx: 0,
			Reason:        "多行结论\n第二行 | 带竖杠",
			EvidenceID:    "ev_1",
			Summary:       strings.Repeat("这是一段中文摘要", 30), // 210 字符 > 160
		},
	}
	md := RenderRubricMarkdown(rows)

	// 行数恰为表头 2 行 + 数据 1 行：换行折叠后无内嵌 \n 打穿
	if got := strings.Count(md, "\n"); got != 3 {
		t.Fatalf("表格行数错误：\n%s", md)
	}
	// 竖杠被转义为 \|，剔除后给构分隔符恰 6 个（1 开 + 4 内 + 1 闭），不产生额外列
	dataLine := strings.Split(md, "\n")[2]
	if got := strings.Count(strings.ReplaceAll(dataLine, "\\|", ""), "|"); got != 6 {
		t.Fatalf("数据行列数错误（竖杠未转义？）：%s", dataLine)
	}
	// 中文截断按码点：产物仍是合法 UTF-8 且以省略号结尾
	if !strings.HasSuffix(strings.TrimRight(dataLine, "| \t"), "…") {
		t.Fatalf("截断应保留省略号：%s", dataLine)
	}
	if !utf8.ValidString(md) {
		t.Fatal("渲染产物应是合法 UTF-8（截断不得切断多字节字符）")
	}
}

// 全局池化抽样：多记录合并抽 n 行、固定种子可复现；n 超对数全取
func TestSampleRubricTotal(t *testing.T) {
	mk := func(id string) RunRecord {
		return RunRecord{
			RunID: id,
			RootCauses: []RootCauseEntry{
				{Result: "supported", Reason: id + " 的结论", EvidenceIDs: []string{"ev_" + id}},
			},
			ToolCalls: []ToolCallEntry{{EvidenceID: "ev_" + id, Tool: "k8s", Summary: "s-" + id}},
		}
	}
	recs := []RunRecord{mk("a"), mk("b"), mk("c")}

	first := SampleRubricTotal(recs, 2, 7)
	if len(first) != 2 {
		t.Fatalf("应抽 2 行，得 %d", len(first))
	}
	// 同种子可复现（行序与内容一致）
	again := SampleRubricTotal(recs, 2, 7)
	if len(again) != len(first) {
		t.Fatalf("重复抽样行数不一致：%d vs %d", len(again), len(first))
	}
	for i := range first {
		if first[i].EvidenceID != again[i].EvidenceID {
			t.Fatalf("固定种子应可复现：row %d %s vs %s", i, first[i].EvidenceID, again[i].EvidenceID)
		}
	}
	// n 超对数全取（3 记录各 1 对）
	if all := SampleRubricTotal(recs, 10, 7); len(all) != 3 {
		t.Fatalf("n 超对数应全取 3，得 %d", len(all))
	}
}

// 一致率：逐行严格相等；error 行跳过分母；长度不等 / 未回填报错
func TestAgreement(t *testing.T) {
	a := []RubricRow{
		{Verdict: VerdictSupports}, {Verdict: VerdictPartial},
		{Verdict: VerdictNotSupports}, {Verdict: VerdictError},
	}
	b := []RubricRow{
		{Verdict: " supports "}, {Verdict: VerdictNotSupports}, // 前者空白容忍后相等
		{Verdict: VerdictNotSupports}, {Verdict: VerdictSupports}, // 对侧非 error 也跳过该行
	}
	agree, total, err := Agreement(a, b)
	if err != nil {
		t.Fatalf("agreement: %v", err)
	}
	if agree != 2 || total != 3 {
		t.Fatalf("应 2/3（error 行跳过），得 %d/%d", agree, total)
	}

	if _, _, err := Agreement(a, a[:2]); err == nil {
		t.Fatal("长度不等应报错")
	}
	if _, _, err := Agreement([]RubricRow{{Verdict: ""}}, []RubricRow{{Verdict: VerdictSupports}}); err == nil {
		t.Fatal("未回填行应报错")
	}
}

// 三值口径规范化：容忍首尾空白，其余报错
func TestNormalizeVerdict(t *testing.T) {
	for _, ok := range []string{"supports", "partial", "not_supports", "  supports\t"} {
		if _, err := NormalizeVerdict(ok); err != nil {
			t.Fatalf("%q 应合法：%v", ok, err)
		}
	}
	if _, err := NormalizeVerdict("支持"); err == nil {
		t.Fatal("非三值应报错")
	}
}
