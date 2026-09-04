package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/eval"
)

// 探针装置夹具：临时场景目录（scenario.yaml + probe.yaml）
func writeProbeScenario(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	scenarioYAML := `name: scn-cli
ground_truth:
  resource_type: deployment
  resource_name: demo-api
  namespace: demo
  fault_type: bad-image-crashloop
`
	probeYAML := `name: scn-cli
diagnose_request: "请正式诊断 demo 的 demo-api"
qa_pool: ["看看集群状态"]
probes:
  - id: p1
    class: evidence
    question: "第 1 次诊断查出的异常 pod 叫什么名字？"
    expect:
      - from_ledger: { rule: kth_run_pods, k: 1 }
`
	if err := os.WriteFile(filepath.Join(dir, "scenario.yaml"), []byte(scenarioYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "probe.yaml"), []byte(probeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "scenario.yaml")
}

// 干跑：零 LLM 零集群，解析规格并打印轮次计划（装置核对入口）
func TestRunProbeDryRun(t *testing.T) {
	scenario := writeProbeScenario(t)
	var out, errOut bytes.Buffer
	if err := runProbe([]string{"--scenario", scenario, "--rounds", "20", "--seed", "1", "--dry-run"}, &out, &errOut); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	plan := out.String()
	if !strings.Contains(plan, "diagnose") || !strings.Contains(plan, "qa") || !strings.Contains(plan, "probe") {
		t.Fatalf("plan should list all turn kinds:\n%s", plan)
	}
	if !strings.Contains(plan, "plan: 20 rounds") {
		t.Fatalf("plan summary missing:\n%s", plan)
	}
	// 缺场景参数启动期拦截
	if err := runProbe([]string{"--dry-run"}, &out, &errOut); err == nil {
		t.Fatal("missing --scenario must error")
	}
}

// judge --probe：会话记录判分输出（探针①层 + 内嵌诊断复判）
func TestRunJudgeProbe(t *testing.T) {
	scenario := writeProbeScenario(t)
	rec := eval.ProbeSessionRecord{
		SchemaVersion: eval.SchemaVersion, SessionID: "sess_cli", Scenario: "scn-cli",
		MemoryMethod: "ours", Rounds: 20,
		Probes: []eval.ProbeEntry{{
			ProbeID: "p1", Class: eval.ProbeClassEvidence,
			Question:     "第 1 次诊断查出的异常 pod 叫什么名字？",
			Answer:       "是 demo-api-111-aaa",
			Expected:     [][]string{{"demo-api-111-aaa"}},
			ExpectStatus: eval.ExpectExpanded,
		}},
		Diagnoses: []eval.DiagnoseTurnInfo{{
			RunID: "run_a", Status: "completed",
			Record: eval.RunRecord{RunID: "run_a", Completed: true,
				RootCauses: []eval.RootCauseEntry{{Result: "supported", Reason: "demo-api 镜像拉取失败"}}},
		}},
	}
	dir := t.TempDir()
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	recPath := filepath.Join(dir, "probe-session.json")
	if err := os.WriteFile(recPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := runJudge([]string{"--probe", "--run-json", recPath, "--scenario", scenario}, &out, &errOut); err != nil {
		t.Fatalf("judge probe: %v", err)
	}
	var results []eval.ProbeJudgeResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("parse judge output: %v\n%s", err, out.String())
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	r := results[0]
	if r.ProbeHits != 1 || r.ProbeScored != 1 || r.SuccessRate != 1 {
		t.Fatalf("probe scoring wrong: %+v", r)
	}
	if r.DiagnoseRootCauseHits != 1 {
		t.Fatalf("embedded diagnose recount wrong: %+v", r)
	}
}

// 记录落盘失败必须让命令非零退出：会话记录是 probe 的唯一主产物，
// 静默丢失会让跑批把丢记录的单元误计为成功（runProbe 对该错误直接 return）
func TestRunProbeRecordWriteFailure(t *testing.T) {
	// --out 指向已存在文件下的子路径：MkdirAll 失败模拟不可写目录
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeProbeRecord(filepath.Join(blocker, "sub", "rec.json"), eval.ProbeSessionRecord{}); err == nil {
		t.Fatal("write into a file path must fail")
	}
}

// 同路径重复落盘明确报错：确定性文件名下静默覆盖会丢旧记录（同参数重跑须换 seed/out）
func TestRunProbeRecordNoOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe-session-x.json")
	if err := writeProbeRecord(path, eval.ProbeSessionRecord{}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeProbeRecord(path, eval.ProbeSessionRecord{}); err == nil {
		t.Fatal("second write to the same path must refuse to overwrite")
	}
}
