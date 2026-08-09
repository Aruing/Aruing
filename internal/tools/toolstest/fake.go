// Package toolstest 提供测试用假工具，仅供测试 import。
// 产品二进制与 cmd 不得依赖本包。
package toolstest

import (
	"context"
	"encoding/json"

	"aruing/internal/core"
	"aruing/internal/tools"
)

// 返回固定的 Pod 列表数据，模拟 demo-api 未就绪的场景
type fakeListPodsTool struct{}

// Spec 返回假工具的固定规格
func (t *fakeListPodsTool) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "fake.list_pods",
		Description: "模拟查询 Pod 列表，返回固定数据：demo-api 未就绪，restartCount=8",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`),
	}
}

// Execute 执行假查询，忽略参数，返回固定证据
func (t *fakeListPodsTool) Execute(ctx context.Context, args json.RawMessage) (*core.Evidence, error) {
	raw := json.RawMessage(`{"pods":[{"name":"demo-api-7c8f9d","phase":"Running","ready":false,"reason":"CrashLoopBackOff","restartCount":8}]}`)

	return &core.Evidence{
		Source:      "fake",
		ToolName:    t.Spec().Name,
		CommandView: "kubectl get pods -n default -l app=demo-api",
		Summary:     "找到 1 个 Pod：demo-api-7c8f9d，状态 CrashLoopBackOff，restartCount=8",
		Raw:         raw,
	}, nil
}

// NewFakeListPodsTool 创建假工具实例
func NewFakeListPodsTool() tools.Tool {
	return &fakeListPodsTool{}
}
