// Tower 注入模型的上下文视图：Store 全量历史为权威源；
// 进模型时预算内尽量全文，超预算做 L0/L1/L2 压缩（#18），禁止 last-N 静默截断。
package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"aruing/internal/llm"
	"aruing/internal/session"
)

//go:embed prompts/compact.md
var compactPromptTemplate string

const (
	// 用户侧 payload 默认 token 预算（估算单位）；预留 system/tools/输出
	defaultTowerContextBudgetTokens = 24_000
	// 单条消息 content 超过此估算则 L0 截断预览（完整仍在 Store）
	defaultMaxMessageContentTokens = 4_000
	// L0 截断后保留的估算 token 上限（预览窗口）
	defaultTruncatedPreviewTokens = 800
	// L2 时近期 turn 原文条数（从尾部保留）
	defaultL2KeepRecentMessages = 6
	// L2 后若仍超预算，对 handoff 摘要的截断预览 token
	defaultL2SummaryPreviewTokens = 1_200
)

// 写入 prompt 的精简历史项
type towerHistMsg struct {
	// 消息角色
	Role string `json:"role"`
	// 消息正文（可能经 compact 折叠/截断）
	Content string `json:"content"`
	// 可选展示模式
	Mode string `json:"mode,omitempty"`
	// 可选关联诊断 Run
	RunID string `json:"runId,omitempty"`
}

// 会话内既有诊断摘要，供解释路径引用（无条数 cap，体积由预算治理）
type towerPriorDiagnostic struct {
	// 诊断 Run 编号
	RunID string `json:"run_id"`
	// 该助手消息正文（已落库的诊断摘要）
	Summary string `json:"summary"`
}

// 一轮 Respond 的模型侧上下文视图；L2 时附带可落库的 checkpoint 正文
type towerContextView struct {
	// 注入 history 字段
	Hist []towerHistMsg
	// 注入 prior_diagnostics
	Priors []towerPriorDiagnostic
	// L2 handoff 摘要；非空时 Turn 写入 ModeCheckpoint 消息
	CheckpointContent string
}

// L2 GenerateJSON 输出
type compactLLMOutput struct {
	// 接续摘要
	Summary string `json:"summary"`
	// 旧段中的 run id
	RunIDs []string `json:"run_ids"`
	// 未决问题
	OpenQuestions []string `json:"open_questions"`
}

// 估算文本占用的 token（字符近似，非精确计费）
func estimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	return (n + 3) / 4
}

func estimateHistTokens(msgs []towerHistMsg) int {
	total := 0
	for _, m := range msgs {
		total += estimateTokens(m.Role) + estimateTokens(m.Content) +
			estimateTokens(m.Mode) + estimateTokens(m.RunID) + 8
	}
	return total
}

func estimatePriorTokens(priors []towerPriorDiagnostic) int {
	total := 0
	for _, p := range priors {
		total += estimateTokens(p.RunID) + estimateTokens(p.Summary) + 8
	}
	return total
}

// 从全量历史提取 diagnostic 助手消息；无条数上限
// checkpoint 不进 prior（有 runId 的诊断摘要才算）
func extractPriorDiagnostics(history []session.Message) []towerPriorDiagnostic {
	out := make([]towerPriorDiagnostic, 0)
	for _, m := range history {
		if m.Role != session.RoleAssistant {
			continue
		}
		if m.Mode == session.ModeCheckpoint {
			continue
		}
		if m.Mode != session.ModeDiagnostic && strings.TrimSpace(m.RunID) == "" {
			continue
		}
		runID := strings.TrimSpace(m.RunID)
		summary := strings.TrimSpace(m.Content)
		if runID == "" && summary == "" {
			continue
		}
		out = append(out, towerPriorDiagnostic{RunID: runID, Summary: summary})
	}
	return out
}

// 全量 history → histMsg；不做条数截断
func messagesToHist(history []session.Message) []towerHistMsg {
	msgs := make([]towerHistMsg, 0, len(history))
	for _, m := range history {
		msgs = append(msgs, towerHistMsg{
			Role:    m.Role,
			Content: m.Content,
			Mode:    m.Mode,
			RunID:   m.RunID,
		})
	}
	return msgs
}

