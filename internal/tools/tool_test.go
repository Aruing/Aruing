package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"aruing/internal/core"
)

// -------------------------- 模拟一个工具 --------------------------

const TestToolName = "test.evidence"

// 提供可控的证据返回值，用于验证调度器如何处理工具输出
type testEvidenceTool struct {
	evidence *core.Evidence
}

// 使用独立名称避免与正常假工具的注册项冲突
func (testEvidenceTool) Name() string { return TestToolName }

// 提供满足工具接口的最小说明，测试不依赖其具体内容
func (testEvidenceTool) Description() string { return "返回预先配置的测试证据" }

// 返回预先配置的证据，包括用于模拟接口违约的空值
func (tool testEvidenceTool) Execute(context.Context, json.RawMessage) (*core.Evidence, error) {
	return tool.evidence, nil
}

// 模拟缺少名称的工具，用于验证注册阶段的名称约束
type emptyNameTool struct{}

// 返回空名称以触发注册表的名称校验
func (emptyNameTool) Name() string { return "" }

// 提供满足工具接口的最小说明，测试不依赖其具体内容
func (emptyNameTool) Description() string { return "模拟缺少名称的工具" }

// 返回空证据即可，因为名称校验应在执行前阻止该工具注册
func (emptyNameTool) Execute(context.Context, json.RawMessage) (*core.Evidence, error) {
	return nil, nil
}

// -------------------------- 工具函数 --------------------------

// 构造满足执行条件的最小任务，让边界用例只暴露当前检查的字段
func newTestTask() core.Task {
	return core.Task{
		ID:       "t_test",
		RunID:    "run_test",
		ToolName: TestToolName,
	}
}

// 校验错误包含稳定的关键信息，避免测试依赖完整错误文本
func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want containing %q", err, want)
	}
}

// -------------------------- 测试主要功能 --------------------------

// 注册表必须保留唯一工具名称，避免后注册的工具覆盖已有执行能力
func TestRegistryRegister(t *testing.T) {
	registry := NewRegistry()
	tool := testEvidenceTool{}

	if err := registry.Register(tool); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	if err := registry.Register(tool); err == nil {
		t.Fatal("register duplicate tool: error = nil")
	} else {
		requireErrorContains(t, err, "already registered")
	}
}

// 无效工具应在注册阶段被拒绝，避免空名称污染注册表或空对象触发崩溃
func TestRegistryValidate(t *testing.T) {
	tests := []struct {
		name      string
		tool      Tool
		wantError string
	}{
		{name: "nil tool", tool: nil, wantError: "tool is required"},
		{name: "empty name", tool: emptyNameTool{}, wantError: "tool name is required"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := NewRegistry().Register(test.tool)
			requireErrorContains(t, err, test.wantError)
		})
	}
}

// 归属字段必须由调度器根据任务和注册工具填写，避免工具返回的数据破坏证据链
func TestDispatcherExecute(t *testing.T) {
	registry := NewRegistry()
	tool := testEvidenceTool{
		evidence: &core.Evidence{ToolName: "untrusted"},
	}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register test tool: %v", err)
	}

	task := newTestTask()
	evidence, err := NewDispatcher(registry).Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}
	if evidence.RunID != task.RunID {
		t.Errorf("RunID = %q, want %q", evidence.RunID, task.RunID)
	}
	if evidence.TaskID != task.ID {
		t.Errorf("TaskID = %q, want %q", evidence.TaskID, task.ID)
	}
	if evidence.ToolName != tool.Name() {
		t.Errorf("ToolName = %q, want %q", evidence.ToolName, tool.Name())
	}
}

// 缺少调度依赖或任务关联字段时应返回明确错误，避免执行阶段生成无法回溯的证据
func TestDispatcherValidate(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewFakeListPodsTool()); err != nil {
		t.Fatalf("register fake tool: %v", err)
	}

	tests := []struct {
		name       string
		dispatcher *Dispatcher
		task       core.Task
		wantError  string
	}{
		{
			name:       "nil dispatcher",
			dispatcher: nil,
			task:       core.Task{ID: "t_test", RunID: "run_test", ToolName: "fake.list_pods"},
			wantError:  "requires a registry",
		},
		{
			name:       "missing registry",
			dispatcher: &Dispatcher{},
			task:       core.Task{ID: "t_test", RunID: "run_test", ToolName: "fake.list_pods"},
			wantError:  "requires a registry",
		},
		{
			name:       "missing task ID",
			dispatcher: NewDispatcher(registry),
			task:       core.Task{RunID: "run_test", ToolName: "fake.list_pods"},
			wantError:  "requires an ID",
		},
		{
			name:       "missing run ID",
			dispatcher: NewDispatcher(registry),
			task:       core.Task{ID: "t_test", ToolName: "fake.list_pods"},
			wantError:  "requires a run ID",
		},
		{
			name:       "missing tool name",
			dispatcher: NewDispatcher(registry),
			task:       core.Task{ID: "t_test", RunID: "run_test"},
			wantError:  "requires a tool name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.dispatcher.Execute(context.Background(), test.task)
			requireErrorContains(t, err, test.wantError)
		})
	}
}

// 工具未返回证据属于接口契约错误，调度器应拒绝空结果而不是解引用导致崩溃
func TestDispatcherNilEvidence(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testEvidenceTool{}); err != nil {
		t.Fatalf("register test tool: %v", err)
	}

	_, err := NewDispatcher(registry).Execute(context.Background(), newTestTask())
	requireErrorContains(t, err, "returned nil evidence")
}
