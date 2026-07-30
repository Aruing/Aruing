package session

import (
	"context"
	"fmt"
	"strings"

	"aruing/internal/core"
)

// 一轮 Turn 的可观察结果，供调用方与测试断言
type TurnResult struct {
	// 本轮写入的用户消息
	UserMessage Message
	// 本轮写入的助手消息
	AssistantMessage Message
	// 本轮若升格诊断则有 Run 编号，否则为空
	RunID string
	// 本轮若跑了诊断则非空
	Report *core.Report
}

// 本轮「业务上怎么答」的可注入接口；写库只在 Service.Turn 内完成
// 实现可为 Echo / Diagnose / agent.TowerResponder（reply / call_tool / escalate）；接口形状保持稳定
type Responder interface {
	Respond(ctx context.Context, in RespondInput) (RespondOutput, error)
}

// 交给 Responder 的本轮输入
// History 为本轮用户消息写入前的历史（不含本轮 user），UserText 为当前用户原文
type RespondInput struct {
	// 当前会话编号，升格诊断时写入 Run.SessionID
	SessionID string
	// 本轮用户原文
	UserText string
	// 写入本轮 user 之前的消息列表，按时间顺序
	History []Message
}

// Responder 产出的回复内容与可选诊断结果
// 不负责落库；由 Turn 根据本结构写 assistant 消息
type RespondOutput struct {
	// 助手回复正文
	Content string
	// 助手展示模式：baseline 或 diagnostic（checkpoint 由 CheckpointContent 另写）
	Mode string
	// 本轮若开了诊断则有
	RunID string
	// 本轮若开了诊断则非空
	Report *core.Report
	// L2 handoff 摘要；非空时 Turn 在 assistant 前写入 ModeCheckpoint
	// Store 仍保留压缩前全量历史，checkpoint 是增补不是替换
	CheckpointContent string
}

// 会话服务：创建会话并执行 Turn
// 同一会话本步约定串行调用，不加跨 Turn 锁
type Service struct {
	// 会话与消息存储
	store Store
	// 为 Session / Message 发放编号与时间
	factory *core.Factory
	// 本轮如何生成助手回复
	responder Responder
}

// 绑定存储、发号器与 Responder；依赖在方法入口校验
func NewService(store Store, factory *core.Factory, responder Responder) *Service {
	return &Service{
		store:     store,
		factory:   factory,
		responder: responder,
	}
}

// 创建空会话并持久化，返回已写入的会话
func (s *Service) NewSession(ctx context.Context) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}
	if err := s.validate(); err != nil {
		return nil, err
	}

	id, err := s.factory.NewID("sess")
	if err != nil {
		return nil, fmt.Errorf("new session id: %w", err)
	}
	now := s.factory.Now()
	session := &Session{
		ID:        id,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return session, nil
}

// 处理一轮用户输入：校验会话 → 写 user → Responder → 可选 checkpoint → 写 assistant → 刷新 UpdatedAt
// History 传给 Responder 时不含本轮 user（见 RespondInput）
// CheckpointContent 非空时先落 ModeCheckpoint，再落助手回复；两者均进 Store 全量时间线
func (s *Service) Turn(ctx context.Context, sessionID, userText string) (TurnResult, error) {
	if err := ctx.Err(); err != nil {
		return TurnResult{}, fmt.Errorf("turn: %w", err)
	}
	if err := s.validate(); err != nil {
		return TurnResult{}, err
	}
	if sessionID == "" {
		return TurnResult{}, fmt.Errorf("turn: session id is required")
	}

	// 先确认会话存在，再读历史，避免对不存在会话写消息
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return TurnResult{}, fmt.Errorf("turn: %w", err)
	}

	// 历史取自写入本轮 user 之前，Responder 用 UserText 表示当前句
	history, err := s.store.ListMessages(ctx, sessionID)
	if err != nil {
		return TurnResult{}, fmt.Errorf("list messages: %w", err)
	}

	userMsg, err := s.newMessage(sessionID, RoleUser, userText, "", "")
	if err != nil {
		return TurnResult{}, err
	}
	if err = s.store.AppendMessage(ctx, &userMsg); err != nil {
		return TurnResult{}, fmt.Errorf("append user message: %w", err)
	}

	out, err := s.responder.Respond(ctx, RespondInput{
		SessionID: sessionID,
		UserText:  userText,
		History:   history,
	})
	if err != nil {
		return TurnResult{}, fmt.Errorf("respond: %w", err)
	}

	// L2 handoff：先写 checkpoint，再写助手回复；Store 仍保留压缩前的全量历史
	if cp := strings.TrimSpace(out.CheckpointContent); cp != "" {
		cpMsg, cpErr := s.newMessage(sessionID, RoleAssistant, cp, "", ModeCheckpoint)
		if cpErr != nil {
			return TurnResult{}, cpErr
		}
		if err = s.store.AppendMessage(ctx, &cpMsg); err != nil {
			return TurnResult{}, fmt.Errorf("append checkpoint message: %w", err)
		}
	}

	assistantMsg, err := s.newMessage(sessionID, RoleAssistant, out.Content, out.RunID, out.Mode)
	if err != nil {
		return TurnResult{}, err
	}
	if err = s.store.AppendMessage(ctx, &assistantMsg); err != nil {
		return TurnResult{}, fmt.Errorf("append assistant message: %w", err)
	}

	// 用助手消息时间刷新会话活跃时间
	session.UpdatedAt = assistantMsg.CreatedAt
	if err = s.store.UpdateSession(ctx, session); err != nil {
		return TurnResult{}, fmt.Errorf("update session: %w", err)
	}

	return TurnResult{
		UserMessage:      userMsg,
		AssistantMessage: assistantMsg,
		RunID:            out.RunID,
		Report:           out.Report,
	}, nil
}

// 用 Factory 组装一条消息实体（尚未落库）
func (s *Service) newMessage(sessionID, role, content, runID, mode string) (Message, error) {
	id, err := s.factory.NewID("msg")
	if err != nil {
		return Message{}, fmt.Errorf("new message id: %w", err)
	}
	return Message{
		ID:        id,
		SessionID: sessionID,
		Role:      role,
		Content:   content,
		CreatedAt: s.factory.Now(),
		RunID:     runID,
		Mode:      mode,
	}, nil
}

// 检查构造时注入的依赖是否齐全
func (s *Service) validate() error {
	if s == nil {
		return fmt.Errorf("session service is nil")
	}
	if s.store == nil {
		return fmt.Errorf("session store is nil")
	}
	if s.factory == nil {
		return fmt.Errorf("session factory is nil")
	}
	if s.responder == nil {
		return fmt.Errorf("session responder is nil")
	}
	return nil
}
