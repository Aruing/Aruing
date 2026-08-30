// B3 ReAct 对比臂的每轮选择器（统一实验批强对照，0.1.2 步骤 5）
//
// 形态（冻结裁决 1）：复用 acquireLoop 全部骨架，只把选择策略换为每轮一次
// LLM 调用——输入 = 假设列表 + 已有证据摘要 + 动作菜单（含成本），输出 =
// 下一个动作或「证据足够可验证」；无贝叶斯更新、无 EIG、无 MSPRT，无显式
// 信念状态正是对照点（业界 agent 现状代表）
//
// 信息公平性（冻结裁决 2）：选择器所见与 ours 决策输入同构——同样动作菜单
// （含结果类别标签）、同样成本标注、同样假设与证据摘要，只少数学层（不喂
// 判别矩阵、不喂信念后验），对手弱化即稻草人指控点
//
// 计划级违规（动作名不在菜单 / action 与 sufficient 同给 / 两者皆空）在业务级
// 重试内重新请求，上限次仍不合规则返回模型输出不一致错误（与决策规划器同口径）
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
)

//go:embed prompts/react-select.md
var reactSelectPrompt string

// ReActSelectRequest 一次选择调用的输入视图；动作菜单 = 未执行动作池全体
type ReActSelectRequest struct {
	// 当前假设空间（语句与预期信号，无信念后验——B3 无数学层）
	Hypotheses []core.Hypothesis
	// 历次取证累积的证据（载荷构建只取摘要面）
	Evidence []core.Evidence
	// 待选动作菜单（含成本与结果类别标签；不含判别矩阵）
	Actions []ActionProposal
	// 用户澄清的累积答复（问用户动作挂起恢复后可见）
	Clarifications []string
	// 剩余轮次预算（提示词据此引导果断声明足够）
	BudgetLeft int
}

// ReActChoice 选择器输出：下一个动作名或声明证据足够，二者互斥
type ReActChoice struct {
	// 选中的动作名（须属于输入菜单，原样回传）
	ActionName string
	// 声明证据足够：不再取证，进入正式验证
	Sufficient bool
	// 判断依据原文（一两句）；逐字进决策轨迹（DecisionTrace）
	Reason string
}

// B3 每轮选择能力（可选注入，b3-react 方法必需；先例 SetDecisionPlanner）
type reactSelector interface {
	// 从动作菜单选出下一个动作，或声明证据足够
	SelectAction(context.Context, ReActSelectRequest) (ReActChoice, error)
}

// 用大模型做每轮选择的 ReAct 选择器
//
// 持有不可变依赖（客户端、提示词），可被多次运行复用；不持有跨轮状态——
// 每轮的假设/证据/菜单快照由编排侧组装传入，选择器自身无记忆（无显式
// 信念状态正是对照点）
type LLMReActSelector struct {
	// 大模型客户端，每轮选择发一次结构化生成
	client llm.Client
	// 系统提示词全文，构造后只读
	prompt string
}

// 创建基于大模型的 ReAct 选择器
func NewLLMReActSelector(client llm.Client) (*LLMReActSelector, error) {
	if client == nil {
		return nil, errors.New("LLM ReAct selector requires an llm client")
	}
	return &LLMReActSelector{client: client, prompt: reactSelectPrompt}, nil
}

// 请求模型选出下一动作或声明足够；输出经菜单校验，违规在重试内重新请求
func (s *LLMReActSelector) SelectAction(ctx context.Context, req ReActSelectRequest) (ReActChoice, error) {
	if ctx == nil {
		return ReActChoice{}, errors.New("react selector requires a context")
	}
	if err := ctx.Err(); err != nil {
		return ReActChoice{}, fmt.Errorf("react select: %w", err)
	}
	if s == nil {
		return ReActChoice{}, errors.New("react selector is required")
	}
	if len(req.Actions) == 0 {
		return ReActChoice{}, errors.New("react select requires a non-empty action menu")
	}

	payload, err := buildReActUserPayload(req)
	if err != nil {
		return ReActChoice{}, fmt.Errorf("build react select prompt: %w", err)
	}
	llmReq := llm.Request{System: s.prompt, User: payload}

	// 菜单名集合：输出动作名的合法域（编造动作即计划级违规）
	valid := make(map[string]struct{}, len(req.Actions))
	for _, a := range req.Actions {
		valid[a.Name] = struct{}{}
	}

	var lastRaw json.RawMessage
	var lastErr error
	for attempt := 0; attempt < maxPlanAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ReActChoice{}, fmt.Errorf("react select: %w", err)
		}

		var raw json.RawMessage
		if gErr := s.client.GenerateJSON(ctx, llmReq, &raw); gErr != nil {
			return ReActChoice{}, fmt.Errorf("react select with LLM: %w", gErr)
		}
		choice, pErr := parseReActOutput(raw, valid)
		if pErr != nil {
			lastRaw = raw
			lastErr = pErr
			continue
		}
		return choice, nil
	}
	return ReActChoice{}, fmt.Errorf("%w: react select output invalid after %d attempts: %v, last output: %s",
		ErrLLMOutputInconsistent, maxPlanAttempts, lastErr, previewRaw(lastRaw))
}

