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
func (testEvidenceTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        TestToolName,
		Description: "返回预先配置的测试证据",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`),
	}
}

// 返回预先配置的证据，包括用于模拟接口违约的空值
func (tool testEvidenceTool) Execute(context.Context, json.RawMessage) (*core.Evidence, error) {
	return tool.evidence, nil
}

// 模拟缺少名称的工具，用于验证注册阶段的名称约束
type emptyNameTool struct{}

// 返回空名称以触发注册表的名称校验
func (emptyNameTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "",
		Description: "模拟缺少名称的工具",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

// 返回空证据即可，因为名称校验应在执行前阻止该工具注册
func (emptyNameTool) Execute(context.Context, json.RawMessage) (*core.Evidence, error) {
	return nil, nil
}

// 模拟 Schema 非法的工具，用于验证注册阶段的 Schema 编译
type invalidSchemaTool struct {
	schema json.RawMessage
}

func (t invalidSchemaTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "bad.schema",
		Description: "模拟非法输入 Schema",
		InputSchema: t.schema,
	}
}

func (invalidSchemaTool) Execute(context.Context, json.RawMessage) (*core.Evidence, error) {
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
	// 代表校验：nil、空名、坏 schema（JSON Schema 语义）
	tests := []struct {
		name      string
		tool      Tool
		wantError string
	}{
		{name: "nil tool", tool: nil, wantError: "tool is required"},
		{name: "empty name", tool: emptyNameTool{}, wantError: "tool name is required"},
		{
			name:      "invalid json schema",
			tool:      invalidSchemaTool{schema: json.RawMessage(`{"type":"not-a-type"}`)},
			wantError: "valid JSON Schema",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := NewRegistry().Register(test.tool)
			requireErrorContains(t, err, test.wantError)
		})
	}
}

// Specs 应按名称稳定排序，并返回可独立修改的副本
func TestRegistrySpecs(t *testing.T) {
	registry := NewRegistry()
	for _, tool := range []Tool{
		namedSpecTool{name: "z.tool"},
		namedSpecTool{name: "a.tool"},
		NewFakeListPodsTool(),
	} {
		if err := registry.Register(tool); err != nil {
			t.Fatalf("register %s: %v", tool.Spec().Name, err)
		}
	}

	specs := registry.Specs()
	if len(specs) != 3 {
		t.Fatalf("len(specs) = %d, want 3", len(specs))
	}
	if specs[0].Name != "a.tool" || specs[1].Name != "fake.list_pods" || specs[2].Name != "z.tool" {
		t.Fatalf("specs order = %q, %q, %q", specs[0].Name, specs[1].Name, specs[2].Name)
	}

	// 修改返回切片中的 Schema 不应污染注册表
	specs[0].InputSchema[0] = 'X'
	again := registry.Specs()
	if again[0].InputSchema[0] == 'X' {
		t.Fatal("Specs returned shared InputSchema buffer")
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
	evidence, err := NewDispatcher(registry, nil).Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}
	if evidence.RunID != task.RunID {
		t.Errorf("RunID = %q, want %q", evidence.RunID, task.RunID)
	}
	if evidence.TaskID != task.ID {
		t.Errorf("TaskID = %q, want %q", evidence.TaskID, task.ID)
	}
	if evidence.ToolName != tool.Spec().Name {
		t.Errorf("ToolName = %q, want %q", evidence.ToolName, tool.Spec().Name)
	}
}

// 空 RunID 表示基线非诊断观察，调度器应允许执行并原样拷贝到证据
func TestDispatcherExecuteEmptyRunID(t *testing.T) {
	registry := NewRegistry()
	tool := testEvidenceTool{
		evidence: &core.Evidence{Summary: "baseline obs"},
	}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register test tool: %v", err)
	}

	task := core.Task{
		ID:       "t_baseline",
		RunID:    "",
		ToolName: TestToolName,
	}
	evidence, err := NewDispatcher(registry, nil).Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}
	if evidence.RunID != "" {
		t.Errorf("RunID = %q, want empty", evidence.RunID)
	}
	if evidence.TaskID != task.ID {
		t.Errorf("TaskID = %q, want %q", evidence.TaskID, task.ID)
	}
	if evidence.Summary != "baseline obs" {
		t.Errorf("Summary = %q", evidence.Summary)
	}
}

// 缺少调度依赖或任务关联字段时应返回明确错误，避免执行阶段生成无法回溯的证据
func TestDispatcherValidate(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewFakeListPodsTool()); err != nil {
		t.Fatalf("register fake tool: %v", err)
	}

	// 代表校验：调度器无 registry、任务缺关键身份字段
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
			name:       "missing task ID",
			dispatcher: NewDispatcher(registry, nil),
			task:       core.Task{RunID: "run_test", ToolName: "fake.list_pods"},
			wantError:  "requires an ID",
		},
		{
			name:       "missing tool name",
			dispatcher: NewDispatcher(registry, nil),
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

	_, err := NewDispatcher(registry, nil).Execute(context.Background(), newTestTask())
	requireErrorContains(t, err, "returned nil evidence")
}

// 仅用于 Specs 排序测试的最小工具
type namedSpecTool struct {
	name string
}

func (t namedSpecTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        t.name,
		Description: "specs sort fixture",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func (namedSpecTool) Execute(context.Context, json.RawMessage) (*core.Evidence, error) {
	return &core.Evidence{Summary: "ok"}, nil
}
