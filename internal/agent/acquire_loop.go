// 取证决策循环（Ours 路径，0.1.2 步骤 3）：a* ← argmax EIG/c 序贯取证 + MSPRT 停止
//
// 与旧 investigateLoop（B1 基线）并行、同签名同返回：旧循环零改动保真「现实现」口径，
// config agent.acquire.method 分派（裁决 1/2）。LLM 只在决策规划、重规划与富文本强度
// 判定三处出现，其余全是 acquire 包的机械算术（思考文档 §2 伪代码的编排落地）；
// 动作执行仍全经 Dispatcher（#16），证据与判决结构不变（置信度是聚合视图非替代）
//
// 轮预算语义（段内）：一轮 = 一次动作执行或一次重规划（重规划也是有成本动作，
// 统一计数防死环）；预算尽走 insufficient 出口（#18 明确失败）。问用户挂起不跨
// 恢复计费：Resume 重置本段预算（与 B1 clarify 挂起同语义——若挂起消耗不可恢复
// 的预算，预算尽前提问会永久卡死，违 #18；实验两臂口径一致）
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/Aruing/Aruing/internal/agent/acquire"
	"github.com/Aruing/Aruing/internal/core"
)

// 决策动作映射的后端工具名：动作提议的 argv 即 kubectl 参数（决策规划提示词口径）
const acquireToolName = "k8s"

// 非全零先验里零值假设的参与下限：完全不参与（权重 0）会让该假设在对数域恒为
// −Inf 永远无法复活，违假设空间活性（思考文档 §3.5），压到极小但不归零
const priorFloor = 1e-6

// 重规划时旧假设侧的保留质量份额：旧假设集体压到一半、新假设按先验分另一半
// （固定规则：abduction 半信半疑——意外观测既否定了旧空间又未证实新空间）
const replanOldShare = 0.5

