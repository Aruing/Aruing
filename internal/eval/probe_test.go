package eval

import (
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
)

// 探针规格测试夹具：两问一断言池；期望组覆盖静态与账本两来源
func newTestProbeSpec() ProbeSpec {
	return ProbeSpec{
		Name:            "scn-test",
		DiagnoseRequest: "请正式诊断 demo namespace 下的 demo-api：排查根因。",
		QAPool:          []string{"看看集群状态", "有哪些工作负载"},
		Probes: []ProbeQuestion{
			{
				ID: "p1", Class: ProbeClassEvidence,
				Question: "第 3 次诊断查出的异常 pod 叫什么名字？",
				Expect:   []ExpectGroup{{FromLedger: &LedgerRule{Rule: LedgerRulePods, K: 3}}},
			},
			{
				ID: "p2", Class: ProbeClassSynthesis,
				Question: "对比第一次和最后一次诊断的结论。",
				Expect: []ExpectGroup{
					{FromLedger: &LedgerRule{Rule: LedgerRulePods, K: 1}},
					{FromLedger: &LedgerRule{Rule: LedgerRulePods, K: -1}},
					{Literal: "demo-api"},
				},
			},
		},
	}
}

// 同规格同参数两次生成全等（种子可复现）；布局约束：诊断间隔 3–5、探针全在尾部
func TestGenerateProbeScript(t *testing.T) {
	spec := newTestProbeSpec()
	a, err := GenerateProbeScript(spec, 20, 1)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	b, err := GenerateProbeScript(spec, 20, 1)
	if err != nil {
		t.Fatalf("generate again: %v", err)
	}
	if len(a.Turns) != len(b.Turns) {
		t.Fatalf("turn count changed between runs: %d vs %d", len(a.Turns), len(b.Turns))
	}
	for i := range a.Turns {
		if a.Turns[i] != b.Turns[i] {
			t.Fatalf("turn %d differs: %+v vs %+v", i, a.Turns[i], b.Turns[i])
		}
	}
	if len(a.Turns) != 20+2 {
		t.Fatalf("want 20 rounds + 2 probes, got %d turns", len(a.Turns))
	}
	// 主体 N 轮内只允许 qa/diagnose；探针轮必须全在尾部且顺序与规格一致
	diagIdx := []int{}
	for i, turn := range a.Turns {
		switch turn.Kind {
		case ProbeTurnDiagnose:
			if i >= 20 {
				t.Fatalf("diagnose turn %d must stay within %d rounds", i, 20)
			}
			if turn.Text != spec.DiagnoseRequest {
				t.Fatalf("diagnose turn %d text mismatch", i)
			}
			diagIdx = append(diagIdx, i)
		case ProbeTurnQA:
			if i >= 20 {
				t.Fatalf("qa turn %d must stay within %d rounds", i, 20)
			}
		case ProbeTurnProbe:
			if i < 20 {
				t.Fatalf("probe turn %d must be in the tail", i)
			}
		default:
			t.Fatalf("unknown turn kind %q", turn.Kind)
		}
	}
	// 诊断间隔 ∈ [3,5]（含首个诊断位置，1 起口径）
	prev := 0
	for _, zeroBased := range diagIdx {
		at := zeroBased + 1
		if prev == 0 && (at < 3 || at > 5) {
			t.Fatalf("first diagnose placement %d violates 3-5 window", at)
		}
		if prev != 0 && (at-prev < 3 || at-prev > 5) {
			t.Fatalf("diagnose placement %d (prev %d) violates 3-5 spacing", at, prev)
		}
		prev = at
	}
	// 探针引用 k=3 必须被脚本满足（20 轮至少 4 次诊断）
	if len(diagIdx) < 3 {
		t.Fatalf("20 rounds must fit >= 3 diagnose turns, got %d", len(diagIdx))
	}
	if a.Turns[20].ProbeID != "p1" || a.Turns[21].ProbeID != "p2" {
		t.Fatalf("probe tail order must follow spec")
	}
}

// 主体轮数撑不起探针引用的诊断序号：生成期报错，不留到运行期
func TestGenerateProbeScriptInsufficientRounds(t *testing.T) {
	spec := newTestProbeSpec()
	if _, err := GenerateProbeScript(spec, 6, 1); err == nil {
		t.Fatal("6 rounds cannot fit run k=3, want error")
	} else if !strings.Contains(err.Error(), "k=3") {
		t.Fatalf("error should name the offending k: %v", err)
	}
}

