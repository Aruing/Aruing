package agent

import (
	"strings"
	"testing"
)

// 标准路径：先验进 Confidence，动作形状保持，问用户动作成本固定粗档
func TestParseDecisionOutput(t *testing.T) {
	body := `{
		"hypotheses": [
			{"statement": "服务选择器配错", "reason": "不可达优先查路由", "expected_signals": ["Endpoints 为空"], "prior": 0.6},
			{"statement": "后端 Pod 未就绪", "reason": "次优先", "expected_signals": [], "prior": 0.3}
		],
		"actions": [
			{
				"name": "get-endpoints",
				"argv": ["get", "endpoints", "demo-api", "-n", "demo"],
				"purpose": "看后端是否登记",
				"cost": 1,
				"outcomes": ["empty", "partial", "full"],
				"matrix": [[0.8, 0.1, 0.1], [0.2, 0.6, 0.2]]
			},
			{
				"name": "ask-change-time",
				"ask": "故障是最近变更后出现的吗？",
				"purpose": "区分变更引入",
				"cost": 3,
				"outcomes": ["after-change", "no-change"],
				"matrix": [[0.5, 0.5], [0.3, 0.7]]
			}
		]
	}`

	decision, err := parseDecisionOutput([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(decision.Hypotheses) != 2 {
		t.Fatalf("hypotheses = %d, want 2", len(decision.Hypotheses))
	}
	if decision.Hypotheses[0].Confidence != 0.6 || decision.Hypotheses[1].Confidence != 0.3 {
		t.Errorf("priors = %v / %v, want 0.6 / 0.3",
			decision.Hypotheses[0].Confidence, decision.Hypotheses[1].Confidence)
	}
	if decision.DroppedActions != 0 {
		t.Errorf("dropped = %d, want 0", decision.DroppedActions)
	}

	if len(decision.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(decision.Actions))
	}
	tool := decision.Actions[0]
	if len(tool.Argv) != 5 || tool.Cost != 1 || len(tool.Outcomes) != 3 {
		t.Errorf("tool action shape wrong: %+v", tool)
	}
	if len(tool.Matrix) != 2 || len(tool.Matrix[0]) != 3 {
		t.Errorf("matrix shape = %dx%d, want 2x3", len(tool.Matrix), len(tool.Matrix[0]))
	}
	ask := decision.Actions[1]
	if len(ask.Argv) != 0 || ask.Ask == "" {
		t.Errorf("ask action shape wrong: %+v", ask)
	}
	// 问用户成本固定粗档，模型压价不生效
	if ask.Cost != askCost {
		t.Errorf("ask cost = %v, want %v", ask.Cost, askCost)
	}
}

// 先验越界钳位到 [0,1]，不因模型输出失真拒绝整个计划
func TestParseDecisionOutputPriorClamp(t *testing.T) {
	body := `{
		"hypotheses": [
			{"statement": "a", "prior": 1.7},
			{"statement": "b", "prior": -0.2}
		],
		"actions": [
			{"name": "x", "argv": ["get", "pods"], "cost": 1, "outcomes": ["u", "v"], "matrix": [[0.9, 0.1], [0.1, 0.9]]}
		]
	}`
	decision, err := parseDecisionOutput([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if decision.Hypotheses[0].Confidence != 1 || decision.Hypotheses[1].Confidence != 0 {
		t.Errorf("clamped priors = %v / %v, want 1 / 0",
			decision.Hypotheses[0].Confidence, decision.Hypotheses[1].Confidence)
	}
}

// 动作级容错：非法动作丢弃并计数，剩一个有效动作即成功；全坏才报错
func TestParseDecisionOutputDropsInvalidActions(t *testing.T) {
	body := `{
		"hypotheses": [
			{"statement": "a", "prior": 0.5},
			{"statement": "b", "prior": 0.5}
		],
		"actions": [
			{"name": "good", "argv": ["get", "pods"], "cost": 1, "outcomes": ["u", "v"], "matrix": [[0.9, 0.1], [0.2, 0.8]]},
			{"name": "negative-entry", "argv": ["get", "pods"], "cost": 1, "outcomes": ["u", "v"], "matrix": [[-0.1, 1.1], [0.2, 0.8]]},
			{"name": "row-count", "argv": ["get", "pods"], "cost": 1, "outcomes": ["u", "v"], "matrix": [[0.9, 0.1]]},
			{"name": "col-count", "argv": ["get", "pods"], "cost": 1, "outcomes": ["u", "v"], "matrix": [[0.9], [0.2, 0.8]]},
			{"name": "", "argv": ["get", "pods"], "cost": 1, "outcomes": ["u", "v"], "matrix": [[0.9, 0.1], [0.2, 0.8]]},
			{"name": "both-argv-ask", "argv": ["get", "pods"], "ask": "问用户？", "cost": 1, "outcomes": ["u", "v"], "matrix": [[0.9, 0.1], [0.2, 0.8]]},
			{"name": "neither", "cost": 1, "outcomes": ["u", "v"], "matrix": [[0.9, 0.1], [0.2, 0.8]]},
			{"name": "dup-outcome", "argv": ["get", "pods"], "cost": 1, "outcomes": ["u", "u"], "matrix": [[0.9, 0.1], [0.2, 0.8]]},
			{"name": "good", "argv": ["get", "svc"], "cost": 1, "outcomes": ["u", "v"], "matrix": [[0.9, 0.1], [0.2, 0.8]]}
		]
	}`
	decision, err := parseDecisionOutput([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(decision.Actions) != 1 || decision.Actions[0].Name != "good" {
		t.Fatalf("kept actions = %+v, want only good", decision.Actions)
	}
	// 8 个非法动作：negative-entry / row-count / col-count / empty-name /
	// both-argv-ask / neither / dup-outcome / dup-name（重名丢弃）
	if decision.DroppedActions != 8 {
		t.Errorf("dropped = %d, want 8", decision.DroppedActions)
	}
}

// 值域边界（pr-agent 第 1 轮裁决）：
// 采纳——行和溢出在解析层拒绝，与 acquire.NewAction 的溢出检查同族同判，
// 避免容错分裂在两层（解析层报有效、构造层才拒）；
// 证伪钉板——[0,1] 之外的有限值是相对权重（归一交 NewAction 构造期），
// 全零行是缺损质量设计语义（NewAction 有 sum>0 守卫无除零，EIG=0 已在
// acquire 测试钉板），两者保留不丢
func TestParseDecisionOutputMatrixValueDomain(t *testing.T) {
	body := `{
		"hypotheses": [{"statement": "a"}, {"statement": "b"}],
		"actions": [
			{"name": "overflow", "argv": ["get", "pods"], "cost": 1, "outcomes": ["u", "v"], "matrix": [[1e308, 1e308], [0.5, 0.5]]},
			{"name": "relative-weights", "argv": ["get", "svc"], "cost": 1, "outcomes": ["u", "v"], "matrix": [[5, 3], [1, 1]]},
			{"name": "zero-row", "argv": ["get", "ep"], "cost": 1, "outcomes": ["u", "v"], "matrix": [[0, 0], [0.7, 0.3]]}
		]
	}`
	decision, err := parseDecisionOutput([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if decision.DroppedActions != 1 {
		t.Errorf("dropped = %d, want 1 (overflow only)", decision.DroppedActions)
	}
	kept := map[string]bool{}
	for _, a := range decision.Actions {
		kept[a.Name] = true
	}
	if !kept["relative-weights"] || !kept["zero-row"] {
		t.Errorf("design-semantics actions dropped: kept = %v", kept)
	}
}

// 计划级下限：零假设、空语句、全部动作非法都整计划拒绝
func TestParseDecisionOutputPlanLevelErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"zero hypotheses", `{"hypotheses": [], "actions": [{"name": "x", "argv": ["get"], "outcomes": ["u"], "matrix": [[0.5]]}]}`},
		{"empty statement", `{"hypotheses": [{"statement": "  "}], "actions": [{"name": "x", "argv": ["get"], "outcomes": ["u"], "matrix": [[0.5]]}]}`},
		{"zero actions", `{"hypotheses": [{"statement": "a"}], "actions": []}`},
		{"all actions invalid", `{"hypotheses": [{"statement": "a"}, {"statement": "b"}], "actions": [{"name": "x", "argv": ["get"], "outcomes": ["u", "v"], "matrix": [[0.5, 0.5]]}]}`},
	}
	for _, tc := range cases {
		if _, err := parseDecisionOutput([]byte(tc.body)); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

// 提示词契约：planner-decision.md 的输出示例必须能被解析器接受且零丢弃，
// 防止提示词教学与代码契约漂移（改示例不改解析器会在此暴露）
func TestDecisionPromptContract(t *testing.T) {
	block := fencedJSONBlock(t, plannerDecisionPrompt, `"hypotheses"`)
	decision, err := parseDecisionOutput([]byte(block))
	if err != nil {
		t.Fatalf("prompt example not parseable: %v", err)
	}
	if len(decision.Hypotheses) == 0 || len(decision.Actions) == 0 {
		t.Fatalf("prompt example empty: %d hypotheses, %d actions", len(decision.Hypotheses), len(decision.Actions))
	}
	if decision.DroppedActions != 0 {
		t.Errorf("prompt example dropped %d actions, want 0", decision.DroppedActions)
	}
	// 示例矩阵行数与示例假设数对齐是文档自己教的关键形状
	for _, a := range decision.Actions {
		if len(a.Matrix) != len(decision.Hypotheses) {
			t.Errorf("prompt example action %q matrix rows = %d, hypotheses = %d",
				a.Name, len(a.Matrix), len(decision.Hypotheses))
		}
	}
}

// 从 markdown 提示词中抽取含锚点标记的围栏 JSON 代码块
func fencedJSONBlock(t *testing.T, text, anchor string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	var block []string
	inBlock := false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "```json"):
			inBlock = true
			block = block[:0]
		case inBlock && strings.TrimSpace(line) == "```":
			joined := strings.Join(block, "\n")
			if strings.Contains(joined, anchor) {
				return joined
			}
			inBlock = false
		case inBlock:
			block = append(block, line)
		}
	}
	t.Fatalf("no fenced json block containing %q found", anchor)
	return ""
}
