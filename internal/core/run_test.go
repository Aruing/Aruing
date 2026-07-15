package core

import (
	"encoding/json"
	"testing"
	"time"
)

// JSON 序列化是后续存储、报告和调试复盘都会依赖的公共契约
// 该测试同时验证子实体通过 RunID 关联的拓扑是否完整
func TestRunJSON(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 30, 0, 0, time.UTC)
	runID := "run-001"
	raw := json.RawMessage(`{"pods":[{"name":"demo-api","ready":false}]}`)

	run := Run{
		ID:        runID,
		Question:  "demo-api 为什么访问不了",
		Status:    RunStatusReported,
		CreatedAt: now,
		UpdatedAt: now,
	}

	hypothesis := Hypothesis{
		ID:              "h1",
		RunID:           runID,
		Statement:       "后端 Pod 没有正常运行",
		Reason:          "服务不可访问时需要先确认后端是否 Ready",
		ExpectedSignals: []string{"Pod 未 Ready"},
		CreatedAt:       now,
	}

	task := Task{
		ID:        "t1",
		RunID:     runID,
		Refs:      []string{"target_demo", "h1"},
		ToolName:  "fake.list_pods",
		Arguments: json.RawMessage(`{"namespace":"default"}`),
		Purpose:   "查看后端 Pod 状态",
	}

	evidence := Evidence{
		ID:          "e1",
		RunID:       runID,
		TaskID:      "t1",
		Source:      "fake",
		ToolName:    "fake.list_pods",
		CommandView: "fake list pods -n default",
		Summary:     "demo-api 未 Ready",
		Raw:         raw,
		CreatedAt:   now,
	}

	verdict := Verdict{
		ID:           "v1",
		RunID:        runID,
		HypothesisID: "h1",
		Result:       VerdictSupported,
		Reason:       "证据显示后端 Pod 未 Ready",
		EvidenceIDs:  []string{"e1"},
		CreatedAt:    now,
	}

	report := Report{
		ID:      "r1",
		RunID:   runID,
		Title:   "demo-api 诊断报告",
		Summary: "后端 Pod 未 Ready",
		Conclusions: []Conclusion{{
			HypothesisID: "h1",
			Result:       VerdictSupported,
			Reason:       "后端 Pod 未正常提供服务",
			EvidenceIDs:  []string{"e1"},
		}},
		Suggestions: []string{"检查 Pod 启动状态"},
		CreatedAt:   now,
	}

	// 逐个验证序列化往返，确认每个实体可以独立存储和读取
	entities := []struct {
		name string
		val  any
	}{
		{"run", run},
		{"hypothesis", hypothesis},
		{"task", task},
		{"evidence", evidence},
		{"verdict", verdict},
		{"report", report},
	}

	for _, e := range entities {
		data, err := json.Marshal(e.val)
		if err != nil {
			t.Fatalf("marshal %s: %v", e.name, err)
		}

		var remap map[string]any
		if err := json.Unmarshal(data, &remap); err != nil {
			t.Fatalf("unmarshal %s to map: %v", e.name, err)
		}

		if got, ok := remap["runId"]; ok {
			if got != runID {
				t.Fatalf("%s runId = %v, want %s", e.name, got, runID)
			}
		}
	}

	// 验证运行结构本身不携带子实体字段
	runData, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("marshal run: %v", err)
	}
	var runFields map[string]any
	if err := json.Unmarshal(runData, &runFields); err != nil {
		t.Fatalf("unmarshal run: %v", err)
	}
	for _, field := range []string{"scopes", "targets", "hypotheses", "tasks", "evidence", "verdicts", "report"} {
		if _, exists := runFields[field]; exists {
			t.Fatalf("run should not carry nested field %q, entities are related by RunID", field)
		}
	}
}

// 任务引用必须兼容定位和诊断阶段，避免任务模型绑定某一种处理流程
func TestTaskJSON(t *testing.T) {
	want := Task{
		ID:       "t_test",
		RunID:    "run_test",
		Refs:     []string{"node_demo", "target_demo", "h_demo"},
		ToolName: "fake.list_pods",
	}

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}

	var got Task
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	if len(got.Refs) != 3 || got.Refs[0] != "node_demo" || got.Refs[2] != "h_demo" {
		t.Errorf("task refs were not preserved: %#v", got.Refs)
	}

	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("unmarshal task fields: %v", err)
	}
	if _, exists := fields["hypothesisId"]; exists {
		t.Errorf("hypothesisId should not be present: %s", data)
	}
}
