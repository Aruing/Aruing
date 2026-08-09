// 测试替身包：假角色与假基线塔，仅供测试导入
// 产品二进制与命令入口不得依赖本包
package agenttest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"aruing/internal/agent"
	"aruing/internal/core"
	"aruing/internal/session"
	"aruing/internal/tools"
)

// 可复用的假解析器，始终返回构造时给定的问题模板
type FakeParser struct {
	// 固定问题模板（按次克隆）
	query core.Query
}

// 使用固定问题模板创建可重复使用的假解析器
func NewFakeParser(query core.Query) *FakeParser {
	return &FakeParser{query: cloneQuery(query)}
}

// 校验运行身份和原始问题，返回绑定当前运行编号的问题结构
func (p *FakeParser) Parse(ctx context.Context, run core.Run) (core.Query, error) {
	if ctx == nil {
		return core.Query{}, errors.New("parser requires a context")
	}
	if err := ctx.Err(); err != nil {
		return core.Query{}, fmt.Errorf("parse query: %w", err)
	}
	if p == nil {
		return core.Query{}, errors.New("parser is required")
	}
	if strings.TrimSpace(run.ID) == "" {
		return core.Query{}, errors.New("parser requires a run ID")
	}
	if strings.TrimSpace(run.Question) == "" {
		return core.Query{}, errors.New("parser requires a question")
	}
	if strings.TrimSpace(p.query.ID) == "" {
		return core.Query{}, errors.New("parser requires a query ID")
	}

	query := cloneQuery(p.query)
	query.RunID = run.ID
	return query, nil
}

// 可复用的假定位器，首轮提交构造时给定的目标模板
type FakeResolver struct {
	// 固定身份模板（按次克隆）
	templates []core.Target
}

// 使用固定身份模板创建可重复使用的假定位器
func NewFakeResolver(templates []core.Target) *FakeResolver {
	return &FakeResolver{templates: cloneTargets(templates)}
}

// 首轮直接提交基于当前问题节点的目标
func (r *FakeResolver) Next(ctx context.Context, state agent.ResolveState) (agent.ResolveAction, error) {
	if err := ctx.Err(); err != nil {
		return agent.ResolveAction{}, fmt.Errorf("resolve next: %w", err)
	}
	if r == nil {
		return agent.ResolveAction{}, fmt.Errorf("resolve next: resolver is required")
	}
	if len(state.Query.Nodes) == 0 {
		return agent.ResolveAction{
			Action: agent.ResolveActionFail,
			Reason: "query has no nodes",
			Error:  "query has no nodes to resolve",
		}, nil
	}

	targets := make([]agent.ProposedTarget, 0, len(state.Query.Nodes))
	for index, node := range state.Query.Nodes {
		if node.ID == "" {
			return agent.ResolveAction{
				Action: agent.ResolveActionFail,
				Reason: "node missing id",
				Error:  fmt.Sprintf("query node at index %d has empty id", index),
			}, nil
		}
		proposed := agent.ProposedTarget{
			NodeID: node.ID,
			Type:   "resource",
			Attrs:  map[string]string{},
		}
		if index < len(r.templates) {
			tmpl := r.templates[index]
			if tmpl.Type != "" {
				proposed.Type = tmpl.Type
			}
			proposed.Attrs = maps.Clone(tmpl.Attrs)
			if proposed.Attrs == nil {
				proposed.Attrs = map[string]string{}
			}
		}
		targets = append(targets, proposed)
	}

	return agent.ResolveAction{
		Action:  agent.ResolveActionSubmitTargets,
		Reason:  "fake resolver submits targets from query nodes",
		Targets: targets,
	}, nil
}

// 可复用的假规划器，始终返回构造时给定的计划模板
type FakePlanner struct {
	// 固定计划模板（按次克隆）
	plan agent.Plan
}

// 使用固定计划模板创建可重复使用的假规划器
func NewFakePlanner(plan agent.Plan) *FakePlanner {
	return &FakePlanner{plan: clonePlan(plan)}
}

