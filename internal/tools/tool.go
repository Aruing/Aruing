// 工具包是诊断系统访问外部数据的统一通道
//
// 规划器只能生成白名单内的取证任务，工具调度器负责查找并执行已注册工具，具体工具负责校验自身参数
// 模型输出不能绕过这里直接执行命令
//
// 工具按后端粒度注册（例如集群、监控），通过规格暴露名称、描述和参数模式
// 规划器从注册表规格列表发现可用能力，不在接口层枚举资源类型或子命令
//
// 工具接口本身不限定只读；读写策略由策略（挂在调度器执行前）与注册层面控制
// 测试替身见测试包中的假工具工厂
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"aruing/internal/core"
)

// 描述一个已注册工具的可发现元数据，供规划器构造提示词或工具列表
// 输入模式必须是合法模式对象，约束执行参数
type ToolSpec struct {
	// 工具名称，标识一个后端数据源，例如集群或监控
	Name string `json:"name"`
	// 工具描述，供规划器理解能力与用途
	Description string `json:"description"`
	// 调用参数的模式，注册时校验合法性，执行时由工具按该契约校验实例
	InputSchema json.RawMessage `json:"inputSchema"`
}

// 工具统一接口，所有取证和变更工具都必须实现
// 编排层通过该接口调用工具，可以知道但不关心具体数据来源是集群、监控还是什么
// 工具接口不限定只读或写入，读写策略由注册和调度层面控制
//
// 工具粒度应该是后端级别，不是命令级别
// 例如集群工具接受结构化参数列表并直接调用集群命令行，不在本产品内按查询类动作拆成多个工具
type Tool interface {
	// 返回工具的可发现元数据，名称必须与注册表键一致
	Spec() ToolSpec

	// 执行工具调用，输入为规划器生成的结构化参数，输出为证据
	// 参数描述具体要查询什么，由工具内部校验并翻译为后端实际的调用方式
	// 参数不符合当前工具约束时返回错误，调度器不解析各工具的参数结构
	// 上下文用于超时和取消控制，工具应尊重取消信号
	Execute(ctx context.Context, args json.RawMessage) (*core.Evidence, error)
}

// 工具注册表，维护工具名称到工具实例的映射
// 规划器只能使用已注册的工具，未注册的工具名称会被调度器拒绝
type Registry struct {
	// 工具名到实例的映射，注册时写入
	tools map[string]Tool
}

// 创建空注册表
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// 注册一个名称非空且参数模式合法的工具，拒绝空对象和重复名称，避免无效能力进入白名单
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return errors.New("tool is required")
	}

	spec := t.Spec()
	if spec.Name == "" {
		return errors.New("tool name is required")
	}
	if err := validateInputSchema(spec.InputSchema); err != nil {
		return fmt.Errorf("tool %s: %w", spec.Name, err)
	}
	if _, exists := r.tools[spec.Name]; exists {
		return fmt.Errorf("tool already registered: %s", spec.Name)
	}
	r.tools[spec.Name] = t
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

// 返回已注册工具规格的稳定排序副本，调用方修改返回值不会影响注册表
func (r *Registry) Specs() []ToolSpec {
	if r == nil || len(r.tools) == 0 {
		return nil
	}

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]ToolSpec, 0, len(names))
	for _, name := range names {
		spec := r.tools[name].Spec()
		out = append(out, ToolSpec{
			Name:        spec.Name,
			Description: spec.Description,
			InputSchema: append(json.RawMessage(nil), spec.InputSchema...),
		})
	}
	return out
}

// 工具调度器，负责接收任务、查找已注册工具、执行调用并产出证据
// 编排层把任务交给调度器，调度器返回证据，编排层不需要直接接触工具实例
// 具体参数约束由对应工具校验，调度器不依赖不同后端的参数结构
// 调度器在执行前调用策略：能力由注册表开放，授权由策略决定
type Dispatcher struct {
	// 已注册工具查找表
	registry *Registry
	// 执行前授权；构造时为空会换成全放行（仅便于测试）
	policy Policy
}

// 创建调度器，绑定工具注册表与可选策略
// 策略为空时使用全放行，避免测试样板代码；生产装配应传入只读策略
func NewDispatcher(r *Registry, policy Policy) *Dispatcher {
	if policy == nil {
		policy = NewAllowAllPolicy()
	}
	return &Dispatcher{registry: r, policy: policy}
}

// 执行一个工具任务，返回产出的证据
// 任务必须具备编号与工具名，工具须在注册表中存在，否则返回错误
// 运行编号可空：基线工具环表示非诊断观察，不得进入判决证据账本；正式诊断管道仍应填写
// 授权未通过时返回错误且不调用工具；参数校验失败时透传带工具名称的上下文错误
// 任务的运行编号（可空）与任务编号会被写入证据，保持证据到任务的回溯链
func (d *Dispatcher) Execute(ctx context.Context, task core.Task) (*core.Evidence, error) {
	if d == nil || d.registry == nil {
		return nil, errors.New("dispatcher requires a registry")
	}
	if task.ID == "" {
		return nil, errors.New("task requires an ID")
	}
	if task.ToolName == "" {
		return nil, errors.New("task requires a tool name")
	}

	decision, reason := d.policy.Check(task.ToolName, task.Arguments)
	switch decision {
	case DecisionAllow:
		// 已授权，继续执行工具
	case DecisionDeny:
		if reason == "" {
			reason = "denied by policy"
		}
		return nil, fmt.Errorf("tool %s denied by policy: %s", task.ToolName, reason)
	case DecisionRequireApproval:
		// 本阶段无审批通道，与拒绝同等处理，避免静默放行
		if reason == "" {
			reason = "requires approval"
		}
		return nil, fmt.Errorf("tool %s requires approval: %s", task.ToolName, reason)
	default:
		return nil, fmt.Errorf("tool %s: unknown policy decision %v", task.ToolName, decision)
	}

	tool, err := d.registry.Get(task.ToolName)
	if err != nil {
		return nil, err
	}

	evidence, err := tool.Execute(ctx, task.Arguments)
	if err != nil {
		return nil, fmt.Errorf("tool %s failed: %w", task.ToolName, err)
	}
	if evidence == nil {
		return nil, fmt.Errorf("tool %s returned nil evidence", task.ToolName)
	}

	// 工具只产出证据内容，归属信息由调度器统一填充
	evidence.RunID = task.RunID
	evidence.TaskID = task.ID
	evidence.ToolName = tool.Spec().Name

	return evidence, nil
}

// 编译并校验输入模式是否为合法模式对象
func validateInputSchema(schema json.RawMessage) error {
	if len(bytes.TrimSpace(schema)) == 0 {
		return errors.New("input schema is required")
	}

	var probe any
	if err := json.Unmarshal(schema, &probe); err != nil {
		return fmt.Errorf("input schema is not valid JSON: %w", err)
	}
	if _, ok := probe.(map[string]any); !ok {
		return errors.New("input schema must be a JSON object")
	}

	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return fmt.Errorf("input schema: %w", err)
	}

	compiler := jsonschema.NewCompiler()
	const schemaURL = "tool-input-schema.json"
	if err := compiler.AddResource(schemaURL, doc); err != nil {
		return fmt.Errorf("input schema: %w", err)
	}
	if _, err := compiler.Compile(schemaURL); err != nil {
		return fmt.Errorf("input schema is not a valid JSON Schema: %w", err)
	}
	return nil
}