// 规格校验：缺段、重复编号、非法类别、期望组双来源/零来源、k 取值非法
func TestProbeSpecValidate(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*ProbeSpec)
		want string
	}{
		{"no name", func(s *ProbeSpec) { s.Name = "" }, "name"},
		{"no diagnose request", func(s *ProbeSpec) { s.DiagnoseRequest = "" }, "diagnose_request"},
		{"empty qa pool", func(s *ProbeSpec) { s.QAPool = nil }, "qa_pool"},
		{"no probes", func(s *ProbeSpec) { s.Probes = nil }, "probes"},
		{"dup id", func(s *ProbeSpec) { s.Probes[1].ID = s.Probes[0].ID }, "duplicated"},
		{"bad class", func(s *ProbeSpec) { s.Probes[0].Class = "other" }, "class"},
		{"no expect group", func(s *ProbeSpec) { s.Probes[0].Expect = nil }, "expect"},
		{
			"both sources", func(s *ProbeSpec) {
				s.Probes[0].Expect[0].Literal = "demo-api"
			}, "exactly one",
		},
		{
			"bad rule", func(s *ProbeSpec) { s.Probes[0].Expect[0].FromLedger.Rule = "magic" }, "rule",
		},
		{
			"bad k", func(s *ProbeSpec) { s.Probes[0].Expect[0].FromLedger.K = -2 }, "k",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := newTestProbeSpec()
			tc.mut(&spec)
			err := spec.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// 账本测试夹具：三次诊断，pod 名带随机后缀（kth_run_pods 的展开动机）
func newTestLedgerRecords() []session.DiagnosticRecord {
	mk := func(runID, pod, cmd string) session.DiagnosticRecord {
		return session.DiagnosticRecord{
			RunID:  runID,
			Report: core.Report{RunID: runID, Summary: "根因是 pod " + pod},
			Evidence: []core.Evidence{{
				ID: "e_" + runID, RunID: runID, ToolName: "k8s",
				CommandView: cmd,
				Summary:     "pod " + pod + " CrashLoopBackOff",
			}},
		}
	}
	return []session.DiagnosticRecord{
		mk("run_a", "demo-api-111-aaa", "kubectl get pods -n demo"),
		mk("run_b", "demo-api-222-bbb", "kubectl describe pod demo-api-222-bbb -n demo"),
		mk("run_c", "demo-api-333-ccc", "kubectl get events -n demo"),
	}
}

// from_ledger 展开：k 定位 / -1 末次 / k 超实际诊断数 no_diagnosis / 无匹配事实 no_facts
func TestExpandExpectations(t *testing.T) {
	spec := newTestProbeSpec()
	records := newTestLedgerRecords()

	// p1 = k=3 pods：展开出第 3 次诊断的 pod 名
	groups, status, err := ExpandExpectations(spec.Probes, "p1", records, "demo-api")
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if status != ExpectExpanded || len(groups) != 1 || groups[0][0] != "demo-api-333-ccc" {
		t.Fatalf("p1 expansion wrong: status=%s groups=%v", status, groups)
	}

	// p2 三组：k=1 / -1（末次）/ 静态字面量
	groups, status, err = ExpandExpectations(spec.Probes, "p2", records, "demo-api")
	if err != nil {
		t.Fatalf("expand p2: %v", err)
	}
	if status != ExpectExpanded || len(groups) != 3 {
		t.Fatalf("p2 expansion wrong: status=%s groups=%v", status, groups)
	}
	if groups[0][0] != "demo-api-111-aaa" || groups[1][0] != "demo-api-333-ccc" || groups[2][0] != "demo-api" {
		t.Fatalf("p2 groups wrong: %v", groups)
	}

	// k 超实际诊断数：探针引用的诊断未发生，不进分母
	_, status, err = ExpandExpectations(spec.Probes, "p1", records[:1], "demo-api")
	if err != nil {
		t.Fatalf("expand missing run: %v", err)
	}
	if status != ExpectNoDiagnosis {
		t.Fatalf("want no_diagnosis, got %s", status)
	}

	// 诊断发生了但证据里没有含资源名的候选 token：no_facts
	bare := []session.DiagnosticRecord{
		{RunID: "run_p"}, {RunID: "run_q"},
		{
			RunID:    "run_x",
			Report:   core.Report{RunID: "run_x"},
			Evidence: []core.Evidence{{ID: "e_x", CommandView: "kubectl get pods -n demo", Summary: "NAME READY STATUS"}},
		},
	}
	_, status, err = ExpandExpectations(spec.Probes, "p1", bare, "demo-api")
	if err != nil {
		t.Fatalf("expand bare: %v", err)
	}
	if status != ExpectNoFacts {
		t.Fatalf("want no_facts, got %s", status)
	}

	// 未知探针编号：调用方契约错误
	if _, _, err := ExpandExpectations(spec.Probes, "ghost", records, "demo-api"); err == nil {
		t.Fatal("unknown probe id must error")
	}
}

// 字面量首尾空白在展开侧与规格校验同口径 Trim（pr-agent R1 采纳）：
// Contains 判分下未 Trim 的带空白字面量永不命中（假阴性）
func TestExpandExpectationsTrimsLiteral(t *testing.T) {
	spec := ProbeSpec{Probes: []ProbeQuestion{{
		ID: "p1", Question: "q",
		Expect: []ExpectGroup{{Literal: "  demo-api  "}},
	}}}
	groups, status, err := ExpandExpectations(spec.Probes, "p1", nil, "")
	if err != nil || status != ExpectExpanded {
		t.Fatalf("expand: status=%s err=%v", status, err)
	}
	if len(groups) != 1 || groups[0][0] != "demo-api" {
		t.Fatalf("groups = %v, want [[demo-api]]", groups)
	}
}

// 命令类规则展开：第 k 次诊断全部命令视图各为一候选串
func TestExpandExpectationsCommands(t *testing.T) {
	spec := ProbeSpec{
		Name: "s", DiagnoseRequest: "d", QAPool: []string{"q"},
		Probes: []ProbeQuestion{{
			ID: "c1", Class: ProbeClassChain, Question: "跑了什么命令",
			Expect: []ExpectGroup{{FromLedger: &LedgerRule{Rule: LedgerRuleCommands, K: 2}}},
		}},
	}
	groups, status, err := ExpandExpectations(spec.Probes, "c1", newTestLedgerRecords(), "demo-api")
	if err != nil || status != ExpectExpanded {
		t.Fatalf("expand commands: status=%s err=%v", status, err)
	}
	if len(groups[0]) != 1 || groups[0][0] != "kubectl describe pod demo-api-222-bbb -n demo" {
		t.Fatalf("command candidates wrong: %v", groups)
	}
}

// 探针判分：单组/多组包含、大小写不敏感、部分组命中即 miss、no_* 不进分母、内嵌诊断复判
func TestJudgeProbeSession(t *testing.T) {
	rec := ProbeSessionRecord{
		SessionID: "sess_j", Scenario: "scn-test", MemoryMethod: "ours", Rounds: 20,
		Probes: []ProbeEntry{
			{ProbeID: "hit1", Class: ProbeClassEvidence, Answer: "是 demo-api-333-ccc，它 CrashLoopBackOff",
				Expected: [][]string{{"demo-api-333-ccc"}}, ExpectStatus: ExpectExpanded},
			{ProbeID: "miss1", Class: ProbeClassEvidence, Answer: "没找到相关记录",
				Expected: [][]string{{"demo-api-333-ccc"}}, ExpectStatus: ExpectExpanded},
			// 多组全中才 hit（跨压缩综合口径）
			{ProbeID: "hit2", Class: ProbeClassSynthesis, Answer: "两次都指向 demo-api；第一次 demo-api-111-aaa，最后 demo-api-333-ccc",
				Expected: [][]string{{"demo-api-111-aaa"}, {"demo-api-333-ccc"}, {"demo-api"}}, ExpectStatus: ExpectExpanded},
			// 多组只中部分：miss
			{ProbeID: "miss2", Class: ProbeClassSynthesis, Answer: "第一次 demo-api-111-aaa，其他不记得了",
				Expected: [][]string{{"demo-api-111-aaa"}, {"demo-api-333-ccc"}, {"demo-api"}}, ExpectStatus: ExpectExpanded},
			// 大小写不敏感
			{ProbeID: "hit3", Class: ProbeClassChain, Answer: "跑的是 KUBECTL GET PODS -N DEMO",
				Expected: [][]string{{"kubectl get pods -n demo"}}, ExpectStatus: ExpectExpanded},
			// 引用的诊断未发生：透传状态、不计分母
			{ProbeID: "nod1", Class: ProbeClassEvidence, Answer: "没有第 5 次诊断",
				ExpectStatus: ExpectNoDiagnosis},
			{ProbeID: "nof1", Class: ProbeClassChain, Answer: "不确定",
				ExpectStatus: ExpectNoFacts},
		},
		Diagnoses: []DiagnoseTurnInfo{
			{RunID: "run_a", Status: "completed", Record: RunRecord{
				RunID: "run_a", Completed: true,
				RootCauses: []RootCauseEntry{{Result: "supported", Reason: "demo-api 镜像拉取失败"}},
			}},
			{RunID: "run_x", Status: "suspended", Record: RunRecord{
				RunID: "run_x", Completed: false,
			}},
		},
	}
	res := JudgeProbeSession(rec, GroundTruth{ResourceName: "demo-api"})

	if res.ProbeTotal != 7 || res.ProbeScored != 5 || res.ProbeHits != 3 {
		t.Fatalf("probe counts wrong: total=%d scored=%d hits=%d", res.ProbeTotal, res.ProbeScored, res.ProbeHits)
	}
	if res.SuccessRate != 3.0/5.0 {
		t.Fatalf("success rate wrong: %v", res.SuccessRate)
	}
	if res.NoDiagnosis != 1 || res.NoFacts != 1 {
		t.Fatalf("no_* counts wrong: %+v", res)
	}
	wantStatus := map[string]string{
		"hit1": ProbeStatusHit, "miss1": ProbeStatusMiss, "hit2": ProbeStatusHit,
		"miss2": ProbeStatusMiss, "hit3": ProbeStatusHit, "nod1": ExpectNoDiagnosis, "nof1": ExpectNoFacts,
	}
	for _, v := range res.Probes {
		if v.Status != wantStatus[v.ProbeID] {
			t.Fatalf("probe %s status = %s, want %s", v.ProbeID, v.Status, wantStatus[v.ProbeID])
		}
	}
	// 内嵌诊断复判：完成 1/2、根因命中 1（suspended 那条无结论不命中）
	if res.DiagnoseTotal != 2 || res.DiagnoseCompleted != 1 || res.DiagnoseRootCauseHits != 1 {
		t.Fatalf("diagnose recount wrong: %+v", res)
	}
}
