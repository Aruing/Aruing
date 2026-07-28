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
	"aruing/internal/session"
)

//go:embed prompts/tower.md
var towerPrompt string

// 业务级重试：模型可能偶发产出非法 action 或空 content
const maxTowerAttempts = 3

// 历史消息塞进 prompt 的条数上限，避免上下文无限膨胀
const maxTowerHistoryMessages = 20

const (
	towerActionReply    = "reply"
	towerActionEscalate = "escalate"
)

// 会话总控：实现 session.Responder；本步仅 reply / escalate
// 不持有跨 Turn 可变状态；每次 Respond 独立决策
type TowerResponder struct {
	client   llm.Client
	factory  *core.Factory
	executor session.RunExecutor
	prompt   string
}

// 创建基于大模型的 Tower；任一依赖缺失直接返回错误
func NewTowerResponder(client llm.Client, factory *core.Factory, executor session.RunExecutor) (*TowerResponder, error) {
	if client == nil {
		return nil, errors.New("tower requires an llm client")
	}
	if factory == nil {
		return nil, errors.New("tower requires a factory")
	}
	if executor == nil {
		return nil, errors.New("tower requires a run executor")
	}
	return &TowerResponder{
		client:   client,
		factory:  factory,
		executor: executor,
		prompt:   towerPrompt,
	}, nil
}

// 看历史与当前句，reply 或 escalate；写库由 session.Service.Turn 负责
func (t *TowerResponder) Respond(ctx context.Context, in session.RespondInput) (session.RespondOutput, error) {
	if err := ctx.Err(); err != nil {
		return session.RespondOutput{}, fmt.Errorf("tower respond: %w", err)
	}
	if t == nil {
		return session.RespondOutput{}, errors.New("tower responder is nil")
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return session.RespondOutput{}, errors.New("tower requires a session id")
	}
	if strings.TrimSpace(in.UserText) == "" {
		return session.RespondOutput{}, errors.New("tower requires user text")
	}

	decision, err := t.decide(ctx, in)
	if err != nil {
		return session.RespondOutput{}, err
	}

	switch decision.Action {
	case towerActionReply:
		return session.RespondOutput{
			Content: decision.Content,
			Mode:    session.ModeBaseline,
		}, nil
	case towerActionEscalate:
		question := strings.TrimSpace(decision.Question)
		if question == "" {
			question = in.UserText
		}
		return session.Escalate(ctx, t.factory, t.executor, in.SessionID, question)
	default:
		return session.RespondOutput{}, fmt.Errorf("tower: unknown action %q", decision.Action)
	}
}

// 模型一次决策的结构化输出
type towerDecision struct {
	Action   string `json:"action"`
	Content  string `json:"content"`
	Question string `json:"question"`
}

func (t *TowerResponder) decide(ctx context.Context, in session.RespondInput) (towerDecision, error) {
	userPayload, err := buildTowerUserPayload(in)
	if err != nil {
		return towerDecision{}, fmt.Errorf("tower payload: %w", err)
	}
	req := llm.Request{
		System: t.prompt,
		User:   userPayload,
	}

	var lastOut towerDecision
	var lastValidateErr error
	for attempt := 0; attempt < maxTowerAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return towerDecision{}, fmt.Errorf("tower decide: %w", err)
		}
		var out towerDecision
		if gErr := t.client.GenerateJSON(ctx, req, &out); gErr != nil {
			return towerDecision{}, fmt.Errorf("tower decide with LLM: %w", gErr)
		}
		if vErr := validateTowerDecision(out); vErr != nil {
			lastOut = out
			lastValidateErr = vErr
			continue
		}
		return normalizeTowerDecision(out), nil
	}
	return towerDecision{}, fmt.Errorf("%w: last error: %v, last output: %+v",
		ErrLLMOutputInconsistent, lastValidateErr, lastOut)
}

func validateTowerDecision(out towerDecision) error {
	action := strings.TrimSpace(strings.ToLower(out.Action))
	switch action {
	case towerActionReply:
		if strings.TrimSpace(out.Content) == "" {
			return errors.New("reply requires non-empty content")
		}
		return nil
	case towerActionEscalate:
		return nil
	default:
		return fmt.Errorf("invalid action %q", out.Action)
	}
}

func normalizeTowerDecision(out towerDecision) towerDecision {
	out.Action = strings.TrimSpace(strings.ToLower(out.Action))
	out.Content = strings.TrimSpace(out.Content)
	out.Question = strings.TrimSpace(out.Question)
	return out
}

// 序列化本轮输入供模型消费；历史只保留最近 maxTowerHistoryMessages 条
func buildTowerUserPayload(in session.RespondInput) (string, error) {
	type histMsg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	history := in.History
	if len(history) > maxTowerHistoryMessages {
		history = history[len(history)-maxTowerHistoryMessages:]
	}
	msgs := make([]histMsg, 0, len(history))
	for _, m := range history {
		msgs = append(msgs, histMsg{Role: m.Role, Content: m.Content})
	}
	payload := struct {
		UserText string    `json:"user_text"`
		History  []histMsg `json:"history"`
	}{
		UserText: in.UserText,
		History:  msgs,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
