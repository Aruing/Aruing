// 测试假工具包：提供固定响应的工具实现，仅供测试导入
// 产品二进制与命令行入口不得依赖本包
package toolstest

import (
	"context"
	"encoding/json"

	"aruing/internal/core"
	"aruing/internal/tools"
)

// 返回固定的容器组列表数据，模拟演示接口未就绪场景
type fakeListPodsTool struct{}

// 返回假工具的固定规格
func (t *fakeListPodsTool) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        "fake.list_pods",
		Description: "模拟查询容器组列表，返回固定数据：演示接口未就绪，重启次数为八",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`),
	}
}

// 执行假查询，忽略参数，返回固定证据
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

// 创建返回固定容器组列表的假工具
func NewFakeListPodsTool() tools.Tool {
	return &fakeListPodsTool{}
}