// 模型侧的选择输出
type reactLLMOutput struct {
	// 选中的动作名；声明足够时省略
	Action string `json:"action"`
	// 声明证据足够；选动作时省略
	Sufficient bool `json:"sufficient"`
	// 判断依据
	Reason string `json:"reason"`
}

// 解析并校验选择输出：action / sufficient 二选一（同给或皆空即违规），
// action 必须属于菜单合法域（编造动作不可用——无动作级丢弃可退，只能整次重试）
func parseReActOutput(data []byte, valid map[string]struct{}) (ReActChoice, error) {
	var out reactLLMOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return ReActChoice{}, fmt.Errorf("react select output: %w", err)
	}
	choice := ReActChoice{Reason: strings.TrimSpace(out.Reason)}
	switch {
	case out.Sufficient && strings.TrimSpace(out.Action) != "":
		return ReActChoice{}, errors.New("react select output cannot carry both action and sufficient")
	case out.Sufficient:
		choice.Sufficient = true
		return choice, nil
	case strings.TrimSpace(out.Action) != "":
		name := strings.TrimSpace(out.Action)
		if _, ok := valid[name]; !ok {
			return ReActChoice{}, fmt.Errorf("react select output references unknown action %q", name)
		}
		choice.ActionName = name
		return choice, nil
	default:
		return ReActChoice{}, errors.New("react select output requires either action or sufficient")
	}
}

// 序列化选择输入：假设（语句面）、证据（摘要面）、动作菜单（含成本与结果
// 类别，无矩阵）、澄清累积与剩余预算；字段面与 ours 决策输入同构只少数学层
func buildReActUserPayload(req ReActSelectRequest) (string, error) {
	// 假设视图：语句/理由/预期信号（不带 Confidence——先验与后验都是数学层素材）
	type hypView struct {
		ID              string   `json:"id"`
		Statement       string   `json:"statement"`
		Reason          string   `json:"reason,omitempty"`
		ExpectedSignals []string `json:"expected_signals,omitempty"`
	}
	// 证据视图：摘要面（工具投影摘要 + 失败错误 + 命令回显；raw 不进选择载荷）
	type evidenceView struct {
		ID      string `json:"id"`
		Command string `json:"command,omitempty"`
		Summary string `json:"summary"`
		Error   string `json:"error,omitempty"`
	}
	// 动作菜单视图：名字/参数或问句/目的/成本/结果类别（无判别矩阵）
	type actionView struct {
		Name     string   `json:"name"`
		Argv     []string `json:"argv,omitempty"`
		Ask      string   `json:"ask,omitempty"`
		Purpose  string   `json:"purpose,omitempty"`
		Cost     float64  `json:"cost"`
		Outcomes []string `json:"outcomes,omitempty"`
	}
	type payload struct {
		Hypotheses     []hypView      `json:"hypotheses"`
		Evidence       []evidenceView `json:"evidence,omitempty"`
		Actions        []actionView   `json:"actions"`
		Clarifications []string       `json:"clarifications,omitempty"`
		BudgetLeft     int            `json:"budget_left"`
	}

	p := payload{
		Hypotheses:     make([]hypView, 0, len(req.Hypotheses)),
		Evidence:       make([]evidenceView, 0, len(req.Evidence)),
		Actions:        make([]actionView, 0, len(req.Actions)),
		Clarifications: req.Clarifications,
		BudgetLeft:     req.BudgetLeft,
	}
	for _, h := range req.Hypotheses {
		p.Hypotheses = append(p.Hypotheses, hypView{
			ID:              h.ID,
			Statement:       h.Statement,
			Reason:          h.Reason,
			ExpectedSignals: append([]string(nil), h.ExpectedSignals...),
		})
	}
	for _, ev := range req.Evidence {
		p.Evidence = append(p.Evidence, evidenceView{
			ID:      ev.ID,
			Command: ev.CommandView,
			Summary: ev.Summary,
			Error:   ev.Error,
		})
	}
	for _, a := range req.Actions {
		p.Actions = append(p.Actions, actionView{
			Name:     a.Name,
			Argv:     append([]string(nil), a.Argv...),
			Ask:      a.Ask,
			Purpose:  a.Purpose,
			Cost:     a.Cost,
			Outcomes: append([]string(nil), a.Outcomes...),
		})
	}

	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
