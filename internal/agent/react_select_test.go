package agent

// B3 ReAct 选择器（0.1.2 步骤 5）：输出解析契约、计划级重试、载荷信息公平面
// 与提示词示例契约；编排级行为（挂起 / 出口 / 轨迹）在 acquire_loop 侧测试

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
)

func reactMenu() []ActionProposal {
	return []ActionProposal{
		{Name: "check-pods", Argv: []string{"get", "pods"}, Purpose: "看 Pod 状态", Cost: 1,
			Outcomes: []string{"crash", "running"}},
		{Name: "ask-change", Ask: "最近变更过吗", Purpose: "区分变更引入", Cost: 10,
			Outcomes: []string{"yes", "no"}},
	}
}

func reactRequest() ReActSelectRequest {
	return ReActSelectRequest{
		Hypotheses: []core.Hypothesis{
			{ID: "h_1", Statement: "Pod 崩溃"},
			{ID: "h_2", Statement: "选择器配错"},
		},
		Evidence: []core.Evidence{
			{ID: "e_1", Summary: "3 行 pods", CommandView: "kubectl get pods"},
		},
		Actions:        reactMenu(),
		Clarifications: []string{"是变更后出现的"},
		BudgetLeft:     3,
	}
}

func reactValidNames() map[string]struct{} {
	valid := make(map[string]struct{}, 2)
	for _, a := range reactMenu() {
		valid[a.Name] = struct{}{}
	}
	return valid
}

// 输出解析契约：action / sufficient 二选一，动作名须在菜单合法域内
func TestParseReActOutput(t *testing.T) {
	valid := reactValidNames()
	cases := []struct {
		name       string
		body       string
		wantChoice ReActChoice
		wantErr    bool
	}{
		{"合法动作", `{"action": "check-pods", "reason": "最便宜"}`,
			ReActChoice{ActionName: "check-pods", Reason: "最便宜"}, false},
		{"声明足够", `{"sufficient": true, "reason": "证据已够"}`,
			ReActChoice{Sufficient: true, Reason: "证据已够"}, false},
		{"动作名裁剪空白", `{"action": "  ask-change  ", "reason": "r"}`,
			ReActChoice{ActionName: "ask-change", Reason: "r"}, false},
		{"两者同给", `{"action": "check-pods", "sufficient": true, "reason": "r"}`,
			ReActChoice{}, true},
		{"两者皆空", `{"reason": "r"}`, ReActChoice{}, true},
		{"编造动作名", `{"action": "reboot-cluster", "reason": "r"}`, ReActChoice{}, true},
		{"非 JSON", `not json`, ReActChoice{}, true},
	}
	for _, tc := range cases {
		got, err := parseReActOutput([]byte(tc.body), valid)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected error", tc.name)
			}
			continue
		}
		if err != nil || got != tc.wantChoice {
			t.Errorf("%s: got %+v err %v, want %+v", tc.name, got, err, tc.wantChoice)
		}
	}
}

// 标准路径：合法输出解析回传；载荷含同构信息面（假设语句 / 证据摘要 / 菜单含
// 成本与结果类别 / 澄清 / 预算），不含判别矩阵与信念后验（信息公平性，裁决 2）
func TestLLMReActSelectorSelectAction(t *testing.T) {
	var payload string
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		for _, m := range req.Messages {
			if m.Role == "user" {
				payload = m.Content
			}
		}
		writeChatCompletion(w, `{"action": "check-pods", "reason": "最便宜的家族级区分点"}`)
	})
	selector, err := NewLLMReActSelector(client)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	choice, err := selector.SelectAction(context.Background(), reactRequest())
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if choice.ActionName != "check-pods" || choice.Reason == "" {
		t.Fatalf("choice = %+v", choice)
	}

	// 信息公平面：同构字段照给，数学层字段不给
	for _, want := range []string{
		`"statement": "Pod 崩溃"`, // 假设语句
		`"summary": "3 行 pods"`, // 证据摘要
		`"name": "check-pods"`,  // 菜单动作名
		`"cost": 1`,             // 成本标注
		`"outcomes"`,            // 结果类别
		`"clarifications"`,      // 澄清累积
		`"budget_left": 3`,      // 剩余预算
	} {
		if !strings.Contains(payload, want) {
			t.Errorf("payload 缺少同构信息 %s", want)
		}
	}
	for _, banned := range []string{"matrix", "belief", "posterior", "prior"} {
		if strings.Contains(payload, banned) {
			t.Errorf("payload 不应含数学层字段 %q（信息公平性）", banned)
		}
	}
}

// 计划级重试：首答编造动作名，次答合法——违规后重新请求，不是失败
func TestLLMReActSelectorRetriesInvalidAction(t *testing.T) {
	var calls atomic.Int32
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeChatCompletion(w, `{"action": "made-up-action", "reason": "r"}`)
			return
		}
		writeChatCompletion(w, `{"action": "ask-change", "reason": "信息只有用户知道"}`)
	})
	selector, err := NewLLMReActSelector(client)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	choice, err := selector.SelectAction(context.Background(), reactRequest())
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if choice.ActionName != "ask-change" || calls.Load() != 2 {
		t.Fatalf("choice = %+v calls = %d, want ask-change after retry", choice, calls.Load())
	}
}

// 重试上限耗尽返回模型输出不一致（与决策规划器同口径）
func TestLLMReActSelectorExhaustsRetries(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, `{"action": "made-up-action", "reason": "r"}`)
	})
	selector, _ := NewLLMReActSelector(client)

	_, err := selector.SelectAction(context.Background(), reactRequest())
	if err == nil || !strings.Contains(err.Error(), "inconsistent") {
		t.Fatalf("err = %v, want output inconsistent", err)
	}
}

// 空菜单是调用方错误（防御：循环内池尽先重规划，不应带空菜单进选择器）
func TestLLMReActSelectorEmptyMenu(t *testing.T) {
	selector, _ := NewLLMReActSelector(newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {}))
	req := reactRequest()
	req.Actions = nil
	if _, err := selector.SelectAction(context.Background(), req); err == nil {
		t.Fatal("expected error for empty menu")
	}
}

// 提示词契约：react-select.md 的两个输出示例必须能被解析器接受
// （改示例不改解析器会在此暴露，与 planner-decision 同模式）
func TestReActPromptContract(t *testing.T) {
	valid := reactValidNames()

	actionBlock := fencedJSONBlock(t, reactSelectPrompt, `"action"`)
	choice, err := parseReActOutput([]byte(actionBlock), valid)
	if err != nil {
		t.Fatalf("prompt action example not parseable: %v", err)
	}
	if choice.ActionName != "check-pods" || choice.Reason == "" {
		t.Fatalf("prompt action example = %+v", choice)
	}

	sufficientBlock := fencedJSONBlock(t, reactSelectPrompt, `"sufficient"`)
	sufficient, err := parseReActOutput([]byte(sufficientBlock), valid)
	if err != nil {
		t.Fatalf("prompt sufficient example not parseable: %v", err)
	}
	if !sufficient.Sufficient {
		t.Fatalf("prompt sufficient example = %+v", sufficient)
	}
}