// 取证决策循环主体；签名与返回口径对齐 investigateLoop，澄清挂起与报告路径共用
func (o *Orchestrator) acquireLoop(
	ctx context.Context,
	query core.Query,
	seed InvestigateState,
	clusterResources []ClusterResource,
) (evidence []core.Evidence, verdicts []core.Verdict, clarify *ClarifyRequest, paused InvestigateState, err error) {
	maxRounds := seed.MaxRounds
	if maxRounds <= 0 {
		maxRounds = defaultInvestigateMaxRounds
	}

	// ours 方法的前置依赖：装配层应已校验，运行期兜底明确失败（不静默回退 B1）
	if o.decisionPlanner == nil {
		return nil, nil, nil, InvestigateState{}, errDecisionPlannerRequired
	}
	judge, ok := o.verifier.(strengthJudge)
	if !ok {
		return nil, nil, nil, InvestigateState{}, errStrengthJudgeRequired
	}

	state := seed
	started := seed.Round
	defer func() {
		o.mu.Lock()
		o.lastStats.InvestigateRounds = started - seed.Round
		o.mu.Unlock()
	}()
	hypotheses := slices.Clone(seed.Hypotheses)
	tasks := slices.Clone(seed.Tasks)
	evidence = slices.Clone(seed.Evidence)
	verdicts = slices.Clone(seed.Verdicts)

	// 首入（无决策状态）：决策规划一次，假设带先验、动作带判别矩阵
	// 挂起恢复（seed.Acquire 非 nil）：信念与动作池自快照连续，不重复规划
	var acq *AcquireState
	var belief acquire.Belief
	if seed.Acquire != nil {
		acq = cloneAcquireStatePtr(seed.Acquire)
		belief = acq.Belief
	} else {
		decision, planErr := o.decisionPlanner.PlanDecision(ctx, PlanState{
			Query:            query,
			Targets:          state.Targets,
			Evidence:         evidence,
			Verdicts:         verdicts,
			ClusterResources: clusterResources,
			Clarifications:   slices.Clone(state.Clarifications),
		})
		if planErr != nil {
			return nil, nil, nil, InvestigateState{}, fmt.Errorf("plan decision: %w", planErr)
		}
		hypotheses = decision.Hypotheses
		belief, err = beliefFromPriors(hypotheses)
		if err != nil {
			return nil, nil, nil, InvestigateState{}, fmt.Errorf("build belief from priors: %w", err)
		}
		acq = &AcquireState{Belief: belief, Actions: slices.Clone(decision.Actions)}
		o.progressf("决策规划：%d 个假设、%d 个动作（解析期丢弃 %d 非法动作）",
			len(hypotheses), len(acq.Actions), decision.DroppedActions)
	}

	for round := state.Round; round < maxRounds; round++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, nil, InvestigateState{}, fmt.Errorf("acquire: %w", ctxErr)
		}
		started = round + 1

		// 待答复的问用户动作：Resume 注入的最近答复按结果类别机械归类更新
		// 零/多命中不更新（问用户无富文本兜底路径），消耗的预算不退
		if acq.Asked != nil && len(state.Clarifications) > 0 {
			answer := state.Clarifications[len(state.Clarifications)-1]
			asked := *acq.Asked
			acq.Asked = nil
			o.progressf("调查第 %d 轮（答复归类）…", round+1)
			if outcome, hit := classifyOutcome(answer, asked.Outcomes); hit {
				if act, actErr := acquire.NewAction(asked.Name, asked.Outcomes, asked.Matrix, asked.Cost); actErr == nil {
					var surprise bool
					belief, surprise, err = updateBeliefOutcome(o.acquireOptions, belief, act, outcome)
					if err != nil {
						return nil, nil, nil, InvestigateState{}, err
					}
					acq.Belief = belief
					writeConfidence(hypotheses, belief)
					if surprise {
						exhausted, replanErr := o.replanAcquire(ctx, query, state.Targets, &hypotheses, &belief, acq, evidence, verdicts, clusterResources, state.Clarifications)
						if replanErr != nil {
							return nil, nil, nil, InvestigateState{}, replanErr
						}
						if exhausted {
							return o.finishAcquire(ctx, query, hypotheses, tasks, evidence, "insufficient", "重规划后无新动作（意外观测未开启新方向）")
						}
					}
				}
			} else {
				o.progressf("  答复未能唯一归类到结果类别，信念不变")
			}
		}

		o.progressf("调查第 %d 轮（决策）…", round+1)

		// 构建候选动作集：矩阵行须与当前假设数对齐（重规划换假设空间后旧矩阵
		// 自然失配，跳过即丢弃——动作级容错与解析层同口径）
		acts, poolIdx := buildAcquireActions(acq.Actions, len(hypotheses))

		// 停止检查（先于选择与池尽处理，思考文档 §2 步骤 4）：supported → 收敛出判；
		// refuted → 质量坍缩重规划；insufficient → 平台/预算尽带缺口。
		// 前置：至少一条证据——Verdict 必须引用 Evidence（#5），零证据收敛无法出判。
		// 动作池尽时 maxEIG 取 +Inf：池尽不是信息平台（有动作区分不动才是），
		// 平台分支自然不触发，supported/refuted 照常评估
		maxEIG := math.Inf(1)
		var sel acquire.Selection
		if len(acts) > 0 {
			var selErr error
			sel, selErr = acquire.Select(belief, acts)
			if selErr != nil {
				return nil, nil, nil, InvestigateState{}, fmt.Errorf("select action (round %d): %w", round, selErr)
			}
			maxEIG = sel.MaxEIG
		}
		if len(evidence) > 0 {
			stop := acquire.CheckStop(belief, maxEIG, maxRounds-round, o.acquireOptions)
			if stop.Stop {
				switch stop.Kind {
				case acquire.VerdictSupported:
					writeConfidence(hypotheses, belief)
					o.progressf("  取证收敛：假设 %d 后验 %.3f，出正式判断", stop.Winner, stop.Confidence)
					return o.finishAcquire(ctx, query, hypotheses, tasks, evidence, "supported", "")
				case acquire.VerdictInsufficient:
					writeConfidence(hypotheses, belief)
					o.progressf("  取证停止（insufficient）：%s", stop.Gap)
					return o.finishAcquire(ctx, query, hypotheses, tasks, evidence, "insufficient", stop.Gap)
				case acquire.VerdictRefuted:
					o.progressf("  假设空间被证据压死（质量坍缩），abduction 重规划…")
					exhausted, replanErr := o.replanAcquire(ctx, query, state.Targets, &hypotheses, &belief, acq, evidence, verdicts, clusterResources, state.Clarifications)
					if replanErr != nil {
						return nil, nil, nil, InvestigateState{}, replanErr
					}
					if exhausted {
						return o.finishAcquire(ctx, query, hypotheses, tasks, evidence, "insufficient", "重规划后无新动作（假设空间压死后无新方向）")
					}
					continue
				}
			}
		}

		if len(acts) == 0 {
			// 动作池尽：重规划一次补充；仍无新动作 = 动作空间平台，insufficient 出口
			o.progressf("  动作池尽，重规划补充…")
			exhausted, replanErr := o.replanAcquire(ctx, query, state.Targets, &hypotheses, &belief, acq, evidence, verdicts, clusterResources, state.Clarifications)
			if replanErr != nil {
				return nil, nil, nil, InvestigateState{}, replanErr
			}
			if exhausted {
				return o.finishAcquire(ctx, query, hypotheses, tasks, evidence, "insufficient", "决策规划无新动作提议（动作空间平台）")
			}
			continue
		}

		// 执行 a*：问用户动作挂起（复用 investigate clarify 机制，选项即结果类别），
		// 工具动作映射 core.Task 经 Dispatcher 执行（#16）
		chosen := acq.Actions[poolIdx[sel.Best]]
		if chosen.Ask != "" {
			asked := chosen
			acq.Asked = &asked
			acq.Actions = slices.DeleteFunc(acq.Actions, func(p ActionProposal) bool { return p.Name == chosen.Name })
			acq.Executed = append(acq.Executed, chosen.Name)
			acq.Belief = belief
			o.progressf("  选中问用户动作 %q（成本 %.0f）", chosen.Name, chosen.Cost)
			state.Hypotheses = hypotheses
			state.Tasks = tasks
			state.Evidence = evidence
			state.Verdicts = verdicts
			state.Round = round
			state.Acquire = acq
			return nil, nil, &ClarifyRequest{
				Question: strings.TrimSpace(chosen.Ask),
				Options:  slices.Clone(chosen.Outcomes),
			}, cloneInvestigateState(state), nil
		}

		o.progressf("  选中动作 %q（EIG/c = %.2f）：%s", chosen.Name, sel.BestScore, chosen.Purpose)
		task, taskErr := o.acquireTask(query, hypotheses, chosen)
		if taskErr != nil {
			return nil, nil, nil, InvestigateState{}, taskErr
		}
		item, execErr := o.executeTask(ctx, task)
		if execErr != nil && (!errors.Is(execErr, errToolFailed) || item == nil) {
			return nil, nil, nil, InvestigateState{}, fmt.Errorf("execute action %q: %w", chosen.Name, execErr)
		}
		// 工具失败合成为失败证据，照常参与更新（失败本身也是观测）
		tasks = append(tasks, task)
		evidence = append(evidence, *item)
		acq.Actions = slices.DeleteFunc(acq.Actions, func(p ActionProposal) bool { return p.Name == chosen.Name })
		acq.Executed = append(acq.Executed, chosen.Name)

		// 观测归类与信念更新：机械优先（唯一命中判别矩阵列），零/多命中走强度路径
		act := acts[sel.Best]
		var surprise bool
		if outcome, hit := classifyOutcome(evidenceText(item), chosen.Outcomes); hit {
			belief, surprise, err = updateBeliefOutcome(o.acquireOptions, belief, act, outcome)
		} else {
			belief, surprise, err = o.strengthUpdate(ctx, judge, *item, hypotheses, belief)
		}
		if err != nil {
			return nil, nil, nil, InvestigateState{}, err
		}
		acq.Belief = belief
		writeConfidence(hypotheses, belief)
		post := belief.Posterior()
		top := 0
		for i, p := range post {
			if p > post[top] {
				top = i
			}
		}
		o.progressf("  信念更新：领先假设后验 %.3f", post[top])

		// 全局意外：观测在所有假设下都低概率 → abduction 重规划（旧假设保留压低）
		if surprise {
			o.progressf("  全局意外：假设空间未预期该观测，abduction 重规划…")
			exhausted, replanErr := o.replanAcquire(ctx, query, state.Targets, &hypotheses, &belief, acq, evidence, verdicts, clusterResources, state.Clarifications)
			if replanErr != nil {
				return nil, nil, nil, InvestigateState{}, replanErr
			}
			if exhausted {
				return o.finishAcquire(ctx, query, hypotheses, tasks, evidence, "insufficient", "重规划后无新动作（意外观测未开启新方向）")
			}
		}
	}

	// 预算尽：insufficient 出口（#18 明确失败，非静默截断）
	writeConfidence(hypotheses, belief)
	o.progressf("  取证预算尽，出 insufficient")
	return o.finishAcquire(ctx, query, hypotheses, tasks, evidence, "insufficient", "预算尽：取证动作次数已达上限")
}

