// 场景列表子命令的测试：表格占位与 json 契约
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 落一个场景目录：scored=true 时带完整难度标注，否则只有真值
func writeScenarioFixture(t *testing.T, root, name string, scored bool) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "manifests"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "name: " + name + `
ground_truth:
  resource_name: ` + name + `-api
`
	if scored {
		content += `difficulty:
  faults: 2
  evidence_sources: 3
  decoys: 1
  output_scale: large
  layers: 2
  session_rounds: 4
  info_gap: true
`
	}
	if err := os.WriteFile(filepath.Join(dir, "scenario.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write scenario: %v", err)
	}
}

// 表格视图：已标注场景出行分数，未标注场景显示占位而不是报错
func TestRunScenariosTable(t *testing.T) {
	root := t.TempDir()
	writeScenarioFixture(t, root, "scored", true)
	writeScenarioFixture(t, root, "unscored", false)

	var stdout, stderr bytes.Buffer
	if err := runScenarios([]string{"--dir", root}, &stdout, &stderr); err != nil {
		t.Fatalf("runScenarios: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "SCENARIO") || !strings.Contains(out, "TOTAL") {
		t.Fatalf("header missing: %q", out)
	}
	// 2+3+1+2+2+3 = 13 分，信息缺口单独列
	if !strings.Contains(out, "scored") || !strings.Contains(out, "13") || !strings.Contains(out, "yes") {
		t.Fatalf("scored row missing: %q", out)
	}
	unscoredLine := lineWith(out, "unscored")
	if unscoredLine == "" || !strings.Contains(unscoredLine, "-") {
		t.Fatalf("unscored row want placeholder: %q", out)
	}
	if strings.Contains(unscoredLine, "yes") || strings.Contains(unscoredLine, "no") {
		t.Fatalf("unscored row must not claim gap value: %q", unscoredLine)
	}
}

// json 视图：难度不可用时 score 为 null，可用时给出原子事实与派生分
func TestRunScenariosJSON(t *testing.T) {
	root := t.TempDir()
	writeScenarioFixture(t, root, "scored", true)
	writeScenarioFixture(t, root, "unscored", false)

	var stdout, stderr bytes.Buffer
	if err := runScenarios([]string{"--dir", root, "--format", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("runScenarios: %v", err)
	}

	var rows []struct {
		Name       string `json:"name"`
		Difficulty *struct {
			Faults int `json:"faults"`
		} `json:"difficulty"`
		Score *struct {
			Total int `json:"total"`
		} `json:"score"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v (%q)", err, stdout.String())
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}

	byName := map[string]int{}
	for i, r := range rows {
		byName[r.Name] = i
	}
	scored := rows[byName["scored"]]
	if scored.Score == nil || scored.Score.Total != 13 {
		t.Fatalf("scored total = %+v, want 13", scored.Score)
	}
	unscored := rows[byName["unscored"]]
	if unscored.Score != nil || unscored.Difficulty != nil {
		t.Fatalf("unscored = %+v, want null difficulty and score", unscored)
	}
	if unscored.Error != "" {
		t.Fatalf("unscored error = %q, want empty (missing difficulty is not an error)", unscored.Error)
	}
}

// 位置参数与非法格式都走明确报错
func TestRunScenariosRejects(t *testing.T) {
	for _, args := range [][]string{
		{"--dir", t.TempDir(), "extra"},
		{"--dir", t.TempDir(), "--format", "yaml"},
	} {
		var stdout, stderr bytes.Buffer
		if err := runScenarios(args, &stdout, &stderr); err == nil {
			t.Fatalf("args %v: want error", args)
		}
	}
}

// 取包含指定子串的第一行输出
func lineWith(out, sub string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	return ""
}
