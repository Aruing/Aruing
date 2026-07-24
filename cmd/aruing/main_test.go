package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"aruing/internal/core"
)

// 默认输出应为 Markdown：含标题、结论段、证据编号，而非 JSON
func TestDispatchRun(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := dispatch([]string{"run", "demo-api", "为什么访问不了"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("dispatch run: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "skeleton") {
		t.Fatalf("stdout still contains skeleton output: %q", out)
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("default output should be markdown, got JSON: %q", out)
	}
	// 假报告标题固定含「诊断报告」，证据编号以 e_ 开头应出现在 Markdown 中
	if !strings.Contains(out, "诊断报告") {
		t.Errorf("missing title in markdown:\n%s", out)
	}
	if !strings.Contains(out, "`e_") {
		t.Errorf("missing evidence id in markdown:\n%s", out)
	}
}

// --format json 应输出可解析的结构化报告，证据链完整
func TestDispatchRunJSON(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := dispatch([]string{"run", "--format", "json", "demo-api", "为什么访问不了"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("dispatch run: %v", err)
	}

	var report core.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if !strings.HasPrefix(report.ID, "rep_") || !strings.HasPrefix(report.RunID, "run_") {
		t.Errorf("report identity was not generated: %#v", report)
	}
	if len(report.Conclusions) != 1 || len(report.Conclusions[0].EvidenceIDs) != 1 {
		t.Fatalf("report evidence chain is incomplete: %#v", report.Conclusions)
	}
	if !strings.HasPrefix(report.Conclusions[0].EvidenceIDs[0], "e_") {
		t.Errorf("evidence ID = %q, want e_ prefix", report.Conclusions[0].EvidenceIDs[0])
	}
}

// 非法 format 应报错
func TestDispatchRunBadFormat(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := dispatch([]string{"run", "--format", "xml", "whatever"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("want error for unknown format")
	}
	if !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("unexpected error: %v", err)
	}
}