// 校验任务引用并返回绑定当前运行编号的独立计划
func (p *FakePlanner) Plan(ctx context.Context, state agent.PlanState) (agent.Plan, error) {
	if err := ctx.Err(); err != nil {
		return agent.Plan{}, fmt.Errorf("plan tasks: %w", err)
	}

	query, targets := state.Query, state.Targets
	plan := clonePlan(p.plan)
	for index := range plan.Hypotheses {
		plan.Hypotheses[index].RunID = query.RunID
	}

	knownRefs := make(map[string]struct{}, 1+len(query.Nodes)+len(query.Edges)+len(targets)+len(plan.Hypotheses))
	if query.ID != "" {
		knownRefs[query.ID] = struct{}{}
	}
	for _, node := range query.Nodes {
		if node.ID != "" {
			knownRefs[node.ID] = struct{}{}
		}
	}
	for _, edge := range query.Edges {
		if edge.ID != "" {
			knownRefs[edge.ID] = struct{}{}
		}
	}
	for _, target := range targets {
		if target.RunID != query.RunID {
			return agent.Plan{}, fmt.Errorf("target %q belongs to run %q, not %q", target.ID, target.RunID, query.RunID)
		}
		if target.ID != "" {
			knownRefs[target.ID] = struct{}{}
		}
	}
	for _, hypothesis := range plan.Hypotheses {
		if hypothesis.ID != "" {
			knownRefs[hypothesis.ID] = struct{}{}
		}
	}

	for index := range plan.Tasks {
		task := &plan.Tasks[index]
		for _, ref := range task.Refs {
			if _, exists := knownRefs[ref]; !exists {
				return agent.Plan{}, fmt.Errorf("task %q references unknown data %q", task.ID, ref)
			}
		}
		task.RunID = query.RunID
	}
	return plan, nil
}

// 可复用的假判断器，始终返回构造时给定的结论模板
type FakeVerifier struct {
	// 固定判断结果模板（按次克隆）
	verdicts []core.Verdict
}

// 使用固定判断模板创建可重复使用的假判断器
func NewFakeVerifier(verdicts []core.Verdict) *FakeVerifier {
	return &FakeVerifier{verdicts: cloneVerdicts(verdicts)}
}

// 根据任务引用找到每个猜想的实际证据
func (v *FakeVerifier) Verify(
	ctx context.Context,
	_ core.Query,
	hypotheses []core.Hypothesis,
	tasks []core.Task,
	evidence []core.Evidence,
) ([]core.Verdict, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("verify evidence: %w", err)
	}

	hypothesesByID := make(map[string]core.Hypothesis, len(hypotheses))
	for _, hypothesis := range hypotheses {
		if hypothesis.ID != "" {
			hypothesesByID[hypothesis.ID] = hypothesis
		}
	}
	evidenceByTaskID := make(map[string]core.Evidence, len(evidence))
	for _, item := range evidence {
		if item.TaskID != "" {
			evidenceByTaskID[item.TaskID] = item
		}
	}

	verdicts := cloneVerdicts(v.verdicts)
	for index := range verdicts {
		verdict := &verdicts[index]
		hypothesis, exists := hypothesesByID[verdict.HypothesisID]
		if !exists {
			return nil, fmt.Errorf("verdict %q references unknown hypothesis %q", verdict.ID, verdict.HypothesisID)
		}

		verdict.EvidenceIDs = verdict.EvidenceIDs[:0]
		for _, task := range tasks {
			if !slices.Contains(task.Refs, hypothesis.ID) {
				continue
			}
			if task.RunID != hypothesis.RunID {
				return nil, fmt.Errorf("task %q belongs to run %q, not %q", task.ID, task.RunID, hypothesis.RunID)
			}
			item, exists := evidenceByTaskID[task.ID]
			if !exists {
				return nil, fmt.Errorf("task %q has no evidence", task.ID)
			}
			if item.ID == "" {
				return nil, fmt.Errorf("task %q produced evidence without an ID", task.ID)
			}
			if item.RunID != hypothesis.RunID {
				return nil, fmt.Errorf("evidence %q belongs to run %q, not %q", item.ID, item.RunID, hypothesis.RunID)
			}
			verdict.EvidenceIDs = append(verdict.EvidenceIDs, item.ID)
		}
		if len(verdict.EvidenceIDs) == 0 {
			return nil, fmt.Errorf("verdict %q requires evidence", verdict.ID)
		}
		verdict.RunID = hypothesis.RunID
	}
	return verdicts, nil
}

// 可复用的假报告器，始终返回构造时给定的报告模板
type FakeReporter struct {
	// 固定报告模板（按次克隆）
	report core.Report
}

// 使用固定报告模板创建可重复使用的假报告器
func NewFakeReporter(report core.Report) *FakeReporter {
	return &FakeReporter{report: cloneReport(report)}
}

