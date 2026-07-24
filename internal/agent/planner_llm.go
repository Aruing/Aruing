// 真实规划器把 Query 与已确认 Target 交给大模型，回填系统编号后产出 Plan
//
// 与 FakePlanner 共享同一 Plan 边界：一次调用返回完整猜想与任务，不在角色内多轮调工具
// 工具规格来自 Registry.Specs 注入，禁止在 prompt 手写第二份工具清单
// Hypothesis / Task 的系统编号与创建时间由本模块经 Factory 发放（对齐 LLMParser）
package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"aruing/internal/core"
	"aruing/internal/llm"
	"aruing/internal/tools"
)

//go:embed prompts/planner.md
var plannerPrompt string

// 规划业务级重试次数：JSON 合法但语义违规时重新请求
const maxPlanAttempts = 3

// 用大模型生成猜想与取证任务的规划器
//
// 持有不可变依赖（客户端、工厂、工具规格快照、prompt），可被多次运行复用
// 不持有跨运行可变状态；每次 Plan 独立发 GenerateJSON
type LLMPlanner struct {
	client  llm.Client
	factory *core.Factory
	specs   []tools.ToolSpec
	prompt  string
}

// 创建基于大模型的规划器
// specs 应为 Registry.Specs() 快照；空列表时仍可构造，但任务 tool_name 将无法通过校验
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
// 模型若返回结构合法但语义违规的输出（未知工具、坏 ref 等），在业务级重试内重新请求；
// 重试 maxPlanAttempts 次仍不合规则返回 ErrLLMOutputInconsistent
// 首轮 state.Evidence/Verdicts 为 nil，payload 不含这两段，模型输入与 beta2 逐字一致
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

// 将嵌入 prompt 中的工具规格占位符替换为 JSON 文本
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

// 序列化规划输入：Query、Targets，以及非空时的历史 Evidence 与 Verdicts
// evidence/verdicts 用 omitempty，首轮为 nil 时不出现在 JSON，保证模型输入与 beta2 一致
func buildPlannerUserPayload(state PlanState) (string, error) {
	type payload struct {
		Query    core.Query      `json:"query"`
		Targets  []core.Target   `json:"targets"`
		Evidence []core.Evidence `json:"evidence,omitempty"`
		Verdicts []core.Verdict  `json:"verdicts,omitempty"`
	}
	raw, err := json.MarshalIndent(payload(state), "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// 模型输出的中间结构，只含内容与局部 ref，不含系统编号或时间
type plannerLLMOutput struct {
	Hypotheses []plannerHypothesisOut `json:"hypotheses"`
	Tasks      []plannerTaskOut       `json:"tasks"`
}

type plannerHypothesisOut struct {
	Ref             string   `json:"ref"`
	Statement       string   `json:"statement"`
	Reason          string   `json:"reason"`
	ExpectedSignals []string `json:"expected_signals"`
}

type plannerTaskOut struct {
	Ref       string          `json:"ref"`
	ToolName  string          `json:"tool_name"`
	Arguments json.RawMessage `json:"arguments"`
	Purpose   string          `json:"purpose"`
	Refs      []string        `json:"refs"`
	DependsOn []string        `json:"depends_on"`
}

// 校验模型输出满足编排与取证的最小契约
// 首轮与后续轮的校验规则不同：
//   - 首轮（followUp=false）：猜想与任务均至少 1 个
//   - 后续轮（followUp=true）：任务可为空（=规划器宣布查完），猜想可选
//
// priorHypothesisIDs 为前几轮已登记的猜想系统编号，允许后续轮任务引用现有猜想
func validatePlannerOutput(out plannerLLMOutput, query core.Query, targets []core.Target, specs []tools.ToolSpec, followUp bool, priorHypothesisIDs []string) error {
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

	// 任务 refs 可引用：query / node / edge / target 系统编号 + 本轮 hypothesis 局部 ref
	// 后续轮还可引用前几轮已登记的猜想系统编号（priorHypothesisIDs）
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

// 将模型输出回填为绑定当前运行的 Plan
func (p *LLMPlanner) fillPlan(runID string, out plannerLLMOutput) (Plan, error) {
	now := p.factory.Now()
	hypIDByRef := make(map[string]string, len(out.Hypotheses))
	plan := Plan{
		Hypotheses: make([]core.Hypothesis, 0, len(out.Hypotheses)),
		Tasks:      make([]core.Task, 0, len(out.Tasks)),
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
	// 先分配 task 系统编号，再映射 depends_on
	type pendingTask struct {
		out  plannerTaskOut
		id   string
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
