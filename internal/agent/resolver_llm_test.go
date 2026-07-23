package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"aruing/internal/core"
	"aruing/internal/tools"
)

// 标准路径：模型提交带证据的目标
func TestLLMResolverSubmit(t *testing.T) {
	specs := []tools.ToolSpec{{
		Name:        "fake.list_pods",
		Description: "list pods",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}
	body := `{"action":"submit_targets","reason":"found","tool_calls":[],"targets":[{"node_id":"node_1","type":"k8s.resource","attrs":{"k8s.name":"demo"},"evidence_ids":["e_1"]}],"error":""}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	resolver, err := NewLLMResolver(client, specs)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	action, err := resolver.Next(context.Background(), ResolveState{
		Query: core.Query{
			RunID: "run_1",
			Nodes: []core.Node{{ID: "node_1", Text: "demo"}},
		},
		Evidence: []core.Evidence{{ID: "e_1", Summary: "ok"}},
	})
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if action.Action != ResolveActionSubmitTargets {
		t.Fatalf("action = %q", action.Action)
	}
	if len(action.Targets) != 1 || action.Targets[0].NodeID != "node_1" {
		t.Errorf("targets = %+v", action.Targets)
	}
	if len(action.Targets[0].EvidenceIDs) != 1 || action.Targets[0].EvidenceIDs[0] != "e_1" {
		t.Errorf("evidence_ids = %+v", action.Targets[0].EvidenceIDs)
	}
}

// call_tool 校验工具名必须在 Specs 中
func TestLLMResolverCallTool(t *testing.T) {
	specs := []tools.ToolSpec{{
		Name:        "k8s",
		Description: "kubectl",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"argv":{"type":"array"}}}`),
	}}
	body := `{"action":"call_tool","reason":"list","tool_calls":[{"tool_name":"k8s","arguments":{"argv":["get","pods"]},"purpose":"list","refs":["node_1"]}],"targets":[],"error":""}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeChatCompletion(w, body)
	})
	resolver, err := NewLLMResolver(client, specs)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	action, err := resolver.Next(context.Background(), ResolveState{
		Query: core.Query{Nodes: []core.Node{{ID: "node_1"}}},
	})
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if action.Action != ResolveActionCallTool || len(action.ToolCalls) != 1 {
		t.Fatalf("action = %+v", action)
	}
	if action.ToolCalls[0].ToolName != "k8s" {
		t.Errorf("tool = %q", action.ToolCalls[0].ToolName)
	}
}

// 未知工具 / 未知节点 / 无证据提交 → 业务重试后 ErrLLMOutputInconsistent
func TestLLMResolverInvalidOutput(t *testing.T) {
	specs := []tools.ToolSpec{{
		Name:        "k8s",
		Description: "k",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unknown tool",
			body: `{"action":"call_tool","reason":"x","tool_calls":[{"tool_name":"nope","arguments":{},"purpose":"x","refs":[]}],"targets":[],"error":""}`,
			want: "unknown tool",
		},
		{
			name: "submit without evidence",
			body: `{"action":"submit_targets","reason":"x","tool_calls":[],"targets":[{"node_id":"node_1","type":"r","attrs":{},"evidence_ids":[]}],"error":""}`,
			want: "evidence",
		},
		{
			name: "unknown node",
			body: `{"action":"submit_targets","reason":"x","tool_calls":[],"targets":[{"node_id":"node_missing","type":"r","attrs":{},"evidence_ids":["e_1"]}],"error":""}`,
			want: "unknown node",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := test.body
			client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
				writeChatCompletion(w, body)
			})
			resolver, err := NewLLMResolver(client, specs)
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			_, err = resolver.Next(context.Background(), ResolveState{
				Query:    core.Query{Nodes: []core.Node{{ID: "node_1"}}},
				Evidence: []core.Evidence{{ID: "e_1"}},
			})
			if err == nil {
				t.Fatal("error = nil")
			}
			if !errors.Is(err, ErrLLMOutputInconsistent) {
				t.Errorf("error = %v, want ErrLLMOutputInconsistent", err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want containing %q", err, test.want)
			}
		})
	}
}

// 首次违规、第二次合法时应重试成功
func TestLLMResolverRetry(t *testing.T) {
	specs := []tools.ToolSpec{{
		Name:        "k8s",
		Description: "k",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}
	var calls atomic.Int32
	dirty := `{"action":"call_tool","reason":"x","tool_calls":[{"tool_name":"nope","arguments":{},"purpose":"x","refs":[]}],"targets":[],"error":""}`
	clean := `{"action":"fail","reason":"give up","tool_calls":[],"targets":[],"error":"no match"}`
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			writeChatCompletion(w, dirty)
			return
		}
		writeChatCompletion(w, clean)
	})
	resolver, err := NewLLMResolver(client, specs)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	action, err := resolver.Next(context.Background(), ResolveState{
		Query: core.Query{Nodes: []core.Node{{ID: "node_1"}}},
	})
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if action.Action != ResolveActionFail {
		t.Fatalf("action = %q, want fail", action.Action)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

// 依赖缺失时应构造失败
func TestNewLLMResolverValidate(t *testing.T) {
	if _, err := NewLLMResolver(nil, nil); err == nil {
		t.Fatal("want nil client rejection")
	}
}

// system prompt 应注入 Specs 并替换占位符
func TestLLMResolverPromptIncludesSpecs(t *testing.T) {
	specs := []tools.ToolSpec{{
		Name:        "k8s",
		Description: "kubectl backend",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}}
	system, err := buildResolverSystemPrompt(resolverPrompt, specs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(system, "k8s") {
		t.Errorf("system prompt missing tool specs")
	}
	if strings.Contains(system, "{{TOOL_SPECS}}") {
		t.Error("placeholder not replaced")
	}
}
