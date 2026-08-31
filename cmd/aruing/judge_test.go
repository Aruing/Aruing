package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/eval"
	"github.com/Aruing/Aruing/internal/llm"
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

// 假 llm 客户端：按序回放固定正文（或错误），供 rubric LLM 辅助评测试
type fakeJudgeLLM struct {
	responses []string
	errs      []bool
	calls     int
}

func (f *fakeJudgeLLM) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{}, fmt.Errorf("not implemented")
}

func (f *fakeJudgeLLM) GenerateJSON(_ context.Context, _ llm.Request, out any) error {
	i := f.calls
	f.calls++
	if i < len(f.errs) && f.errs[i] {
		return fmt.Errorf("gateway hiccup")
	}
	if i >= len(f.responses) {
		return fmt.Errorf("no scripted response")
	}
	return json.Unmarshal([]byte(f.responses[i]), out)
}

// scoreRubricRow：合法输出取三值；前两次失败后第三次成功（重试）；全失败记 error 不中断
func TestScoreRubricRow(t *testing.T) {
	row := eval.RubricRow{Reason: "镜像 tag 不存在", Summary: "pod ImagePullBackOff"}

	ok := &fakeJudgeLLM{responses: []string{`{"verdict":" supports "}`}}
	if v := scoreRubricRow(context.Background(), ok, row); v != eval.VerdictSupports {
		t.Fatalf("应取规范化三值，得 %q", v)
	}

	retry := &fakeJudgeLLM{errs: []bool{true, false}, responses: []string{``, `{"verdict":"partial"}`}}
	if v := scoreRubricRow(context.Background(), retry, row); v != eval.VerdictPartial {
		t.Fatalf("重试后应成功，得 %q", v)
	}

	allBad := &fakeJudgeLLM{errs: []bool{true, true, true}}
	if v := scoreRubricRow(context.Background(), allBad, row); v != eval.VerdictError {
		t.Fatalf("重试耗尽应记 error，得 %q", v)
	}
	if allBad.calls != 3 {
		t.Fatalf("应共试 3 次，得 %d", allBad.calls)
	}

	// 非三值输出同样走重试路径
	nonVerdict := &fakeJudgeLLM{responses: []string{`{"verdict":"支持"}`, `{"verdict":"支持"}`, `{"verdict":"支持"}`}}
	if v := scoreRubricRow(context.Background(), nonVerdict, row); v != eval.VerdictError {
		t.Fatalf("非三值应重试耗尽记 error，得 %q", v)
	}
}

// --sample-total：输出机器可回填的 rubric JSON，同种子可复现
func TestRunJudgeSampleTotal(t *testing.T) {
	recPath, scenario := writeJudgeFixture(t)
	var out bytes.Buffer
	err := runJudge([]string{"--run-json", recPath, "--scenario", scenario, "--sample-total", "5", "--seed", "1"}, &out, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("judge sample-total: %v", err)
	}
	var rows []eval.RubricRow
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("输出应为 rubric JSON：%v\n%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].EvidenceID != "ev_1" || rows[0].Verdict != "" {
		t.Fatalf("抽样行不符（verdict 应留空待填）：%+v", rows)
	}

	var again bytes.Buffer
	_ = runJudge([]string{"--run-json", recPath, "--scenario", scenario, "--sample-total", "5", "--seed", "1"}, &again, &bytes.Buffer{})
	var rows2 []eval.RubricRow
	_ = json.Unmarshal(again.Bytes(), &rows2)
	if len(rows2) != len(rows) || rows2[0].EvidenceID != rows[0].EvidenceID {
		t.Fatalf("固定种子应可复现：%+v vs %+v", rows, rows2)
	}
}

// --agree：两组已回填 rubric 的一致率（error 行跳过分母；位置参数传两份文件）
func TestRunJudgeAgree(t *testing.T) {
	recPath, scenario := writeJudgeFixture(t)
	dir := t.TempDir()
	a := filepath.Join(dir, "llm.json")
	b := filepath.Join(dir, "human.json")
	// 2 行对 1 行错 + 1 行 LLM error（跳过）：一致率 1/2
	writeRows := func(p string, rows []eval.RubricRow) {
		raw, _ := json.Marshal(rows)
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	writeRows(a, []eval.RubricRow{{Verdict: eval.VerdictSupports}, {Verdict: eval.VerdictPartial}, {Verdict: eval.VerdictError}})
	writeRows(b, []eval.RubricRow{{Verdict: eval.VerdictSupports}, {Verdict: eval.VerdictNotSupports}, {Verdict: eval.VerdictSupports}})

	var out bytes.Buffer
	err := runJudge([]string{"--run-json", recPath, "--scenario", scenario, "--agree", a, b}, &out, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("judge agree: %v", err)
	}
	var got struct {
		Agree int     `json:"agree"`
		Total int     `json:"total"`
		Rate  float64 `json:"rate"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("输出应为一致率 JSON：%v\n%s", err, out.String())
	}
	if got.Agree != 1 || got.Total != 2 || got.Rate != 0.5 {
		t.Fatalf("一致率应 1/2（error 行跳过）：%+v", got)
	}

	// 缺位置参数明确报错
	if err := runJudge([]string{"--run-json", recPath, "--scenario", scenario, "--agree"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("--agree 缺文件参数应报错")
	}
}

// --rubric-llm 无 LLM 配置：启动期明确报错（不下到半路）
func TestRunJudgeRubricLLMRequiresConfig(t *testing.T) {
	recPath, scenario := writeJudgeFixture(t)
	// 空配置路径且无 env 时不落盘凭据，应报 LLM 未配置
	t.Setenv("ARUING_LLM_BASE_URL", "")
	t.Setenv("ARUING_LLM_API_KEY", "")
	t.Setenv("ARUING_LLM_MODEL", "")
	err := runJudge([]string{"--run-json", recPath, "--scenario", scenario,
		"--sample-total", "1", "--rubric-llm", "--config", filepath.Join(t.TempDir(), "none.yaml")},
		&bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "rubric-llm") {
		t.Fatalf("无 LLM 配置应明确报错，得：%v", err)
	}
}

// --rubric-llm 不带 --sample-total：组合非法启动期明确报错，不静默落到普通判分
func TestRunJudgeRubricLLMRequiresSampleTotal(t *testing.T) {
	recPath, scenario := writeJudgeFixture(t)
	err := runJudge([]string{"--run-json", recPath, "--scenario", scenario, "--rubric-llm"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "sample-total") {
		t.Fatalf("--rubric-llm 缺 --sample-total 应报错，得：%v", err)
	}
}
