// Tower 注入模型的上下文视图：Store 全量历史为权威源；
// 进模型时预算内尽量全文，超预算做 L0/L1 压缩（#18），禁止 last-N 静默截断。
package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"aruing/internal/session"
)

const (
	// 用户侧 payload 默认 token 预算（估算单位）；预留 system/tools/输出
	defaultTowerContextBudgetTokens = 24_000
	// 单条消息 content 超过此估算则 L0 截断预览（完整仍在 Store）
	defaultMaxMessageContentTokens = 4_000
	// L0 截断后保留的估算 token 上限（预览窗口）
	defaultTruncatedPreviewTokens = 800
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
func extractPriorDiagnostics(history []session.Message) []towerPriorDiagnostic {
	out := make([]towerPriorDiagnostic, 0)
	for _, m := range history {
		if m.Role != session.RoleAssistant {
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

// 组装并在超预算时应用 L0→L1 compact，返回注入用 history 与 prior
func buildTowerContextView(
	history []session.Message,
	budgetTokens int,
	maxMsgTokens int,
	previewTokens int,
) (hist []towerHistMsg, priors []towerPriorDiagnostic) {
	if budgetTokens <= 0 {
		budgetTokens = defaultTowerContextBudgetTokens
	}
	if maxMsgTokens <= 0 {
		maxMsgTokens = defaultMaxMessageContentTokens
	}
	if previewTokens <= 0 {
		previewTokens = defaultTruncatedPreviewTokens
	}

	hist = messagesToHist(history)
	priors = extractPriorDiagnostics(history)

	if estimateHistTokens(hist)+estimatePriorTokens(priors) <= budgetTokens {
		return hist, priors
	}

	// L0：超长单条截断预览
	hist = compactL0(hist, maxMsgTokens, previewTokens)
	priors = extractPriorFromHist(hist)
	if estimateHistTokens(hist)+estimatePriorTokens(priors) <= budgetTokens {
		return hist, priors
	}

	// L1：诊断优先，折叠旧非诊断轮
	hist = compactL1(hist, budgetTokens, priors)
	priors = extractPriorFromHist(hist)
	return hist, priors
}

func extractPriorFromHist(hist []towerHistMsg) []towerPriorDiagnostic {
	out := make([]towerPriorDiagnostic, 0)
	for _, m := range hist {
		if m.Role != session.RoleAssistant {
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
