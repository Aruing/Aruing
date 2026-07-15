package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"aruing/internal/core"
)

// 运行命令必须经过完整编排流程并输出可追溯报告，不能继续返回骨架占位文本
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
	if strings.Contains(stdout.String(), "skeleton") {
		t.Fatalf("stdout still contains skeleton output: %q", stdout.String())
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
