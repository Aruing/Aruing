// 真实规划器把问题结构与已确认目标交给大模型，回填系统编号后产出计划
//
// 与假规划器共享同一计划边界：一次调用返回完整猜想与任务，不在角色内多轮调工具
// 工具规格来自注册表规格列表注入，禁止在提示词手写第二份工具清单
// 猜想与任务的系统编号与创建时间由本模块经工厂发放（对齐解析器）
package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/llm"
	"github.com/Aruing/Aruing/internal/tools"
)

//go:embed prompts/planner.md
var plannerPrompt string

// 规划业务级重试次数：结构合法但语义违规时重新请求
const maxPlanAttempts = 3

// 用大模型生成猜想与取证任务的规划器
//
// 持有不可变依赖（客户端、工厂、工具规格快照、提示词），可被多次运行复用
// 不持有跨运行可变状态；每次计划独立发结构化生成
type LLMPlanner struct {
	// 大模型客户端，每次计划发一次结构化生成
	client llm.Client
	// 领域编号与创建时间工厂，回填猜想与任务时使用
	factory *core.Factory
	// 注册表工具规格快照，用于拼系统提示词与校验工具名
	specs []tools.ToolSpec
	// 已注入工具规格的系统提示词全文，构造后只读
	prompt string
}

// 创建基于大模型的规划器
// 规格应为注册表规格列表快照；空列表时仍可构造，但任务工具名将无法通过校验
func NewLLMPlanner(client llm.Client, factory *core.Factory, specs []tools.ToolSpec) (*LLMPlanner, error) {
	if client == nil {
		return nil, errors.New("LLM planner requires an llm client")
	}
	if factory == nil {
		return nil, errors.New("LLM planner requires a factory")
	}
	copied := make([]tools.ToolSpec, len(specs))
	copy(copied, specs)
	for i := range copied {
		copied[i].InputSchema = append(json.RawMessage(nil), specs[i].InputSchema...)
	}
	system, err := buildPlannerSystemPrompt(plannerPrompt, copied)
	if err != nil {
		return nil, err
	}
	return &LLMPlanner{client: client, factory: factory, specs: copied, prompt: system}, nil
}

// 请求模型生成计划，校验后回填系统编号与运行绑定
//
// 模型若返回结构合法但语义违规的输出（未知工具、坏引用等），在业务级重试内重新请求；
// 重试上限次仍不合规则返回模型输出不一致错误
// 首轮状态中证据与判决为空时，载荷不含这两段，模型输入与早期单轮路径一致
func (p *LLMPlanner) Plan(ctx context.Context, state PlanState) (Plan, error) {
	if ctx == nil {
		return Plan{}, errors.New("planner requires a context")
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, fmt.Errorf("plan tasks: %w", err)
	}
	if p == nil {
		return Plan{}, errors.New("planner is required")
	}
	if strings.TrimSpace(state.Query.RunID) == "" {
		return Plan{}, errors.New("planner requires a run ID")
	}

	userPayload, err := buildPlannerUserPayload(state)
	if err != nil {
		return Plan{}, fmt.Errorf("build plan prompt: %w", err)
	}

	req := llm.Request{
		System: p.prompt,
		User:   userPayload,
	}

	// 后续轮判定：有历史证据即为后续轮；收集前几轮已登记猜想编号供任务引用
	followUp := len(state.Evidence) > 0
	priorHypothesisIDs := make([]string, 0, len(state.Verdicts))
	for _, v := range state.Verdicts {
		if v.HypothesisID != "" {
			priorHypothesisIDs = append(priorHypothesisIDs, v.HypothesisID)
		}
	}

	var lastOut plannerLLMOutput
	var lastValidateErr error
	for attempt := 0; attempt < maxPlanAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Plan{}, fmt.Errorf("plan tasks: %w", err)
		}

		var out plannerLLMOutput
		if gErr := p.client.GenerateJSON(ctx, req, &out); gErr != nil {
			return Plan{}, fmt.Errorf("plan with LLM: %w", gErr)
		}

		if vErr := validatePlannerOutput(out, state.Query, state.Targets, p.specs, followUp, priorHypothesisIDs); vErr != nil {
			lastOut = out
			lastValidateErr = vErr
			continue
		}

		return p.fillPlan(state.Query.RunID, out)
	}

	return Plan{}, fmt.Errorf("%w: last error: %v, last output: %+v",
		ErrLLMOutputInconsistent, lastValidateErr, lastOut)
}