// 校验报告结论与已有判断和证据一致
func (r *FakeReporter) Report(
	ctx context.Context,
	run core.Run,
	verdicts []core.Verdict,
	evidence []core.Evidence,
) (core.Report, error) {
	if err := ctx.Err(); err != nil {
		return core.Report{}, fmt.Errorf("build report: %w", err)
	}

	verdictsByHypothesis := make(map[string]core.Verdict, len(verdicts))
	for _, verdict := range verdicts {
		if verdict.RunID != run.ID {
			return core.Report{}, fmt.Errorf("verdict %q belongs to run %q, not %q", verdict.ID, verdict.RunID, run.ID)
		}
		if verdict.HypothesisID != "" {
			verdictsByHypothesis[verdict.HypothesisID] = verdict
		}
	}
	evidenceByID := make(map[string]core.Evidence, len(evidence))
	for _, item := range evidence {
		if item.ID != "" {
			evidenceByID[item.ID] = item
		}
	}

	report := cloneReport(r.report)
	for index := range report.Conclusions {
		conclusion := &report.Conclusions[index]
		verdict, exists := verdictsByHypothesis[conclusion.HypothesisID]
		if !exists {
			return core.Report{}, fmt.Errorf("report references unknown hypothesis verdict %q", conclusion.HypothesisID)
		}
		if conclusion.Result != verdict.Result {
			return core.Report{}, fmt.Errorf("report result for hypothesis %q does not match verdict", conclusion.HypothesisID)
		}
		if len(verdict.EvidenceIDs) == 0 {
			return core.Report{}, fmt.Errorf("report conclusion for hypothesis %q requires evidence", conclusion.HypothesisID)
		}
		for _, evidenceID := range verdict.EvidenceIDs {
			item, exists := evidenceByID[evidenceID]
			if !exists {
				return core.Report{}, fmt.Errorf("report references unknown evidence %q", evidenceID)
			}
			if item.RunID != run.ID {
				return core.Report{}, fmt.Errorf("evidence %q belongs to run %q, not %q", item.ID, item.RunID, run.ID)
			}
		}
		conclusion.EvidenceIDs = slices.Clone(verdict.EvidenceIDs)
	}
	report.RunID = run.ID
	return report, nil
}

// 假基线塔在调用工具时使用的工具提议
type ToolCall struct {
	// 工具名，须已在调度器中注册
	ToolName string
	// 调用参数；空则按空对象发送
	Arguments json.RawMessage
	// 调用目的说明
	Purpose string
}

// 假基线塔默认的基线工具轮次上限
const defaultBaselineMaxToolRounds = 12

// 测试用基线塔：由决定函数选择动作，不调用大模型
type FakeTowerResponder struct {
	// 发号器；调用工具与升格需要
	Factory *core.Factory
	// 正式诊断执行器；升格需要
	Executor session.RunExecutor
	// 诊断账本；升格成功后写入
	Ledger session.RunLedger
	// 工具调度器；调用工具需要
	Dispatcher *tools.Dispatcher
	// 决定本轮动作；返回动作名、回复正文、升格问题
	Decide func(in session.RespondInput) (action, content, question string)
	// 调用工具时执行的工具提议
	CallTool ToolCall
	// 基线工具轮次上限；非正数时用默认值
	BaselineMaxToolRounds int
}

// 按决定在回复、调工具与升格之间分支
func (f *FakeTowerResponder) Respond(ctx context.Context, in session.RespondInput) (session.RespondOutput, error) {
	if err := ctx.Err(); err != nil {
		return session.RespondOutput{}, fmt.Errorf("fake tower: %w", err)
	}
	if f == nil {
		return session.RespondOutput{}, fmt.Errorf("fake tower is nil")
	}

	maxRounds := f.BaselineMaxToolRounds
	if maxRounds <= 0 {
		maxRounds = defaultBaselineMaxToolRounds
	}
	toolRounds := 0

	for {
		if err := ctx.Err(); err != nil {
			return session.RespondOutput{}, fmt.Errorf("fake tower: %w", err)
		}

		action, content, question := "reply", "基线："+in.UserText, ""
		if f.Decide != nil {
			action, content, question = f.Decide(in)
		}
		action = strings.TrimSpace(strings.ToLower(action))

		switch action {
		case "reply":
			if strings.TrimSpace(content) == "" {
				content = "基线：" + in.UserText
			}
			return session.RespondOutput{
				Content: content,
				Mode:    session.ModeBaseline,
			}, nil

		case "call_tool":
			if f.Dispatcher == nil {
				return session.RespondOutput{}, fmt.Errorf("fake tower: call_tool requires dispatcher")
			}
			if toolRounds >= maxRounds {
				q := strings.TrimSpace(in.UserText)
				return session.Escalate(ctx, f.Factory, f.Executor, f.Ledger, in.SessionID, q)
			}
			if err := f.executeCallTool(ctx); err != nil {
				return session.RespondOutput{}, err
			}
			toolRounds++

		case "escalate":
			q := strings.TrimSpace(question)
			if q == "" {
				q = in.UserText
			}
			return session.Escalate(ctx, f.Factory, f.Executor, f.Ledger, in.SessionID, q)

		default:
			return session.RespondOutput{}, fmt.Errorf("fake tower: unknown action %q", action)
		}
	}
}