// 组装并在超预算时应用 L0→L1→L2 compact。
// client 可为 nil：跳过 L2，仅 L0/L1（单测与无 LLM 路径）。
// L2 成功时 CheckpointContent 非空，供 Turn 落 ModeCheckpoint。
func prepareTowerContext(
	ctx context.Context,
	client llm.Client,
	history []session.Message,
	budgetTokens int,
	maxMsgTokens int,
	previewTokens int,
) (towerContextView, error) {
	if budgetTokens <= 0 {
		budgetTokens = defaultTowerContextBudgetTokens
	}
	if maxMsgTokens <= 0 {
		maxMsgTokens = defaultMaxMessageContentTokens
	}
	if previewTokens <= 0 {
		previewTokens = defaultTruncatedPreviewTokens
	}

	hist := messagesToHist(history)
	priors := extractPriorDiagnostics(history)

	if estimateHistTokens(hist)+estimatePriorTokens(priors) <= budgetTokens {
		return towerContextView{Hist: hist, Priors: priors}, nil
	}

	// L0：超长单条截断预览
	hist = compactL0(hist, maxMsgTokens, previewTokens)
	priors = extractPriorFromHist(hist)
	if estimateHistTokens(hist)+estimatePriorTokens(priors) <= budgetTokens {
		return towerContextView{Hist: hist, Priors: priors}, nil
	}

	// L1：诊断优先，折叠旧非诊断轮
	hist = compactL1(hist, budgetTokens, priors)
	priors = extractPriorFromHist(hist)
	if estimateHistTokens(hist)+estimatePriorTokens(priors) <= budgetTokens {
		return towerContextView{Hist: hist, Priors: priors}, nil
	}

	// L2：handoff summary + 近期原文
	if client == nil {
		return towerContextView{Hist: hist, Priors: priors}, nil
	}
	return compactL2(ctx, client, hist, budgetTokens)
}

// 兼容旧测试与无 LLM 路径：仅 L0/L1
func buildTowerContextView(
	history []session.Message,
	budgetTokens int,
	maxMsgTokens int,
	previewTokens int,
) (hist []towerHistMsg, priors []towerPriorDiagnostic) {
	view, err := prepareTowerContext(context.Background(), nil, history, budgetTokens, maxMsgTokens, previewTokens)
	if err != nil {
		// nil client 路径不应失败
		return messagesToHist(history), extractPriorDiagnostics(history)
	}
	return view.Hist, view.Priors
}

func extractPriorFromHist(hist []towerHistMsg) []towerPriorDiagnostic {
	out := make([]towerPriorDiagnostic, 0)
	for _, m := range hist {
		if m.Role != session.RoleAssistant {
			continue
		}
		if m.Mode == session.ModeCheckpoint {
			continue
		}
		if m.Mode != session.ModeDiagnostic && strings.TrimSpace(m.RunID) == "" {
			continue
		}
		if strings.HasPrefix(m.Content, "[folded]") {
			continue
		}
		runID := strings.TrimSpace(m.RunID)
		summary := strings.TrimSpace(m.Content)
		if runID == "" && summary == "" {
			continue
		}
		out = append(out, towerPriorDiagnostic{RunID: runID, Summary: summary})
	}
	return out
}

func isDiagnosticHist(m towerHistMsg) bool {
	return m.Role == session.RoleAssistant &&
		(m.Mode == session.ModeDiagnostic || strings.TrimSpace(m.RunID) != "")
}

// L0：单条 content 超长则截断并标注 truncated（完整仍在 Store）
func compactL0(hist []towerHistMsg, maxMsgTokens, previewTokens int) []towerHistMsg {
	out := make([]towerHistMsg, len(hist))
	copy(out, hist)
	for i := range out {
		if estimateTokens(out[i].Content) <= maxMsgTokens {
			continue
		}
		out[i].Content = truncateContentPreview(out[i].Content, previewTokens)
	}
	return out
}

