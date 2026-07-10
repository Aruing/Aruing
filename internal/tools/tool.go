// 工具包是诊断系统访问外部数据的统一通道
//
// 规划器只能生成白名单内的取证任务，工具调度器负责查找并执行已注册工具，具体工具负责校验自身参数
// 模型输出不能绕过这里直接执行命令
//
// 当前阶段所有工具实现都只读，但工具接口本身不限定只读
// 后续可能扩展需要用户确认的变更性操作，例如执行修复命令或触发探测性变更
// 读写策略应在注册和调度层面控制，而不是在工具接口上限定
//
// 当前阶段先提供假工具结果，证明报告中的结论可以引用真实存在的证据编号
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"aruing/internal/core"
)

// 工具统一接口，所有取证和变更工具都必须实现
// 编排层通过该接口调用工具，可以知道但不关心具体数据来源是 Kubernetes、Prometheus 还是什么
// 工具接口不限定只读或写入，读写策略由注册和调度层面控制
//
// 工具粒度应该是后端级别，不是命令级别
// 例如一个 k8s 工具接受结构化查询参数（资源类型、命名空间、名称等），内部翻译为对应的 API 调用
// 而不是为 list_pods、get_service、get_events 各封装一个独立工具
// 这样模型只需要知道有哪些后端可用，由工具内部处理具体的命令差异和版本兼容
type Tool interface {
	// 工具名称，标识一个后端数据源，例如 k8s 或 prometheus
	// 规划器生成的任务通过该名称在注册表中查找对应工具
	Name() string

	// 工具描述，供规划器理解工具能力和用途，决定是否生成该工具的取证任务
	Description() string

	// 执行工具调用，输入为规划器生成的结构化参数，输出为证据
	// 参数描述具体要查询什么，由工具内部校验并翻译为后端实际的调用方式
	// 参数不符合当前工具约束时返回错误，调度器不解析各工具的参数结构
	// context 用于超时和取消控制，工具应尊重 ctx.Done()
	Execute(ctx context.Context, args json.RawMessage) (*core.Evidence, error)
}

// 工具注册表，维护工具名称到工具实例的映射
// 规划器只能使用已注册的工具，未注册的工具名称会被调度器拒绝
type Registry struct {
	tools map[string]Tool
}

// 创建空注册表
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// 注册一个工具，如果工具名称已存在则返回错误，避免重复注册覆盖
func (r *Registry) Register(t Tool) error {
	name := t.Name()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool already registered: %s", name)
	}
	r.tools[name] = t
	return nil
}

// 按名称查找工具，找不到返回错误
func (r *Registry) Get(name string) (Tool, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return t, nil
}

// 工具调度器，负责接收取证任务、查找已注册工具、执行调用并产出证据
// 编排层把任务交给调度器，调度器返回证据，编排层不需要直接接触工具实例
// 具体参数约束由对应工具校验，调度器不依赖不同后端的参数结构
// 调度器是实施读写策略的合适位置，例如只读模式下拒绝变更类工具的调用
type Dispatcher struct {
	registry *Registry
}

// 创建调度器，绑定一个工具注册表
func NewDispatcher(r *Registry) *Dispatcher {
	return &Dispatcher{registry: r}
}

// 执行一个取证任务，返回产出的证据
// 任务中的工具名称必须在注册表中存在，否则返回错误
// 任务参数由找到的工具负责校验，校验失败时透传带工具名称的上下文错误
// 任务的 RunID 和 TaskID 会被写入证据，保持证据到任务的回溯链
func (d *Dispatcher) Execute(ctx context.Context, task core.EvidenceTask) (*core.Evidence, error) {
	tool, err := d.registry.Get(task.ToolName)
	if err != nil {
		return nil, err
	}

	evidence, err := tool.Execute(ctx, task.Arguments)
	if err != nil {
		return nil, fmt.Errorf("tool %s failed: %w", task.ToolName, err)
	}

	// 工具只产出证据内容，归属信息由调度器统一填充
	evidence.RunID = task.RunID
	evidence.TaskID = task.ID

	return evidence, nil
}

// ---------- 假工具，用于最小闭环阶段验证证据链是否完整

// 返回固定的 Pod 列表数据，模拟 demo-api 未就绪的场景
type fakeListPodsTool struct{}

func (t *fakeListPodsTool) Name() string { return "fake.list_pods" }

func (t *fakeListPodsTool) Description() string {
	return "模拟查询 Pod 列表，返回固定数据：demo-api 未就绪，restartCount=8"
}

// 执行假查询，忽略参数，返回固定证据
// 证据的 ID 由调用方（调度器）或编排层在持久化时填充
func (t *fakeListPodsTool) Execute(ctx context.Context, args json.RawMessage) (*core.Evidence, error) {
	raw := json.RawMessage(`{"pods":[{"name":"demo-api-7c8f9d","phase":"Running","ready":false,"reason":"CrashLoopBackOff","restartCount":8}]}`)

	return &core.Evidence{
		Source:      "fake",
		ToolName:    t.Name(),
		CommandView: "kubectl get pods -n default -l app=demo-api",
		Summary:     "找到 1 个 Pod：demo-api-7c8f9d，状态 CrashLoopBackOff，restartCount=8",
		Raw:         raw,
	}, nil
}

// 创建假工具实例，方便注册时使用
func NewFakeListPodsTool() Tool {
	return &fakeListPodsTool{}
}