// 将嵌入提示词中的工具规格占位符替换为结构化文本
func buildPlannerSystemPrompt(template string, specs []tools.ToolSpec) (string, error) {
	raw, err := json.MarshalIndent(specs, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal tool specs: %w", err)
	}
	if !strings.Contains(template, "{{TOOL_SPECS}}") {
		return "", errors.New("planner prompt missing {{TOOL_SPECS}} placeholder")
	}
	return strings.Replace(template, "{{TOOL_SPECS}}", string(raw), 1), nil
}

// 序列化规划输入：问题、目标，以及非空时的历史证据、判决与集群资源
// 用省略空值，对应为空时不出现，保证首轮无侦察、无历史时模型输入最小
func buildPlannerUserPayload(state PlanState) (string, error) {
	// 与规划状态字段对齐的用户载荷
	type payload struct {
		// 当前问题的开放线索图
		Query core.Query `json:"query"`
		// 定位阶段已确认的目标
		Targets []core.Target `json:"targets"`
		// 历史证据，首轮常为空（省略空值时省略）
		Evidence []core.Evidence `json:"evidence,omitempty"`
		// 历史判断，后续轮用于补查证据不足
		Verdicts []core.Verdict `json:"verdicts,omitempty"`
		// 集群侦察得到的资源类型列表，无集群工具时为空
		ClusterResources []ClusterResource `json:"cluster_resources,omitempty"`
		// 调查挂起后用户澄清的累积答复，规划器优先据此收敛
		Clarifications []string `json:"clarifications,omitempty"`
	}
	raw, err := json.MarshalIndent(payload(state), "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// 模型输出的中间结构，只含内容与局部引用，不含系统编号或时间
type plannerLLMOutput struct {
	// 本轮候选猜想列表
	Hypotheses []plannerHypothesisOut `json:"hypotheses"`
	// 本轮取证任务列表；后续轮可为空表示查完
	Tasks []plannerTaskOut `json:"tasks"`
	// 可选澄清提议：证据不足以继续且缺口信息用户知道时输出；与任务/猜想互斥
	Clarify *plannerClarifyOut `json:"clarify,omitempty"`
}

// 模型侧澄清请求，镜像 resolver 的 clarify 意图字段
type plannerClarifyOut struct {
	// 面向用户的问题
	Question string `json:"question"`
	// 可选候选答案
	Options []string `json:"options,omitempty"`
}

// 模型侧一条猜想，引用仅在本输出内用于任务引用
type plannerHypothesisOut struct {
	// 局部引用名，任务引用可指向它
	Ref string `json:"ref"`
	// 猜想陈述句
	Statement string `json:"statement"`
	// 提出该猜想的理由
	Reason string `json:"reason"`
	// 预期可观测信号，供验证对照
	ExpectedSignals []string `json:"expected_signals"`
}

// 模型侧一条工具任务，引用仅在本输出内用于依赖列表
type plannerTaskOut struct {
	// 局部任务引用名
	Ref string `json:"ref"`
	// 注册表中的工具名
	ToolName string `json:"tool_name"`
	// 符合工具规格的参数
	Arguments json.RawMessage `json:"arguments"`
	// 取证目的说明
	Purpose string `json:"purpose"`
	// 关联节点、目标、猜想的引用（局部猜想引用或系统编号）
	Refs []string `json:"refs"`
	// 依赖的其它任务局部引用，回填时换成系统任务编号
	DependsOn []string `json:"depends_on"`
}

// 校验模型输出满足编排与取证的最小契约
// 首轮与后续轮的校验规则不同：
//   - 首轮（非后续）：猜想与任务均至少一项
//   - 后续轮：任务可为空（等于规划器宣布查完），猜想可选
//
// 既往猜想编号为前几轮已登记的猜想系统编号，允许后续轮任务引用现有猜想
func validatePlannerOutput(out plannerLLMOutput, query core.Query, targets []core.Target, specs []tools.ToolSpec, followUp bool, priorHypothesisIDs []string) error {
	// 澄清提议与任务/猜想互斥：同给为规划错误（编排据此挂起，静默取舍会丢意图）
	if out.Clarify != nil {
		if len(out.Hypotheses) > 0 || len(out.Tasks) > 0 {
			return errors.New("clarify must not carry hypotheses or tasks")
		}
		if strings.TrimSpace(out.Clarify.Question) == "" {
			return errors.New("clarify question is required")
		}
		return nil
	}
	if !followUp {
		if len(out.Hypotheses) == 0 {
			return errors.New("at least one hypothesis is required")
		}
		if len(out.Tasks) == 0 {
			return errors.New("at least one task is required")
		}
	}
	// 后续轮：任务可为空（调查完成）；猜想可选

	hypRefs := make(map[string]struct{}, len(out.Hypotheses))
	for i, h := range out.Hypotheses {
		ref := strings.TrimSpace(h.Ref)
		if ref == "" {
			return fmt.Errorf("hypothesis[%d] ref is required", i)
		}
		if _, dup := hypRefs[ref]; dup {
			return fmt.Errorf("hypothesis[%d] ref %q is duplicated", i, ref)
		}
		if strings.TrimSpace(h.Statement) == "" {
			return fmt.Errorf("hypothesis[%d] statement is required", i)
		}
		hypRefs[ref] = struct{}{}
	}

	taskRefs := make(map[string]struct{}, len(out.Tasks))
	for i, task := range out.Tasks {
		ref := strings.TrimSpace(task.Ref)
		if ref == "" {
			return fmt.Errorf("task[%d] ref is required", i)
		}
		if _, dup := taskRefs[ref]; dup {
			return fmt.Errorf("task[%d] ref %q is duplicated", i, ref)
		}
		taskRefs[ref] = struct{}{}
	}

	knownTools := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		knownTools[spec.Name] = struct{}{}
	}

	// 任务引用可指向：问题、节点、边、目标系统编号，以及本轮猜想局部引用
	// 后续轮还可引用前几轮已登记的猜想系统编号
	knownData := make(map[string]struct{}, 1+len(query.Nodes)+len(query.Edges)+len(targets)+len(hypRefs)+len(priorHypothesisIDs))
	if query.ID != "" {
		knownData[query.ID] = struct{}{}
	}
	for _, node := range query.Nodes {
		if node.ID != "" {
			knownData[node.ID] = struct{}{}
		}
	}
	for _, edge := range query.Edges {
		if edge.ID != "" {
			knownData[edge.ID] = struct{}{}
		}
	}
	for _, target := range targets {
		if target.RunID != "" && target.RunID != query.RunID {
			return fmt.Errorf("target %q belongs to run %q, not %q", target.ID, target.RunID, query.RunID)
		}
		if target.ID != "" {
			knownData[target.ID] = struct{}{}
		}
	}
	for ref := range hypRefs {
		knownData[ref] = struct{}{}
	}
	for _, id := range priorHypothesisIDs {
		if id != "" {
			knownData[id] = struct{}{}
		}
	}

	for i, task := range out.Tasks {
		toolName := strings.TrimSpace(task.ToolName)
		if toolName == "" {
			return fmt.Errorf("task[%d] tool_name is required", i)
		}
		if _, ok := knownTools[toolName]; !ok {
			return fmt.Errorf("task[%d] unknown tool %q", i, toolName)
		}
		if len(task.Arguments) == 0 {
			return fmt.Errorf("task[%d] arguments are required", i)
		}
		var probe any
		if err := json.Unmarshal(task.Arguments, &probe); err != nil {
			return fmt.Errorf("task[%d] arguments: %w", i, err)
		}
		if _, ok := probe.(map[string]any); !ok {
			return fmt.Errorf("task[%d] arguments must be a JSON object", i)
		}
		for _, ref := range task.Refs {
			if strings.TrimSpace(ref) == "" {
				return fmt.Errorf("task[%d] refs contains empty value", i)
			}
			if _, ok := knownData[ref]; !ok {
				return fmt.Errorf("task[%d] references unknown data %q", i, ref)
			}
		}
		for _, dep := range task.DependsOn {
			if strings.TrimSpace(dep) == "" {
				return fmt.Errorf("task[%d] depends_on contains empty value", i)
			}
			if _, ok := taskRefs[dep]; !ok {
				return fmt.Errorf("task[%d] depends_on unknown task ref %q", i, dep)
			}
		}
	}

	return nil
}

// 将模型输出回填为绑定当前运行的计划
func (p *LLMPlanner) fillPlan(runID string, out plannerLLMOutput) (Plan, error) {
	now := p.factory.Now()
	hypIDByRef := make(map[string]string, len(out.Hypotheses))
	plan := Plan{
		Hypotheses: make([]core.Hypothesis, 0, len(out.Hypotheses)),
		Tasks:      make([]core.Task, 0, len(out.Tasks)),
	}
	if out.Clarify != nil {
		plan.Clarify = &ClarifyRequest{
			Question: strings.TrimSpace(out.Clarify.Question),
			Options:  append([]string(nil), out.Clarify.Options...),
		}
		return plan, nil
	}

	for _, h := range out.Hypotheses {
		id, err := p.factory.NewID("h")
		if err != nil {
			return Plan{}, fmt.Errorf("create hypothesis ID: %w", err)
		}
		ref := strings.TrimSpace(h.Ref)
		hypIDByRef[ref] = id
		signals := append([]string(nil), h.ExpectedSignals...)
		plan.Hypotheses = append(plan.Hypotheses, core.Hypothesis{
			ID:              id,
			RunID:           runID,
			Statement:       h.Statement,
			Reason:          h.Reason,
			ExpectedSignals: signals,
			CreatedAt:       now,
		})
	}

	taskIDByRef := make(map[string]string, len(out.Tasks))
	// 先分配任务系统编号，再映射依赖列表
	// 两阶段缓冲：已发号但尚未写入计划的任务
	type pendingTask struct {
		// 模型原始任务字段
		out plannerTaskOut
		// 已发放的系统任务编号
		id string
		// 已把猜想局部引用映射为系统编号后的引用列表
		refs []string
	}
	pending := make([]pendingTask, 0, len(out.Tasks))
	for _, task := range out.Tasks {
		id, err := p.factory.NewID("t")
		if err != nil {
			return Plan{}, fmt.Errorf("create task ID: %w", err)
		}
		ref := strings.TrimSpace(task.Ref)
		taskIDByRef[ref] = id

		mappedRefs := make([]string, 0, len(task.Refs))
		for _, r := range task.Refs {
			if sys, ok := hypIDByRef[r]; ok {
				mappedRefs = append(mappedRefs, sys)
				continue
			}
			mappedRefs = append(mappedRefs, r)
		}
		pending = append(pending, pendingTask{out: task, id: id, refs: mappedRefs})
	}

	for _, item := range pending {
		deps := make([]string, 0, len(item.out.DependsOn))
		for _, dep := range item.out.DependsOn {
			deps = append(deps, taskIDByRef[dep])
		}
		plan.Tasks = append(plan.Tasks, core.Task{
			ID:        item.id,
			RunID:     runID,
			Refs:      item.refs,
			ToolName:  strings.TrimSpace(item.out.ToolName),
			Arguments: append(json.RawMessage(nil), item.out.Arguments...),
			Purpose:   item.out.Purpose,
			DependsOn: deps,
		})
	}

	return plan, nil
}