func truncateContentPreview(content string, previewTokens int) string {
	runes := []rune(content)
	// previewTokens * 4 约等于 rune 数
	maxRunes := previewTokens * 4
	if maxRunes <= 0 {
		maxRunes = 200
	}
	if len(runes) <= maxRunes {
		return content
	}
	return string(runes[:maxRunes]) + fmt.Sprintf(
		"\n…[truncated, full message retained in store; shown %d/%d runes]",
		maxRunes, len(runes))
}

// L1：从最旧开始折叠非诊断消息为一行摘要，尽量保留 diagnostic 全文
// 无法再压仍超预算时停止（保留骨架；Store 仍全量，#18）
func compactL1(hist []towerHistMsg, budgetTokens int, _ []towerPriorDiagnostic) []towerHistMsg {
	if len(hist) == 0 {
		return hist
	}
	out := make([]towerHistMsg, len(hist))
	copy(out, hist)

	const maxPasses = 10_000
	for pass := 0; pass < maxPasses && estimateHistTokens(out) > budgetTokens; pass++ {
		// 优先折叠最旧非诊断
		if idx := firstUnfoldedNonDiagnostic(out); idx >= 0 {
			out[idx].Content = foldLine(out[idx])
			continue
		}
		// 再截最旧未截诊断
		if idx := firstUntruncatedDiagnostic(out); idx >= 0 {
			prev := out[idx].Content
			out[idx].Content = truncateContentPreview(prev, defaultTruncatedPreviewTokens/2)
			if out[idx].Content == prev {
				// 已无法再缩：强制标记，避免死循环
				out[idx].Content = forceCompactMark(prev)
			}
			continue
		}
		// 无可折可截
		break
	}
	return out
}

func firstUnfoldedNonDiagnostic(hist []towerHistMsg) int {
	for i, m := range hist {
		if isDiagnosticHist(m) {
			continue
		}
		if m.Mode == session.ModeCheckpoint {
			// checkpoint 可折叠以腾空间
			if strings.HasPrefix(m.Content, "[folded]") {
				continue
			}
			return i
		}
		if strings.HasPrefix(m.Content, "[folded]") {
			continue
		}
		return i
	}
	return -1
}

func firstUntruncatedDiagnostic(hist []towerHistMsg) int {
	for i, m := range hist {
		if !isDiagnosticHist(m) {
			continue
		}
		if strings.Contains(m.Content, "[truncated") || strings.HasPrefix(m.Content, "[folded]") {
			continue
		}
		return i
	}
	return -1
}

func forceCompactMark(content string) string {
	runes := []rune(strings.TrimSpace(content))
	const n = 40
	if len(runes) > n {
		return string(runes[:n]) + "…[truncated, full message retained in store]"
	}
	if content == "" {
		return "[truncated, full message retained in store]"
	}
	return content + "\n…[truncated, full message retained in store]"
}

func foldLine(m towerHistMsg) string {
	preview := strings.TrimSpace(m.Content)
	runes := []rune(preview)
	if len(runes) > 80 {
		preview = string(runes[:80]) + "…"
	}
	mode := m.Mode
	if mode == "" {
		mode = "-"
	}
	runID := m.RunID
	if runID == "" {
		runID = "-"
	}
	return fmt.Sprintf("[folded] %s mode=%s runId=%s | %s", m.Role, mode, runID, preview)
}

