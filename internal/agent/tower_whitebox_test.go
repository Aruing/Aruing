package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"aruing/internal/tools"
	"aruing/internal/tools/toolstest"
)

// fetchBaselineClusterResources 在无 dispatcher 时返回 nil
func TestFetchBaselineClusterResourcesNoDispatcher(t *testing.T) {
	tower := &TowerResponder{
		factory: newTestFactory(t),
		specs:   []tools.ToolSpec{{Name: "k8s"}},
	}
	if got := tower.fetchBaselineClusterResources(context.Background()); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

// 单条超预算时注入副本截断并标记；环内权威 raw 不变
// 预算充足时保持全文，证明不是无条件截断
func TestPrepareTowerObservationsTruncates(t *testing.T) {
	// 远超 10 token 预算，迫使走截断路径
	raw := json.RawMessage(`{"stdout":"` + strings.Repeat("A", 500) + `"}`)
	auth := []towerObservation{{
		TaskID:   "t_1",
		ToolName: "k8s",
		Summary:  "exitCode=0",
		Raw:      append(json.RawMessage(nil), raw...),
	}}

	view := prepareTowerObservationsForPrompt(auth, 10)
	if len(view) != 1 || !view[0].RawTruncated {
		t.Fatalf("want truncated injection, got len=%d trunc=%v", len(view), len(view) > 0 && view[0].RawTruncated)
	}
	if !strings.Contains(string(view[0].Raw), "truncated") {
		t.Fatalf("injection raw missing truncate mark: %s", view[0].Raw)
	}
	if auth[0].RawTruncated || string(auth[0].Raw) != string(raw) {
		t.Fatal("authoritative observation must stay full")
	}

	small := []towerObservation{{
		ToolName: "k8s",
		Raw:      json.RawMessage(`{"stdout":"hi","exitCode":0}`),
	}}
	full := prepareTowerObservationsForPrompt(small, 8_000)
	if full[0].RawTruncated || string(full[0].Raw) != string(small[0].Raw) {
		t.Fatalf("small raw should stay full: trunc=%v raw=%s", full[0].RawTruncated, full[0].Raw)
	}
}

// 多条共享预算时优先保留较新观察全文；旧条截断或占位，权威切片不变
func TestPrepareTowerObservationsBudget(t *testing.T) {
	oldMark := "OLD_RAW_MARK_zzz"
	newMark := "NEW_RAW_MARK_yyy"
	// 单条均大于预算份额，迫使新条吃满、旧条退让
	oldRaw := json.RawMessage(`{"stdout":"` + oldMark + strings.Repeat("o", 200) + `"}`)
	newRaw := json.RawMessage(`{"stdout":"` + newMark + strings.Repeat("n", 200) + `"}`)
	auth := []towerObservation{
		{TaskID: "t_old", ToolName: "k8s", Summary: "old", Raw: append(json.RawMessage(nil), oldRaw...)},
		{TaskID: "t_new", ToolName: "k8s", Summary: "new", Raw: append(json.RawMessage(nil), newRaw...)},
	}

	view := prepareTowerObservationsForPrompt(auth, 60)
	if view[1].RawTruncated || string(view[1].Raw) != string(newRaw) {
		t.Fatalf("newest must stay full: trunc=%v raw=%s", view[1].RawTruncated, view[1].Raw)
	}
	if !view[0].RawTruncated {
		t.Fatal("oldest must yield when shared budget is tight")
	}
	if auth[0].RawTruncated || auth[1].RawTruncated {
		t.Fatal("authoritative observations must not set rawTruncated")
	}
	if string(auth[0].Raw) != string(oldRaw) || string(auth[1].Raw) != string(newRaw) {
		t.Fatal("authoritative raw must not mutate")
	}

	// 预算刚好等于新条成本时，旧条只能占位
	tiny := prepareTowerObservationsForPrompt(auth, estimateTokens(string(newRaw)))
	if tiny[1].RawTruncated || string(tiny[1].Raw) != string(newRaw) {
		t.Fatalf("newest must stay full when budget equals its cost: %s", tiny[1].Raw)
	}
	if !tiny[0].RawTruncated || !strings.Contains(string(tiny[0].Raw), "omitted for shared") {
		t.Fatalf("oldest must omit when remaining is zero: %s", tiny[0].Raw)
	}
}

func TestValidateTowerDecision(t *testing.T) {
	specs := []tools.ToolSpec{{
		Name:        "fake.list_pods",
		Description: "d",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	tower := &TowerResponder{
		dispatcher: tools.NewDispatcher(registry, nil),
		specs:      specs,
	}

	if err := tower.validateTowerDecision(towerLLMOutput{Action: "reply", Content: "ok"}); err != nil {
		t.Fatalf("valid reply: %v", err)
	}
	if err := tower.validateTowerDecision(towerLLMOutput{Action: "escalate"}); err != nil {
		t.Fatalf("valid escalate: %v", err)
	}
	if err := tower.validateTowerDecision(towerLLMOutput{
		Action: "call_tool",
		ToolCall: &towerToolCallJSON{
			ToolName:  "fake.list_pods",
			Arguments: json.RawMessage(`{}`),
			Purpose:   "查",
		},
	}); err != nil {
		t.Fatalf("valid call_tool: %v", err)
	}
	if err := tower.validateTowerDecision(towerLLMOutput{Action: "reply"}); err == nil {
		t.Fatal("empty reply content should fail")
	}
	if err := tower.validateTowerDecision(towerLLMOutput{Action: "other"}); err == nil {
		t.Fatal("invalid action should fail")
	}
	if err := tower.validateTowerDecision(towerLLMOutput{
		Action: "call_tool",
		ToolCall: &towerToolCallJSON{
			ToolName:  "not.registered",
			Arguments: json.RawMessage(`{}`),
		},
	}); err == nil {
		t.Fatal("unknown tool should fail")
	}
}
