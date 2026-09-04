// 决策规划器：把问题结构与已确认目标交给大模型，产出带先验的假设与带判别矩阵的动作提议
//
// 与旧 LLMPlanner 并行的独立路径（冻结裁决）：旧 planner.md / LLMPlanner /
// investigateLoop 原样不动，保真 B1 基线口径；本路径供取证决策循环消费，
// 接线在后续步骤（Orchestrator 按 config 分派）
//
// 模型输出经动作级容错解析（非法动作丢弃计数），假设与动作的系统编号由本模块
// 经工厂发放；工具规格来自注册表注入，不在提示词手写第二份工具清单（#9 / #16）
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

//go:embed prompts/planner-decision.md
var plannerDecisionPrompt string

// 用大模型生成决策规划素材的规划器
//
// 持有不可变依赖（客户端、工厂、工具规格快照、提示词），可被多次运行复用；
// 不持有跨运行可变状态，每次规划独立发结构化生成
type LLMDecisionPlanner struct {
	// 大模型客户端，每次规划发一次结构化生成
	client llm.Client
	// 领域编号与创建时间工厂，回填假设时使用
	factory *core.Factory
	// 已注入工具规格的系统提示词全文，构造后只读
	prompt string
}

// 创建基于大模型的决策规划器
// 规格用于拼系统提示词（教学 argv 形态与只读口径）；空列表时仍可构造
func NewLLMDecisionPlanner(client llm.Client, factory *core.Factory, specs []tools.ToolSpec) (*LLMDecisionPlanner, error) {
	if client == nil {
		return nil, errors.New("LLM decision planner requires an llm client")
	}
	if factory == nil {
		return nil, errors.New("LLM decision planner requires a factory")
	}
	// 复用规划器的规格注入（占位符替换是纯机械动作，与旧路径同一实现）
	system, err := buildPlannerSystemPrompt(plannerDecisionPrompt, specs)
	if err != nil {
		return nil, err
	}
	return &LLMDecisionPlanner{client: client, factory: factory, prompt: system}, nil
}

// 请求模型生成决策规划，解析容错后回填假设系统编号与运行绑定
//
// 输入载荷与旧规划器同构（复用 buildPlannerUserPayload）：后续轮带证据与
// 判决时，供 abduction 重规划使用
// 计划级违规（零假设 / 全部动作非法）在业务级重试内重新请求，上限次仍
// 不合规则返回模型输出不一致错误；动作级容错（部分丢弃）不触发重试
func (p *LLMDecisionPlanner) PlanDecision(ctx context.Context, state PlanState) (PlanDecision, error) {
	if ctx == nil {
		return PlanDecision{}, errors.New("decision planner requires a context")
	}
	if err := ctx.Err(); err != nil {
		return PlanDecision{}, fmt.Errorf("plan decision: %w", err)
	}
	if p == nil {
		return PlanDecision{}, errors.New("decision planner is required")
	}
	if strings.TrimSpace(state.Query.RunID) == "" {
		return PlanDecision{}, errors.New("decision planner requires a run ID")
	}

	userPayload, err := buildPlannerUserPayload(state)
	if err != nil {
		return PlanDecision{}, fmt.Errorf("build decision prompt: %w", err)
	}
	req := llm.Request{System: p.prompt, User: userPayload}

	var lastErr error
	for attempt := 0; attempt < maxPlanAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return PlanDecision{}, fmt.Errorf("plan decision: %w", err)
		}

		var raw json.RawMessage
		// 先收原始 JSON 再进容错解析：解析与校验合一（parseDecisionOutput），
		// 动作级容错在这里而非客户端层做
		if gErr := p.client.GenerateJSON(ctx, req, &raw); gErr != nil {
			return PlanDecision{}, fmt.Errorf("plan decision with LLM: %w", gErr)
		}
		decision, pErr := parseDecisionOutput(raw)
		if pErr != nil {
			lastErr = pErr
			continue
		}
		return p.fillDecision(state.Query.RunID, decision)
	}

	return PlanDecision{}, fmt.Errorf("%w: decision output invalid after %d attempts: %v",
		ErrLLMOutputInconsistent, maxPlanAttempts, lastErr)
}

// 回填假设的系统编号、运行绑定与创建时间（动作提议不是持久化实体，不回填）
func (p *LLMDecisionPlanner) fillDecision(runID string, decision PlanDecision) (PlanDecision, error) {
	now := p.factory.Now()
	for i := range decision.Hypotheses {
		id, err := p.factory.NewID("h")
		if err != nil {
			return PlanDecision{}, fmt.Errorf("create hypothesis ID: %w", err)
		}
		decision.Hypotheses[i].ID = id
		decision.Hypotheses[i].RunID = runID
		decision.Hypotheses[i].CreatedAt = now
	}
	return decision, nil
}
