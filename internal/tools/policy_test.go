package tools_test

import (
	"github.com/Aruing/Aruing/internal/tools"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/tools/toolstest"
)

// 只读策略必须放行常见只读集群子命令
func TestReadonlyPolicyAllowK8s(t *testing.T) {
	policy := tools.NewReadonlyPolicy()
	cases := [][]string{
		{"get", "pods", "-n", "default"},
		{"describe", "pod", "x"},
		{"logs", "pod/x", "-n", "default"},
		{"top", "pods"},
		{"api-resources"},
		{"api-versions"},
		{"explain", "pod"},
		{"version"},
		{"cluster-info"},
		{"auth", "can-i", "get", "pods"},
		{"config", "view"},
		{"config", "current-context"},
		{"diff", "-f", "m.yaml"},
		{"wait", "--for=condition=Ready", "pod/x"},
		{"events", "-n", "default"},
	}
	for _, argv := range cases {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			decision, reason := policy.Check("k8s", mustJSONArgs(t, argv))
			if decision != tools.DecisionAllow {
				t.Fatalf("decision = %v (%s), want allow", decision, reason)
			}
		})
	}
}

// 写操作与未知子命令必须被拒绝，且不依赖工具实现
func TestReadonlyPolicyDenyK8s(t *testing.T) {
	policy := tools.NewReadonlyPolicy()
	cases := []struct {
		name string
		args json.RawMessage
		want string
	}{
		{name: "apply", args: mustJSONArgs(t, []string{"apply", "-f", "x.yaml"}), want: "not allowed"},
		{name: "delete", args: mustJSONArgs(t, []string{"delete", "pod", "x"}), want: "not allowed"},
		{name: "exec", args: mustJSONArgs(t, []string{"exec", "pod/x", "--", "sh"}), want: "not allowed"},
		{name: "patch", args: mustJSONArgs(t, []string{"patch", "deploy", "x", "-p", "{}"}), want: "not allowed"},
		{name: "unknown command", args: mustJSONArgs(t, []string{"something-new"}), want: "allowlist"},
		{name: "empty argv", args: json.RawMessage(`{"argv":[]}`), want: "empty"},
		{name: "missing args", args: nil, want: "required"},
		{name: "auth without can-i", args: mustJSONArgs(t, []string{"auth", "reconcile"}), want: "can-i"},
		{name: "config set", args: mustJSONArgs(t, []string{"config", "set-context", "x"}), want: "not allowed"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			decision, reason := policy.Check("k8s", test.args)
			if decision != tools.DecisionDeny {
				t.Fatalf("decision = %v (%s), want deny", decision, reason)
			}
			if !strings.Contains(reason, test.want) {
				t.Fatalf("reason = %q, want containing %q", reason, test.want)
			}
		})
	}
}

// 假工具应被只读策略放行，避免破坏默认闭环
func TestReadonlyPolicyAllowFakeTool(t *testing.T) {
	decision, reason := tools.NewReadonlyPolicy().Check("fake.list_pods", json.RawMessage(`{}`))
	if decision != tools.DecisionAllow {
		t.Fatalf("decision = %v (%s), want allow", decision, reason)
	}
}

// 调度器在拒绝时不得调用工具
func TestDispatcherPolicyDeny(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	// 用只读策略加伪装成集群的任务名会先拒绝；这里注册一个会崩溃的工具验证未执行
	if err := registry.Register(panicTool{}); err != nil {
		t.Fatalf("register panic tool: %v", err)
	}

	dispatcher := tools.NewDispatcher(registry, tools.NewReadonlyPolicy())
	_, err := dispatcher.Execute(context.Background(), core.Task{
		ID:        "t1",
		RunID:     "run1",
		ToolName:  "k8s",
		Arguments: mustJSONArgs(t, []string{"delete", "pod", "x"}),
	})
	requireErrorContains(t, err, "denied by policy")
}

// 调度器在允许时仍走原有执行路径
func TestDispatcherPolicyAllow(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatcher := tools.NewDispatcher(registry, tools.NewReadonlyPolicy())
	evidence, err := dispatcher.Execute(context.Background(), core.Task{
		ID:       "t1",
		RunID:    "run1",
		ToolName: "fake.list_pods",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if evidence.TaskID != "t1" || evidence.RunID != "run1" {
		t.Fatalf("evidence ownership: %#v", evidence)
	}
}

// 策略为空时退化为全部允许
func TestDispatcherNilPolicyAllowAll(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(toolstest.NewFakeListPodsTool()); err != nil {
		t.Fatalf("register: %v", err)
	}
	dispatcher := tools.NewDispatcher(registry, nil)
	if _, err := dispatcher.Execute(context.Background(), core.Task{
		ID:       "t1",
		RunID:    "run1",
		ToolName: "fake.list_pods",
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func mustJSONArgs(t *testing.T, argv []string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"argv": argv})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// 若被误调用则失败测试，用于证明拒绝短路
type panicTool struct{}

func (panicTool) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "k8s",
		Description: "panic if executed",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`),
	}
}

func (panicTool) Execute(context.Context, json.RawMessage) (*core.Evidence, error) {
	panic("tool must not execute when policy denies")
}
