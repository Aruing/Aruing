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

func writeJudgeFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	scenario := filepath.Join(dir, "scenario.yaml")
	if err := os.WriteFile(scenario, []byte(`
name: demo
ground_truth:
  resource_type: deployment
  resource_name: demo-api
  namespace: demo
  fault_type: bad-image-crashloop
`), 0o644); err != nil {
		t.Fatalf("write scenario: %v", err)
	}
	rec := eval.RunRecord{
		SchemaVersion: eval.SchemaVersion,
		RunID:         "run_j",
		RootCauses: []eval.RootCauseEntry{
			{Result: "supported", Reason: "demo-api 镜像 tag 不存在", EvidenceIDs: []string{"ev_1"}},
		},
		ToolCalls:     []eval.ToolCallEntry{{EvidenceID: "ev_1", Tool: "k8s", Command: "kubectl get pods", Summary: "s"}},
		EvidenceCited: []string{"ev_1"},
	}
	raw, _ := json.Marshal(rec)
	recPath := filepath.Join(dir, "run.json")
	if err := os.WriteFile(recPath, raw, 0o644); err != nil {
		t.Fatalf("write record: %v", err)
	}
	return recPath, scenario
}

// judge 打分模式：记录 × 真值 → JSON 结果（命中 + 无违规）
func TestRunJudgeScoring(t *testing.T) {
	recPath, scenario := writeJudgeFixture(t)
	var out bytes.Buffer
	if err := runJudge([]string{"--run-json", recPath, "--scenario", scenario}, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("judge: %v", err)
	}
	var results []eval.JudgeResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("parse output: %v\n%s", err, out.String())
	}
	if len(results) != 1 || !results[0].RootCauseHit || len(results[0].CitationViolations) != 0 {
		t.Fatalf("判分结果错误：%+v", results)
	}
}

// judge 抽样模式：--sample 输出 rubric markdown
func TestRunJudgeSample(t *testing.T) {
	recPath, scenario := writeJudgeFixture(t)
	var out bytes.Buffer
	err := runJudge([]string{"--run-json", recPath, "--scenario", scenario, "--sample", "5", "--seed", "1"}, &out, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("judge sample: %v", err)
	}
	if !strings.Contains(out.String(), "| 结论# |") || !strings.Contains(out.String(), "ev_1") {
		t.Fatalf("rubric 输出缺内容：\n%s", out.String())
	}
}

// 缺参数明确报错；目录输入按 *.json 批量判分
func TestRunJudgeValidationAndDir(t *testing.T) {
	recPath, scenario := writeJudgeFixture(t)
	if err := runJudge([]string{"--run-json", recPath}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("缺 --scenario 应报错")
	}

	dir := filepath.Dir(recPath)
	var out bytes.Buffer
	if err := runJudge([]string{"--run-json", dir, "--scenario", scenario}, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("judge dir: %v", err)
	}
	var results []eval.JudgeResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil || len(results) != 1 {
		t.Fatalf("目录判分结果错误：%v\n%s", err, out.String())
	}
}
