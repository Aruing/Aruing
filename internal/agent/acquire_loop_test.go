package agent_test

// 取证决策循环（ours 路径）的编排级测试：假实现端到端、三出口、问用户挂起恢复、
// 归类优先级与方法分派防御；B1 回归由既有测试覆盖（零值默认即 B1）

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/agent"
	"github.com/Aruing/Aruing/internal/agent/agenttest"
	"github.com/Aruing/Aruing/internal/core"
)

// 按 argv 关键词脚本化返回证据摘要的假执行器：驱动机械归类与强度路径
type scriptedExecutor struct {
	// argv 含关键词 → 返回该摘要（首个命中生效）
	script map[string]string
}

func (e *scriptedExecutor) Execute(ctx context.Context, task core.Task) (*core.Evidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	summary := "无脚本的默认观察"
	for keyword, text := range e.script {
		if containsArgv(task.Arguments, keyword) {
			summary = text
			break
		}
	}
	return &core.Evidence{
		RunID:       task.RunID,
		TaskID:      task.ID,
		ToolName:    task.ToolName,
		CommandView: "kubectl " + argvOf(task.Arguments),
		Summary:     summary,
	}, nil
}

// 参数 JSON 里是否含给定 argv 关键词（机械包含即可，测试脚本用）
func containsArgv(args []byte, keyword string) bool {
	return strings.Contains(strings.ToLower(string(args)), strings.ToLower(keyword))
}

// 取出参数 JSON 的 argv 字符串拼合（命令视图用）
func argvOf(args []byte) string {
	var parsed struct {
		Argv []string `json:"argv"`
	}
	_ = json.Unmarshal(args, &parsed)
	return strings.Join(parsed.Argv, " ")
}

// 测试内固定参数 JSON
func mustJSON(s string) json.RawMessage {
	return json.RawMessage(s)
}

// 记录型验证器：捕获最后一次 Verify 看到的假设（语句与置信度），并统计强度判定调用次数
type recordingVerifier struct {
	*agenttest.FakeVerifier
	// 最后一次 Verify 输入假设的置信度快照（后验回写断言用）
	SeenConfidence []float64
	// 最后一次 Verify 输入假设的语句快照（重规划保留旧假设断言用）
	SeenStatements []string
	// JudgeStrength 调用次数（机械归类优先断言用）
	StrengthCalls int
}

func (v *recordingVerifier) Verify(
	ctx context.Context,
	query core.Query,
	hypotheses []core.Hypothesis,
	tasks []core.Task,
	evidence []core.Evidence,
) ([]core.Verdict, error) {
	v.SeenConfidence = v.SeenConfidence[:0]
	v.SeenStatements = v.SeenStatements[:0]
	for _, h := range hypotheses {
		v.SeenConfidence = append(v.SeenConfidence, h.Confidence)
		v.SeenStatements = append(v.SeenStatements, h.Statement)
	}
	return v.FakeVerifier.Verify(ctx, query, hypotheses, tasks, evidence)
}

func (v *recordingVerifier) JudgeStrength(
	ctx context.Context,
	evidence core.Evidence,
	hypotheses []core.Hypothesis,
) ([]agent.StrengthJudgement, error) {
	v.StrengthCalls++
	return v.FakeVerifier.JudgeStrength(ctx, evidence, hypotheses)
}

// 组装走 ours 路径的编排器（定位直提目标、报告固定模板）
func newAcquireOrchestrator(
	t *testing.T,
	decision *agenttest.FakeDecisionPlanner,
	verifier *recordingVerifier,
	executor *scriptedExecutor,
) *agent.Orchestrator {
	t.Helper()
	parser := agenttest.NewFakeParser(core.Query{
		ID:    "query_1",
		Goal:  "demo-api 不可达",
		Nodes: []core.Node{{ID: "node_1", Type: "resource", Text: "demo-api"}},
	})
	resolver := agenttest.NewFakeResolver([]core.Target{})
	planner := agenttest.NewFakePlanner(agent.Plan{})
	reporter := agenttest.NewFakeReporter(core.Report{
		Title:   "demo-api 诊断",
		Summary: "测试报告",
	})
	orch := agent.NewOrchestrator(parser, resolver, planner, executor, verifier, reporter, core.NewFactory())
	orch.SetAcquireMethod(agent.AcquireMethodOurs)
	orch.SetDecisionPlanner(decision)
	orch.SetInvestigateMaxRounds(6)
	return orch
}