func (f *FakeTowerResponder) executeCallTool(ctx context.Context) error {
	if f.Factory == nil {
		return fmt.Errorf("fake tower: factory is required for call_tool")
	}
	taskID, err := f.Factory.NewID("t")
	if err != nil {
		return fmt.Errorf("fake tower: create task id: %w", err)
	}
	args := f.CallTool.Arguments
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	toolName := f.CallTool.ToolName
	if toolName == "" {
		return fmt.Errorf("fake tower: call_tool requires tool name")
	}
	task := core.Task{
		ID:        taskID,
		RunID:     "",
		ToolName:  toolName,
		Arguments: args,
		Purpose:   f.CallTool.Purpose,
	}
	_, execErr := f.Dispatcher.Execute(ctx, task)
	if execErr != nil && ctx.Err() != nil {
		return fmt.Errorf("fake tower: execute tool: %w", ctx.Err())
	}
	return nil
}

func cloneQuery(query core.Query) core.Query {
	cloned := query
	cloned.Nodes = slices.Clone(query.Nodes)
	for index := range cloned.Nodes {
		cloned.Nodes[index].Attrs = maps.Clone(query.Nodes[index].Attrs)
	}
	cloned.Edges = slices.Clone(query.Edges)
	for index := range cloned.Edges {
		cloned.Edges[index].Attrs = maps.Clone(query.Edges[index].Attrs)
	}
	if query.TimeRange != nil {
		timeRange := *query.TimeRange
		if query.TimeRange.Start != nil {
			start := *query.TimeRange.Start
			timeRange.Start = &start
		}
		if query.TimeRange.End != nil {
			end := *query.TimeRange.End
			timeRange.End = &end
		}
		cloned.TimeRange = &timeRange
	}
	return cloned
}

func cloneTargets(targets []core.Target) []core.Target {
	cloned := slices.Clone(targets)
	for index := range cloned {
		cloned[index].Attrs = maps.Clone(targets[index].Attrs)
		cloned[index].EvidenceIDs = slices.Clone(targets[index].EvidenceIDs)
	}
	return cloned
}

func clonePlan(plan agent.Plan) agent.Plan {
	cloned := agent.Plan{
		Hypotheses: slices.Clone(plan.Hypotheses),
		Tasks:      slices.Clone(plan.Tasks),
	}
	for index := range cloned.Hypotheses {
		cloned.Hypotheses[index].ExpectedSignals = slices.Clone(plan.Hypotheses[index].ExpectedSignals)
	}
	for index := range cloned.Tasks {
		cloned.Tasks[index].Refs = slices.Clone(plan.Tasks[index].Refs)
		cloned.Tasks[index].Arguments = slices.Clone(plan.Tasks[index].Arguments)
		cloned.Tasks[index].DependsOn = slices.Clone(plan.Tasks[index].DependsOn)
	}
	return cloned
}

func cloneVerdicts(verdicts []core.Verdict) []core.Verdict {
	cloned := slices.Clone(verdicts)
	for index := range cloned {
		cloned[index].EvidenceIDs = slices.Clone(verdicts[index].EvidenceIDs)
	}
	return cloned
}

func cloneReport(report core.Report) core.Report {
	cloned := report
	cloned.Conclusions = slices.Clone(report.Conclusions)
	for index := range cloned.Conclusions {
		cloned.Conclusions[index].EvidenceIDs = slices.Clone(report.Conclusions[index].EvidenceIDs)
	}
	cloned.Suggestions = slices.Clone(report.Suggestions)
	return cloned
}
