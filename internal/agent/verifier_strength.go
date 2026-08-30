// 验证器的强度判定兼职：对一条富文本证据输出各假设的 (d,s) 方向与强度
//
// 与正式 Verify 的分工（冻结裁决）：决策循环内每观测一次做一次轻调用，
// 产出贝叶斯强度更新的素材；MSPRT 停止后仍由 Verify 出正式 Verdict，
// 两者语义同源但互不替代——本输出不进 Verdict / Evidence 链
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

//go:embed prompts/strength.md
var strengthPrompt string

// 请求模型对每条假设判定给定证据的方向与强度，供决策循环做强度贝叶斯更新
//
// 逐假设恰好一条（漏判 / 错枚举会让更新静默失真，严格校验并在业务级重试内
// 重新请求，与 Verify 同口径）；强度越界由解析层钳位（LLM 输出容错），不触发重试
func (v *LLMVerifier) JudgeStrength(
	ctx context.Context,
	evidence core.Evidence,
	hypotheses []core.Hypothesis,
) ([]StrengthJudgement, error) {
	if ctx == nil {
		return nil, errors.New("strength judgement requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("judge strength: %w", err)
	}
	if v == nil {
		return nil, errors.New("verifier is required")
	}
	if len(hypotheses) == 0 {
		return nil, errors.New("strength judgement requires at least one hypothesis")
	}
	for i, h := range hypotheses {
		if strings.TrimSpace(h.ID) == "" {
			return nil, fmt.Errorf("hypothesis[%d] id is required", i)
		}
	}
	if strings.TrimSpace(evidence.ID) == "" {
		return nil, errors.New("strength judgement requires an evidence ID")
	}

	userPayload, err := buildStrengthUserPayload(evidence, hypotheses)
	if err != nil {
		return nil, fmt.Errorf("build strength prompt: %w", err)
	}
	req := llm.Request{System: strengthPrompt, User: userPayload}

	hypothesisIDs := make([]string, len(hypotheses))
	for i, h := range hypotheses {
		hypothesisIDs[i] = h.ID
	}

	// 与决策规划器同构：先收原始 JSON 再进解析校验合一的容错层
	var lastRaw json.RawMessage
	var lastValidateErr error
	for attempt := 0; attempt < maxVerifyAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("judge strength: %w", err)
		}

		var raw json.RawMessage
		if gErr := v.client.GenerateJSON(ctx, req, &raw); gErr != nil {
			return nil, fmt.Errorf("judge strength with LLM: %w", gErr)
		}

		judgements, pErr := parseStrengthOutput(raw, hypothesisIDs)
		if pErr != nil {
			lastRaw = raw
			lastValidateErr = pErr
			continue
		}
		return judgements, nil
	}

	return nil, fmt.Errorf("%w: strength output invalid: %v, last output: %s",
		ErrLLMOutputInconsistent, lastValidateErr, previewRaw(lastRaw))
}

// 序列化强度判定输入：待判定证据 + 全部候选假设
// 证据带原始输出（富文本判定的对象就是 raw），假设带语句与预期信号供对照；
// 任务上下文不进载荷（强度判定只关心证据内容与假设的关系）
func buildStrengthUserPayload(evidence core.Evidence, hypotheses []core.Hypothesis) (string, error) {
	// 单条猜想的精简视图
	type hypView struct {
		// 假设系统编号，判定必须按此回填
		ID string `json:"id"`
		// 假设陈述
		Statement string `json:"statement"`
		// 提出理由
		Reason string `json:"reason,omitempty"`
		// 预期信号列表
		ExpectedSignals []string `json:"expectedSignals,omitempty"`
	}
	// 待判定证据视图
	type evidenceView struct {
		// 证据系统编号
		ID string `json:"id"`
		// 产出任务编号
		TaskID string `json:"taskId"`
		// 工具名
		ToolName string `json:"toolName"`
		// 可展示命令视图
		CommandView string `json:"commandView,omitempty"`
		// 摘要
		Summary string `json:"summary"`
		// 失败错误，成功时可空
		Error string `json:"error,omitempty"`
		// 原始输出片段
		Raw json.RawMessage `json:"raw,omitempty"`
	}
	// 强度判定完整用户载荷
	type payload struct {
		// 待判定的单条证据
		Evidence evidenceView `json:"evidence"`
		// 候选假设列表，逐条判定
		Hypotheses []hypView `json:"hypotheses"`
	}

	p := payload{
		Evidence: evidenceView{
			ID:          evidence.ID,
			TaskID:      evidence.TaskID,
			ToolName:    evidence.ToolName,
			CommandView: evidence.CommandView,
			Summary:     evidence.Summary,
			Error:       evidence.Error,
			Raw:         append(json.RawMessage(nil), evidence.Raw...),
		},
		Hypotheses: make([]hypView, 0, len(hypotheses)),
	}
	for _, h := range hypotheses {
		p.Hypotheses = append(p.Hypotheses, hypView{
			ID:              h.ID,
			Statement:       h.Statement,
			Reason:          h.Reason,
			ExpectedSignals: append([]string(nil), h.ExpectedSignals...),
		})
	}

	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// 截取模型原文片段进错误信息，避免超长输出塞满错误链
func previewRaw(raw json.RawMessage) string {
	const limit = 200
	if len(raw) <= limit {
		return string(raw)
	}
	return string(raw[:limit]) + "..."
}