// 双假设决策模板：h1 Pod 崩溃 / h2 选择器配错；两个强区分动作
func twoHypothesisDecision() agent.PlanDecision {
	return agent.PlanDecision{
		Hypotheses: []core.Hypothesis{
			{ID: "h_1", Statement: "Pod 崩溃", Confidence: 0.5},
			{ID: "h_2", Statement: "选择器配错", Confidence: 0.5},
		},
		Actions: []agent.ActionProposal{
			{
				Name:     "check-pods",
				Argv:     []string{"get", "pods"},
				Purpose:  "看 Pod 状态",
				Cost:     1,
				Outcomes: []string{"crash", "running"},
				Matrix:   [][]float64{{0.8, 0.2}, {0.1, 0.9}},
			},
			{
				Name:     "check-events",
				Argv:     []string{"get", "events"},
				Purpose:  "看事件",
				Cost:     1,
				Outcomes: []string{"backoff", "none"},
				Matrix:   [][]float64{{0.85, 0.15}, {0.2, 0.8}},
			},
		},
	}
}

// 端到端 supported：两次观测收敛（0.888 → 0.971 ≥ P*），置信度回写，正式判决
func TestAcquireLoopSupported(t *testing.T) {
	decision := agenttest.NewFakeDecisionPlanner(twoHypothesisDecision())
	verifier := &recordingVerifier{FakeVerifier: agenttest.NewFakeVerifier([]core.Verdict{{
		ID: "v_1", HypothesisID: "h_1", Result: core.VerdictSupported, Reason: "证据支持",
	}})}
	executor := &scriptedExecutor{script: map[string]string{
		"pods":   "pod CrashLoopBackOff restarts 8",
		"events": "事件 BackOff 反复出现",
	}}
	orch := newAcquireOrchestrator(t, decision, verifier, executor)

	outcome, err := orch.Execute(context.Background(), core.Run{ID: "run_1", Question: "demo-api 不可达"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if outcome.Suspension != nil || outcome.Report == nil {
		t.Fatalf("expected report, got suspension=%v", outcome.Suspension)
	}
	// 两次动作都执行且进证据链（动作经 Dispatcher 契约由编排登记任务）
	if len(outcome.Evidence) < 2 {
		t.Fatalf("evidence = %d, want >= 2", len(outcome.Evidence))
	}
	stats := orch.LastRunStats()
	if stats.AcquireExit != "supported" {
		t.Errorf("exit = %q, want supported", stats.AcquireExit)
	}
	// 后验回写：h1 收敛值 ≈ 0.971（第一次 0.888 不足以停，第二次过阈）
	if len(verifier.SeenConfidence) != 2 || verifier.SeenConfidence[0] < 0.95 {
		t.Errorf("h1 posterior = %v, want >= 0.95", verifier.SeenConfidence)
	}
	// 机械归类全程命中，不应触发富文本强度路径
	if verifier.StrengthCalls != 0 {
		t.Errorf("strength calls = %d, want 0 (mechanical classify should hit)", verifier.StrengthCalls)
	}
}

// 归类不可用走强度路径：零命中动作 + 关键词 (d,s) 表更新信念
func TestAcquireLoopStrengthFallback(t *testing.T) {
	decision := agent.PlanDecision{
		Hypotheses: []core.Hypothesis{
			{ID: "h_1", Statement: "Pod 崩溃", Confidence: 0.5},
			{ID: "h_2", Statement: "选择器配错", Confidence: 0.5},
		},
		Actions: []agent.ActionProposal{{
			// 结果类别刻意的非命中标签：观测文本不含
			Name:     "probe",
			Argv:     []string{"logs", "demo-api"},
			Purpose:  "看日志",
			Cost:     1,
			Outcomes: []string{"gamma", "delta"},
			Matrix:   [][]float64{{0.6, 0.4}, {0.4, 0.6}},
		}},
	}
	fake := agenttest.NewFakeDecisionPlanner(decision)
	fk := agenttest.NewFakeVerifier([]core.Verdict{{
		ID: "v_1", HypothesisID: "h_1", Result: core.VerdictInsufficient, Reason: "证据不足",
	}})
	fk.StrengthRules = []agenttest.StrengthRule{
		{HypothesisID: "h_1", Keyword: "BackOff", Direction: 1, Strength: 0.9},
		{HypothesisID: "h_2", Keyword: "BackOff", Direction: -1, Strength: 0.9},
	}
	verifier := &recordingVerifier{FakeVerifier: fk}
	executor := &scriptedExecutor{script: map[string]string{
		"logs": "日志出现 BackOff 循环",
	}}
	orch := newAcquireOrchestrator(t, fake, verifier, executor)

	outcome, err := orch.Execute(context.Background(), core.Run{ID: "run_1", Question: "q"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if outcome.Report == nil {
		t.Fatalf("expected report")
	}
	// 强度路径被调用；ℓ=2^(3·0.9)≈6.5 强抬 h1 → 后验过阈 supported
	if verifier.StrengthCalls == 0 {
		t.Fatal("strength path not taken for unclassifiable outcome")
	}
	if got := orch.LastRunStats().AcquireExit; got != "supported" {
		t.Errorf("exit = %q, want supported via strength update", got)
	}
}

// refuted 出口：全灭证据压垮假设空间 → abduction 重规划 → 新假设收敛
func TestAcquireLoopRefutedReplans(t *testing.T) {
	first := agent.PlanDecision{
		Hypotheses: []core.Hypothesis{
			{ID: "h_1", Statement: "方向一", Confidence: 0.5},
			{ID: "h_2", Statement: "方向二", Confidence: 0.5},
		},
		Actions: []agent.ActionProposal{
			{Name: "probe-a", Argv: []string{"get", "aaa"}, Purpose: "探针", Cost: 1,
				Outcomes: []string{"gamma", "delta"}, Matrix: [][]float64{{0.6, 0.4}, {0.4, 0.6}}},
			{Name: "probe-b", Argv: []string{"get", "bbb"}, Purpose: "探针", Cost: 1,
				Outcomes: []string{"gamma", "delta"}, Matrix: [][]float64{{0.6, 0.4}, {0.4, 0.6}}},
		},
	}
	second := agent.PlanDecision{
		Hypotheses: []core.Hypothesis{
			{ID: "h_3", Statement: "新方向", Confidence: 0.6},
			{ID: "h_1", Statement: "方向一", Confidence: 0.1},
		},
		Actions: []agent.ActionProposal{
			{Name: "probe-c", Argv: []string{"get", "ccc"}, Purpose: "新探针", Cost: 1,
				Outcomes: []string{"crash", "running"}, Matrix: [][]float64{{0.9, 0.1}, {0.1, 0.9}}},
		},
	}
	decision := agenttest.NewFakeDecisionPlanner(first)
	decision.WithNextDecision(second)
	fk := agenttest.NewFakeVerifier([]core.Verdict{{
		ID: "v_1", HypothesisID: "h_3", Result: core.VerdictSupported, Reason: "新方向成立",
	}})
	fk.StrengthRules = []agenttest.StrengthRule{
		{HypothesisID: "h_1", Keyword: "fatal", Direction: -1, Strength: 0.9},
		{HypothesisID: "h_2", Keyword: "fatal", Direction: -1, Strength: 0.9},
		{HypothesisID: "h_3", Keyword: "fatal", Direction: -1, Strength: 0.9},
	}
	verifier := &recordingVerifier{FakeVerifier: fk}
	executor := &scriptedExecutor{script: map[string]string{
		"aaa": "fatal 错误一",
		"bbb": "fatal 错误二",
		"ccc": "CrashLoopBackOff 出现",
	}}
	orch := newAcquireOrchestrator(t, decision, verifier, executor)

	outcome, err := orch.Execute(context.Background(), core.Run{ID: "run_1", Question: "q"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if outcome.Report == nil {
		t.Fatalf("expected report after replan")
	}
	// 重规划确实发生：首轮 2 次全灭观测压垮质量 → 第二计划接管 → 新假设收敛
	if got := orch.LastRunStats().AcquireExit; got != "supported" {
		t.Errorf("exit = %q, want supported after replan", got)
	}
	// 旧假设保留在最终假设空间（保留压低不丢弃）
	seen := strings.Join(verifier.SeenStatements, ";")
	if !strings.Contains(seen, "方向一") {
		t.Errorf("old hypothesis dropped on replan: seen = %q", seen)
	}
}

// 信息平台出口：零区分力动作集 + 弱强度更新 → max EIG < τ → insufficient 带缺口
func TestAcquireLoopInsufficientPlatform(t *testing.T) {
	decision := agent.PlanDecision{
		Hypotheses: []core.Hypothesis{
			{ID: "h_1", Statement: "方向一", Confidence: 0.5},
			{ID: "h_2", Statement: "方向二", Confidence: 0.5},
		},
		Actions: []agent.ActionProposal{
			// 两个零区分力动作：动作池非空且 max EIG ≈ 0，触发信息平台
			{Name: "flat-1", Argv: []string{"get", "xxx"}, Purpose: "无区分力动作", Cost: 1,
				Outcomes: []string{"gamma", "delta"},
				Matrix:   [][]float64{{0.5, 0.5}, {0.5, 0.5}}},
			{Name: "flat-2", Argv: []string{"get", "yyy"}, Purpose: "无区分力动作", Cost: 1,
				Outcomes: []string{"gamma", "delta"},
				Matrix:   [][]float64{{0.5, 0.5}, {0.5, 0.5}}},
		},
	}
	fk := agenttest.NewFakeVerifier([]core.Verdict{{
		ID: "v_1", HypothesisID: "h_1", Result: core.VerdictInsufficient, Reason: "证据不足",
	}})
	fk.StrengthRules = []agenttest.StrengthRule{
		{HypothesisID: "h_1", Keyword: "fatal", Direction: 1, Strength: 0.05},
		{HypothesisID: "h_2", Keyword: "fatal", Direction: 1, Strength: 0.05},
	}
	verifier := &recordingVerifier{FakeVerifier: fk}
	executor := &scriptedExecutor{script: map[string]string{"xxx": "fatal 弱信号", "yyy": "fatal 弱信号"}}
	orch := newAcquireOrchestrator(t, agenttest.NewFakeDecisionPlanner(decision), verifier, executor)

	outcome, err := orch.Execute(context.Background(), core.Run{ID: "run_1", Question: "q"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if outcome.Report == nil {
		t.Fatalf("insufficient 仍应出正式报告（Verdict insufficient）")
	}
	stats := orch.LastRunStats()
	if stats.AcquireExit != "insufficient" {
		t.Errorf("exit = %q, want insufficient", stats.AcquireExit)
	}
	if !strings.Contains(stats.AcquireGap, "信息平台") {
		t.Errorf("gap = %q, want 信息平台", stats.AcquireGap)
	}
}

// 预算尽出口：弱更新永不收敛，轮次耗尽后 insufficient 带缺口（#18 明确失败）
func TestAcquireLoopInsufficientBudget(t *testing.T) {
	decision := agent.PlanDecision{
		Hypotheses: []core.Hypothesis{
			{ID: "h_1", Statement: "方向一", Confidence: 0.5},
			{ID: "h_2", Statement: "方向二", Confidence: 0.5},
		},
		Actions: []agent.ActionProposal{
			{Name: "probe-a", Argv: []string{"get", "aaa"}, Purpose: "探针", Cost: 1,
				Outcomes: []string{"crash", "running"}, Matrix: [][]float64{{0.8, 0.2}, {0.1, 0.9}}},
			{Name: "probe-b", Argv: []string{"get", "bbb"}, Purpose: "探针", Cost: 1,
				Outcomes: []string{"crash", "running"}, Matrix: [][]float64{{0.8, 0.2}, {0.1, 0.9}}},
			{Name: "probe-c", Argv: []string{"get", "ccc"}, Purpose: "探针", Cost: 1,
				Outcomes: []string{"crash", "running"}, Matrix: [][]float64{{0.8, 0.2}, {0.1, 0.9}}},
		},
	}
	fk := agenttest.NewFakeVerifier([]core.Verdict{{
		ID: "v_1", HypothesisID: "h_1", Result: core.VerdictInsufficient, Reason: "证据不足",
	}})
	verifier := &recordingVerifier{FakeVerifier: fk}
	// 归类不命中（文本无 crash/running）→ 强度路径；无规则 → 全无关，信念不动
	executor := &scriptedExecutor{script: map[string]string{
		"aaa": "无信号一", "bbb": "无信号二", "ccc": "无信号三",
	}}
	orch := newAcquireOrchestrator(t, agenttest.NewFakeDecisionPlanner(decision), verifier, executor)
	orch.SetInvestigateMaxRounds(3)

	outcome, err := orch.Execute(context.Background(), core.Run{ID: "run_1", Question: "q"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if outcome.Report == nil {
		t.Fatalf("预算尽仍应出正式报告")
	}
	stats := orch.LastRunStats()
	if stats.AcquireExit != "insufficient" || !strings.Contains(stats.AcquireGap, "预算尽") {
		t.Errorf("stats = %+v, want insufficient + 预算尽", stats)
	}
}

// 重规划 50/50 拆分（pr-agent 第 1 轮采纳钉板，纯函数直测）：
// 新假设先验和 ≠ 1 时两侧仍各占一半，不被原始先验幅度污染
func TestReplanAcquireSplitsMass(t *testing.T) {
	old := []core.Hypothesis{
		{ID: "h_1", Statement: "旧方向一"},
		{ID: "h_2", Statement: "旧方向二"},
	}
	post := []float64{0.8, 0.2}
	// 先验和 0.7（非归一）：修复前新侧只分到 0.35、旧侧 0.5
	fresh := []core.Hypothesis{
		{ID: "h_3", Statement: "新方向", Confidence: 0.6},
		{ID: "h_4", Statement: "另一个新方向", Confidence: 0.1},
	}

	merged, weights := agent.MergeReplanWeights(old, post, fresh)
	if len(merged) != 4 {
		t.Fatalf("merged = %d, want 4", len(merged))
	}
	var oldMass, newMass float64
	for i, w := range weights {
		if strings.HasPrefix(merged[i].Statement, "旧方向") {
			oldMass += w
		} else {
			newMass += w
		}
	}
	// 旧侧按后验比例分 0.5（0.8:0.2 → 0.4:0.1），新侧按先验比例分 0.5（0.6:0.1 → 3/7:1/7）
	if math.Abs(oldMass-0.5) > 1e-9 || math.Abs(newMass-0.5) > 1e-9 {
		t.Fatalf("mass split = old %.4f / new %.4f, want 0.5 / 0.5", oldMass, newMass)
	}
	if math.Abs(weights[2]-0.5*0.6/0.7) > 1e-9 {
		t.Fatalf("fresh weight[0] = %v, want %.4f", weights[2], 0.5*0.6/0.7)
	}
}

// 重规划合并不留死假设：被重提的旧假设进新侧（新先验），未重提的零后验压下限参与
func TestReplanAcquireReproposedUsesFreshPrior(t *testing.T) {
	old := []core.Hypothesis{
		{ID: "h_1", Statement: "方向一"},
		{ID: "h_2", Statement: "方向二"},
	}
	post := []float64{0.0, 1.0}
	fresh := []core.Hypothesis{
		{ID: "h_3", Statement: "方向一", Confidence: 0.1}, // 重提（语句相同）
		{ID: "h_4", Statement: "方向三", Confidence: 0.9},
	}

	merged, weights := agent.MergeReplanWeights(old, post, fresh)
	if len(merged) != 3 {
		t.Fatalf("merged = %d, want 3（重提的不重复保留）", len(merged))
	}
	// h_2（后验 1.0）独占旧侧 0.5；新侧按 0.1:0.9 分 0.5
	if math.Abs(weights[0]-0.5) > 1e-9 {
		t.Fatalf("kept old weight = %v, want 0.5", weights[0])
	}
	if math.Abs(weights[1]-0.5*0.1) > 1e-9 || math.Abs(weights[2]-0.5*0.9) > 1e-9 {
		t.Fatalf("fresh weights = %v, want 0.05/0.45", weights[1:])
	}
}

// 问用户统一建模：高 EIG ask 被选中 → investigate 挂起 → 答复归类更新续跑收敛
func TestAcquireLoopAskSuspendResume(t *testing.T) {
	decision := agent.PlanDecision{
		Hypotheses: []core.Hypothesis{
			{ID: "h_1", Statement: "近期变更引入", Confidence: 0.5},
			{ID: "h_2", Statement: "环境漂移", Confidence: 0.5},
		},
		Actions: []agent.ActionProposal{
			{
				Name:     "ask-change",
				Ask:      "问题是最近变更后出现的吗？",
				Purpose:  "区分变更引入",
				Cost:     10,
				Outcomes: []string{"yes", "no"},
				Matrix:   [][]float64{{0.7, 0.3}, {0.2, 0.8}},
			},
			{
				Name:     "check-pods",
				Argv:     []string{"get", "pods"},
				Purpose:  "看 Pod 状态",
				Cost:     1,
				Outcomes: []string{"crash", "running"},
				Matrix:   [][]float64{{0.85, 0.15}, {0.1, 0.9}},
			},
		},
	}
	fakeDecision := agenttest.NewFakeDecisionPlanner(decision)
	fk := agenttest.NewFakeVerifier([]core.Verdict{{
		ID: "v_1", HypothesisID: "h_1", Result: core.VerdictSupported, Reason: "证据支持",
	}})
	verifier := &recordingVerifier{FakeVerifier: fk}
	executor := &scriptedExecutor{script: map[string]string{
		"pods": "pod CrashLoopBackOff",
	}}
	orch := newAcquireOrchestrator(t, fakeDecision, verifier, executor)

	outcome, err := orch.Execute(context.Background(), core.Run{ID: "run_1", SessionID: "sess_1", Question: "q"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// ask 动作胜出（工具动作近零区分力）：挂起等用户
	if outcome.Suspension == nil {
		t.Fatalf("expected suspension for ask action, got report=%v", outcome.Report)
	}
	if outcome.Suspension.Stage != core.StageInvestigate {
		t.Errorf("stage = %q, want investigate", outcome.Suspension.Stage)
	}
	if outcome.Suspension.Question != "问题是最近变更后出现的吗？" {
		t.Errorf("question = %q", outcome.Suspension.Question)
	}
	if len(outcome.Suspension.Options) != 2 || outcome.Suspension.Options[0] != "yes" {
		t.Errorf("options = %v, want ask outcomes", outcome.Suspension.Options)
	}
	if calls := fakeDecision.Calls; calls != 1 {
		t.Errorf("plan calls before suspend = %d, want 1", calls)
	}

	// 答复归类（yes）→ 后验 0.778；随后一次工具观测 0.968 ≥ P* 收敛
	// ——若恢复时错误地重规划（先验重置 0.5），一次观测只到 0.895 不会收敛，
	// 挂起状态连续性由此间接钉住
	resumed, err := orch.Resume(context.Background(), "run_1", "yes，变更后出现")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Report == nil {
		t.Fatalf("expected report after resume")
	}
	if got := orch.LastRunStats().AcquireExit; got != "supported" {
		t.Errorf("exit = %q, want supported after answer update", got)
	}
	if calls := fakeDecision.Calls; calls != 1 {
		t.Errorf("plan calls after resume = %d, want 1 (no replan on resume)", calls)
	}
}

// 分派防御：ours 方法缺决策规划器时明确失败，不静默回退 B1
func TestAcquireLoopRequiresRoles(t *testing.T) {
	parser := agenttest.NewFakeParser(core.Query{ID: "query_1", Goal: "q",
		Nodes: []core.Node{{ID: "node_1", Type: "resource", Text: "demo-api"}}})
	resolver := agenttest.NewFakeResolver(nil)
	planner := agenttest.NewFakePlanner(agent.Plan{})
	verifier := agenttest.NewFakeVerifier(nil)
	reporter := agenttest.NewFakeReporter(core.Report{})
	orch := agent.NewOrchestrator(parser, resolver, planner,
		&scriptedExecutor{}, verifier, reporter, core.NewFactory())
	orch.SetAcquireMethod(agent.AcquireMethodOurs)
	orch.SetInvestigateMaxRounds(2)

	_, err := orch.Execute(context.Background(), core.Run{ID: "run_1", Question: "q"})
	if err == nil || !strings.Contains(err.Error(), "decision planner") {
		t.Fatalf("err = %v, want decision planner required", err)
	}
}

// B1 显式切换走旧路径：零值默认与显式 b1-serial 行为一致（旧循环冒烟）
func TestAcquireMethodB1Dispatch(t *testing.T) {
	parser := agenttest.NewFakeParser(core.Query{ID: "query_1", Goal: "q",
		Nodes: []core.Node{{ID: "node_1", Type: "resource", Text: "demo-api"}}})
	resolver := agenttest.NewFakeResolver(nil)
	// 旧规划器模板：一假设一任务，工具成功 → 一次判决收敛
	plan := agent.Plan{
		Hypotheses: []core.Hypothesis{{ID: "h_1", Statement: "Pod 崩溃"}},
		Tasks: []core.Task{{
			ID: "t_1", ToolName: "k8s", Purpose: "查 Pod",
			Arguments: mustJSON(`{"argv":["get","pods"]}`), Refs: []string{"h_1"},
		}},
	}
	planner := agenttest.NewFakePlanner(plan)
	fake := agenttest.NewFakeVerifier([]core.Verdict{{
		ID: "v_1", HypothesisID: "h_1", Result: core.VerdictSupported, Reason: "证据支持",
	}})
	reporter := agenttest.NewFakeReporter(core.Report{Title: "t"})
	orch := agent.NewOrchestrator(parser, resolver, planner,
		&scriptedExecutor{script: map[string]string{"pods": "CrashLoopBackOff"}}, fake, reporter, core.NewFactory())
	orch.SetAcquireMethod(agent.AcquireMethodB1Serial)
	orch.SetInvestigateMaxRounds(3)

	outcome, err := orch.Execute(context.Background(), core.Run{ID: "run_1", Question: "q"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if outcome.Report == nil {
		t.Fatalf("b1 path should produce report via legacy loop")
	}
	if stats := orch.LastRunStats(); stats.AcquireExit != "" {
		t.Errorf("b1 stats exit = %q, want empty", stats.AcquireExit)
	}
}