// 重规划（abduction）：带当前证据/判决上下文再调决策规划，合并假设空间并重建信念
//
// 旧假设未被子集重提的保留、集体压到 replanOldShare；新假设按先验分其余质量
// （新 ID 由规划器发放；语句精确匹配视为同一假设被重提，采用新先验）
// 返回 true 表示重规划后无未执行的新动作（调用方走 insufficient 出口）；
// 规划调用本身失败作为运行错误上抛（与 B1 路径规划失败同口径，不静默降级）
func (o *Orchestrator) replanAcquire(
	ctx context.Context,
	query core.Query,
	targets []core.Target,
	hypotheses *[]core.Hypothesis,
	belief *acquire.Belief,
	acq *AcquireState,
	evidence []core.Evidence,
	verdicts []core.Verdict,
	clusterResources []ClusterResource,
	clarifications []string,
) (bool, error) {
	decision, err := o.decisionPlanner.PlanDecision(ctx, PlanState{
		Query:            query,
		Targets:          targets,
		Evidence:         evidence,
		Verdicts:         verdicts,
		ClusterResources: clusterResources,
		Clarifications:   slices.Clone(clarifications),
	})
	if err != nil {
		return false, fmt.Errorf("replan decision: %w", err)
	}

	merged, weights := MergeReplanWeights(*hypotheses, belief.Posterior(), decision.Hypotheses)

	next, berr := acquire.NewBelief(weights)
	if berr != nil {
		// 合并权重全非法属编程错误（两侧均有下限保障），明确上抛
		return false, fmt.Errorf("rebuild belief after replan: %w", berr)
	}
	*hypotheses = merged
	*belief = next
	acq.Belief = next
	writeConfidence(merged, next)

	// 动作池换新（旧矩阵对旧假设空间，失配自然作废）；过滤已执行同名动作
	executed := make(map[string]struct{}, len(acq.Executed))
	for _, name := range acq.Executed {
		executed[name] = struct{}{}
	}
	pool := make([]ActionProposal, 0, len(decision.Actions))
	for _, a := range decision.Actions {
		if _, done := executed[a.Name]; done {
			continue
		}
		pool = append(pool, a)
	}
	acq.Actions = pool
	o.progressf("  重规划：%d 个假设（保留 %d 旧）、%d 个新动作", len(merged), len(merged)-len(decision.Hypotheses), len(pool))
	return len(pool) == 0, nil
}