// L2：对装不下的旧段做 handoff summary，保留近期原文；返回 checkpoint 正文供落库
func compactL2(
	ctx context.Context,
	client llm.Client,
	hist []towerHistMsg,
	budgetTokens int,
) (towerContextView, error) {
	if len(hist) == 0 {
		return towerContextView{Hist: hist, Priors: nil}, nil
	}

	keepN := defaultL2KeepRecentMessages
	if keepN > len(hist) {
		keepN = len(hist)
	}
	split := len(hist) - keepN
	if split <= 0 {
		// 无旧段可摘要：再截近期
		hist = compactL1(hist, budgetTokens, nil)
		return towerContextView{Hist: hist, Priors: extractPriorFromHist(hist)}, nil
	}

	oldSeg := hist[:split]
	recent := hist[split:]

	summary, err := generateHandoffSummary(ctx, client, oldSeg)
	if err != nil {
		return towerContextView{}, err
	}

	checkpointBody := formatCheckpointContent(summary)
	merged := make([]towerHistMsg, 0, 1+len(recent))
	merged = append(merged, towerHistMsg{
		Role:    session.RoleAssistant,
		Mode:    session.ModeCheckpoint,
		Content: checkpointBody,
	})
	merged = append(merged, recent...)

	// 仍超预算：先压 recent 非诊断，再截 checkpoint
	if estimateHistTokens(merged)+estimatePriorTokens(extractPriorFromHist(merged)) > budgetTokens {
		merged = compactL1(merged, budgetTokens, nil)
	}
	if estimateHistTokens(merged) > budgetTokens {
		// 截 checkpoint 摘要本身
		for i := range merged {
			if merged[i].Mode == session.ModeCheckpoint {
				merged[i].Content = truncateContentPreview(merged[i].Content, defaultL2SummaryPreviewTokens)
				break
			}
		}
	}

	priors := extractPriorFromHist(merged)
	return towerContextView{
		Hist:              merged,
		Priors:            priors,
		CheckpointContent: checkpointBody,
	}, nil
}

func formatCheckpointContent(summary compactLLMOutput) string {
	var b strings.Builder
	b.WriteString("[checkpoint] session handoff summary\n")
	b.WriteString(strings.TrimSpace(summary.Summary))
	if len(summary.RunIDs) > 0 {
		b.WriteString("\nrun_ids: ")
		b.WriteString(strings.Join(summary.RunIDs, ", "))
	}
	if len(summary.OpenQuestions) > 0 {
		b.WriteString("\nopen_questions: ")
		b.WriteString(strings.Join(summary.OpenQuestions, "; "))
	}
	return b.String()
}

func generateHandoffSummary(
	ctx context.Context,
	client llm.Client,
	oldSeg []towerHistMsg,
) (compactLLMOutput, error) {
	// 旧段可能很大：序列化前对单条再压预览，避免 L2 请求本身爆窗
	seg := compactL0(oldSeg, defaultMaxMessageContentTokens, defaultTruncatedPreviewTokens)
	raw, err := json.Marshal(struct {
		Messages []towerHistMsg `json:"messages"`
	}{Messages: seg})
	if err != nil {
		return compactLLMOutput{}, fmt.Errorf("compact L2 marshal: %w", err)
	}

	var out compactLLMOutput
	if gErr := client.GenerateJSON(ctx, llm.Request{
		System: compactPromptTemplate,
		User:   string(raw),
	}, &out); gErr != nil {
		return compactLLMOutput{}, fmt.Errorf("compact L2: %w", gErr)
	}
	if strings.TrimSpace(out.Summary) == "" {
		// 回退：规则拼骨架，避免整轮失败
		out.Summary = fallbackHandoffSummary(oldSeg)
		out.RunIDs = collectRunIDs(oldSeg)
	}
	if out.RunIDs == nil {
		out.RunIDs = []string{}
	}
	if out.OpenQuestions == nil {
		out.OpenQuestions = []string{}
	}
	return out, nil
}

func collectRunIDs(hist []towerHistMsg) []string {
	seen := make(map[string]struct{})
	var ids []string
	for _, m := range hist {
		id := strings.TrimSpace(m.RunID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func fallbackHandoffSummary(hist []towerHistMsg) string {
	var parts []string
	for _, m := range hist {
		if !isDiagnosticHist(m) {
			continue
		}
		line := strings.TrimSpace(m.Content)
		runes := []rune(line)
		if len(runes) > 120 {
			line = string(runes[:120]) + "…"
		}
		if m.RunID != "" {
			parts = append(parts, fmt.Sprintf("run %s: %s", m.RunID, line))
		} else {
			parts = append(parts, line)
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("Earlier conversation (%d messages) compacted; full history retained in store.", len(hist))
	}
	return "Prior diagnostics: " + strings.Join(parts, " | ")
}