// 重规划假设合并的纯算术：旧假设未被重提的按后验比例分 replanOldShare（零后验压
// 下限可复活），新假设按先验比例分其余；两侧各自先归一再乘份额——直接乘原始先验
// 会让「先验和 ≠ 1」的计划偏占质量，50/50 拆分被破坏（pr-agent 评审采纳修正）
//
// 语句精确匹配视为同一假设被重提（进新侧、采用新先验）；无保留旧假设时新侧占满全量
func MergeReplanWeights(old []core.Hypothesis, post []float64, fresh []core.Hypothesis) ([]core.Hypothesis, []float64) {
	freshSet := make(map[string]struct{}, len(fresh))
	for _, h := range fresh {
		freshSet[strings.TrimSpace(h.Statement)] = struct{}{}
	}
	merged := make([]core.Hypothesis, 0, len(old)+len(fresh))
	oldWeights := make([]float64, 0, len(old))

	var keptShare float64
	for i, h := range old {
		if _, reproj := freshSet[strings.TrimSpace(h.Statement)]; reproj {
			continue
		}
		w := priorFloor
		if i < len(post) && post[i] > priorFloor {
			w = post[i]
		}
		merged = append(merged, h)
		oldWeights = append(oldWeights, w)
		keptShare += w
	}

	newShare := 1.0
	if keptShare > 0 {
		newShare = replanOldShare
		for i := range oldWeights {
			oldWeights[i] *= replanOldShare / keptShare
		}
	}

	allZero := true
	for _, h := range fresh {
		if h.Confidence > 0 {
			allZero = false
			break
		}
	}
	freshWeights := make([]float64, len(fresh))
	var freshTotal float64
	for i, h := range fresh {
		w := h.Confidence
		if allZero {
			w = 1
		} else if w < priorFloor {
			w = priorFloor
		}
		freshWeights[i] = w
		freshTotal += w
	}

	weights := oldWeights
	if freshTotal > 0 {
		for i, h := range fresh {
			merged = append(merged, h)
			weights = append(weights, newShare*freshWeights[i]/freshTotal)
		}
	}
	return merged, weights
}

// 出口收尾：后验已回写，正式 Verify 出判决（语义与 B1 收尾一致），记观测统计
func (o *Orchestrator) finishAcquire(
	ctx context.Context,
	query core.Query,
	hypotheses []core.Hypothesis,
	tasks []core.Task,
	evidence []core.Evidence,
	exit, gap string,
) ([]core.Evidence, []core.Verdict, *ClarifyRequest, InvestigateState, error) {
	verdicts, err := o.verifier.Verify(ctx, query, hypotheses, tasks, evidence)
	if err != nil {
		return nil, nil, nil, InvestigateState{}, fmt.Errorf("verify evidence (acquire exit %s): %w", exit, err)
	}
	o.mu.Lock()
	o.lastStats.AcquireExit = exit
	o.lastStats.AcquireGap = gap
	o.mu.Unlock()
	return evidence, verdicts, nil, InvestigateState{}, nil
}

// 富文本强度更新：机械归类不可用时，验证器兼职对单条证据逐假设出 (d,s)
func (o *Orchestrator) strengthUpdate(
	ctx context.Context,
	judge strengthJudge,
	item core.Evidence,
	hypotheses []core.Hypothesis,
	belief acquire.Belief,
) (acquire.Belief, bool, error) {
	judgements, err := judge.JudgeStrength(ctx, item, hypotheses)
	if err != nil {
		return acquire.Belief{}, false, fmt.Errorf("judge strength for evidence %q: %w", item.ID, err)
	}
	se := acquire.StrengthEvidence{D: make([]int, len(judgements)), S: make([]float64, len(judgements))}
	for i, j := range judgements {
		se.D[i] = j.Direction
		se.S[i] = j.Strength
	}
	return updateBeliefStrength(o.acquireOptions, belief, se)
}

// 离散结果更新：观测落入判别矩阵列；全零列观测（所有假设都认为不可能）
// 映射为全局意外标志（保持原信念转重规划），不当作运行错误
func updateBeliefOutcome(opts acquire.Options, belief acquire.Belief, act acquire.Action, outcome string) (acquire.Belief, bool, error) {
	next, surprise, err := opts.UpdateOutcome(belief, act, outcome)
	if errors.Is(err, acquire.ErrImpossibleOutcome) {
		return belief, true, nil
	}
	return next, surprise, err
}

// 富文本强度更新的同名包装（口径对齐 updateBeliefOutcome）
func updateBeliefStrength(opts acquire.Options, belief acquire.Belief, se acquire.StrengthEvidence) (acquire.Belief, bool, error) {
	next, surprise, err := opts.UpdateStrength(belief, se)
	if errors.Is(err, acquire.ErrImpossibleOutcome) {
		return belief, true, nil
	}
	return next, surprise, err
}

// 把决策动作映射为可执行任务：argv 组装为后端工具参数，引用当前全量假设（#6 不加专用字段）
func (o *Orchestrator) acquireTask(query core.Query, hypotheses []core.Hypothesis, chosen ActionProposal) (core.Task, error) {
	id, err := o.factory.NewID("t")
	if err != nil {
		return core.Task{}, fmt.Errorf("create task ID: %w", err)
	}
	argv := make([]any, len(chosen.Argv))
	for i, a := range chosen.Argv {
		argv[i] = a
	}
	args, err := json.Marshal(map[string]any{"argv": argv})
	if err != nil {
		return core.Task{}, fmt.Errorf("marshal action arguments: %w", err)
	}
	refs := make([]string, 0, len(hypotheses))
	for _, h := range hypotheses {
		if h.ID != "" {
			refs = append(refs, h.ID)
		}
	}
	return core.Task{
		ID:        id,
		RunID:     query.RunID,
		Refs:      refs,
		ToolName:  acquireToolName,
		Arguments: args,
		Purpose:   chosen.Purpose,
	}, nil
}

// 从动作池构建对齐的 acquire 动作集：矩阵行数与假设数不符的跳过（重规划后旧矩阵作废）
// 返回动作集与池内索引的对应关系（选择结果回指池中提议）
func buildAcquireActions(proposals []ActionProposal, hypothesisCount int) ([]acquire.Action, []int) {
	acts := make([]acquire.Action, 0, len(proposals))
	idx := make([]int, 0, len(proposals))
	for i, p := range proposals {
		if len(p.Matrix) != hypothesisCount {
			continue
		}
		act, err := acquire.NewAction(p.Name, p.Outcomes, p.Matrix, p.Cost)
		if err != nil {
			continue
		}
		acts = append(acts, act)
		idx = append(idx, i)
	}
	return acts, idx
}

// 先验建信念：全部为零回退均匀（模型未表达倾向）；否则零值按下限参与（不归零，保活性）
func beliefFromPriors(hypotheses []core.Hypothesis) (acquire.Belief, error) {
	allZero := true
	for _, h := range hypotheses {
		if h.Confidence > 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return acquire.NewUniformBelief(len(hypotheses))
	}
	weights := make([]float64, len(hypotheses))
	for i, h := range hypotheses {
		w := h.Confidence
		if w < priorFloor {
			w = priorFloor
		}
		weights[i] = w
	}
	return acquire.NewBelief(weights)
}

// 后验回写假设置信度（聚合视图；先验被决策规划写入，后验由此处覆盖）
func writeConfidence(hypotheses []core.Hypothesis, belief acquire.Belief) {
	post := belief.Posterior()
	for i := range hypotheses {
		if i < len(post) {
			hypotheses[i].Confidence = post[i]
		}
	}
}

// 机械归类的观测文本面：结构化投影摘要 + 失败错误（raw 留给富文本强度路径）
func evidenceText(item *core.Evidence) string {
	if item == nil {
		return ""
	}
	return item.Summary + "\n" + item.Error
}

// 深拷贝决策状态指针变体（挂起快照用）
func cloneAcquireStatePtr(state *AcquireState) *AcquireState {
	cloned := cloneAcquireState(*state)
	return &cloned
}
