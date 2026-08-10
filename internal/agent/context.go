// 基线塔注入模型的上下文视图：存储层全量历史为权威源；
// 进模型时预算内尽量全文，超预算做分层压缩，禁止按固定条数静默截断
//
// 调用约定：应答入口准备上下文一次；工具环复用同一视图
// 深层压缩成功时产出检查点正文，由会话轮次落检查点消息
// 客户端为空时跳过深层压缩，仅做浅层压缩（单测与无大模型路径）
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
	// 用户侧载荷默认词元预算（估算单位）；预留系统提示、工具说明与输出
	defaultTowerContextBudgetTokens = 24_000
	// 单条消息正文超过此估算则浅层截断预览（完整仍在存储层）
	defaultMaxMessageContentTokens = 4_000
	// 浅层截断后保留的估算词元上限（预览窗口）
	defaultTruncatedPreviewTokens = 800
	// 深层压缩时近期轮次原文条数（从尾部保留）
	defaultL2KeepRecentMessages = 6
	// 深层压缩后若仍超预算，对交接摘要的截断预览词元
	defaultL2SummaryPreviewTokens = 1_200
)

// 写入提示词的精简历史项
// 字段可能经压缩折叠或截断；完整原文仍在存储层
type towerHistMsg struct {
	// 消息角色（用户或助手）
	Role string `json:"role"`
	// 消息正文（可能经压缩折叠或截断）
	Content string `json:"content"`
	// 可选展示模式（基线、诊断或检查点）
	Mode string `json:"mode,omitempty"`
	// 可选关联诊断运行编号
	RunID string `json:"runId,omitempty"`
}

// 会话内既有诊断摘要，供解释路径引用
// 无条数上限，体积由预算统一治理
type towerPriorDiagnostic struct {
	// 诊断运行编号
	RunID string `json:"run_id"`
	// 该助手消息正文（已落库的诊断摘要）
	Summary string `json:"summary"`
}

// 一轮应答的模型侧上下文视图
// 深层压缩时附带可落库的检查点正文
type towerContextView struct {
	// 注入历史字段的消息列表
	Hist []towerHistMsg
	// 注入先前诊断字段的诊断摘要
	Priors []towerPriorDiagnostic
	// 深层压缩交接摘要；非空时会话轮次写入检查点消息
	CheckpointContent string
}

// 深层压缩调用结构化生成时的输出
type compactLLMOutput struct {
	// 接续摘要正文
	Summary string `json:"summary"`
	// 旧段中出现的诊断运行编号
	RunIDs []string `json:"run_ids"`
	// 未决问题列表
	OpenQuestions []string `json:"open_questions"`
}

// 按字符近似估算文本占用的词元，非精确计费
// 约四个字符单元计一词元，仅用于预算比较
func estimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	if n == 0 {
		return 0
	}
	return (n + 3) / 4
}

// 估算历史列表的词元总量（含角色等字段开销）
func estimateHistTokens(msgs []towerHistMsg) int {
	total := 0
	for _, m := range msgs {
		total += estimateTokens(m.Role) + estimateTokens(m.Content) +
			estimateTokens(m.Mode) + estimateTokens(m.RunID) + 8
	}
	return total
}

// 估算先前诊断列表的词元总量
func estimatePriorTokens(priors []towerPriorDiagnostic) int {
	total := 0
	for _, p := range priors {
		total += estimateTokens(p.RunID) + estimateTokens(p.Summary) + 8
	}
	return total
}

// 从全量历史提取诊断助手消息，无条数上限
// 检查点不进先前诊断；须有诊断模式或非空运行编号
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

// 将全量历史转为历史消息视图，不做条数截断
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

// 组装注入模型的上下文视图，超预算时依次应用浅层到深层压缩
// 客户端可为空：跳过深层压缩（单测与无大模型路径）
// 深层压缩成功时检查点正文非空，供会话轮次落检查点
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

	// 预算内：全文注入，不做任何压缩
	if estimateHistTokens(hist)+estimatePriorTokens(priors) <= budgetTokens {
		return towerContextView{Hist: hist, Priors: priors}, nil
	}

	// 浅层：单条超长截断预览，完整消息仍在存储层
	hist = compactL0(hist, maxMsgTokens, previewTokens)
	priors = extractPriorFromHist(hist)
	if estimateHistTokens(hist)+estimatePriorTokens(priors) <= budgetTokens {
		return towerContextView{Hist: hist, Priors: priors}, nil
	}

	// 中层：诊断优先，折叠旧非诊断轮
	hist = compactL1(hist, budgetTokens, priors)
	priors = extractPriorFromHist(hist)
	if estimateHistTokens(hist)+estimatePriorTokens(priors) <= budgetTokens {
		return towerContextView{Hist: hist, Priors: priors}, nil
	}

	// 深层：交接摘要加近期原文；无客户端则停在中层结果
	if client == nil {
		return towerContextView{Hist: hist, Priors: priors}, nil
	}
	return compactL2(ctx, client, hist, budgetTokens)
}

// 兼容旧测试与无大模型路径：仅走浅层与中层，不触发深层压缩
func buildTowerContextView(
	history []session.Message,
	budgetTokens int,
	maxMsgTokens int,
	previewTokens int,
) (hist []towerHistMsg, priors []towerPriorDiagnostic) {
	view, err := prepareTowerContext(context.Background(), nil, history, budgetTokens, maxMsgTokens, previewTokens)
	if err != nil {
		// 空客户端路径不应失败；回退为未压缩视图
		return messagesToHist(history), extractPriorDiagnostics(history)
	}
	return view.Hist, view.Priors
}

// 从已压缩的历史重生先前诊断，规则与从全量历史提取一致
// 额外跳过折叠行，避免把骨架摘要当诊断结论
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

// 判断是否为正式诊断助手消息（诊断模式或带运行编号）
// 检查点不算诊断消息
func isDiagnosticHist(m towerHistMsg) bool {
	if m.Mode == session.ModeCheckpoint {
		return false
	}
	return m.Role == session.RoleAssistant &&
		(m.Mode == session.ModeDiagnostic || strings.TrimSpace(m.RunID) != "")
}

// 浅层：单条正文超长则截断并标注截断
// 完整正文仍在存储层，此处只改注入模型的视图
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

// 按预览词元上限截断正文，并附截断标记说明存储层仍全量
func truncateContentPreview(content string, previewTokens int) string {
	runes := []rune(content)
	// 预览词元乘四约等于字符数，与估算对齐
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

// 中层：从最旧开始折叠非诊断消息，尽量保留诊断全文
// 无法再压仍超预算时停止；存储层仍全量
func compactL1(hist []towerHistMsg, budgetTokens int, _ []towerPriorDiagnostic) []towerHistMsg {
	if len(hist) == 0 {
		return hist
	}
	out := make([]towerHistMsg, len(hist))
	copy(out, hist)

	// 有限轮次避免异常输入下死循环
	const maxPasses = 10_000
	for pass := 0; pass < maxPasses && estimateHistTokens(out) > budgetTokens; pass++ {
		// 优先折叠最旧非诊断（含检查点），诊断尽量不折叠
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

// 返回最旧尚未折叠的非诊断下标；检查点视为可折叠
// 无候选时返回负一
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

// 返回最旧尚未截断或折叠的诊断消息下标
// 无候选时返回负一
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

// 在已无法按预览窗口再缩时打截断标记，防止中层压缩死循环
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

// 将单条消息压成一行折叠骨架，保留角色、模式、运行编号与短预览
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

// 深层：对装不下的旧段做交接摘要，保留近期原文
// 返回的检查点正文供会话轮次落库；注入视图为检查点加近期原文
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
		// 无旧段可摘要：回退中层再压近期
		hist = compactL1(hist, budgetTokens, nil)
		return towerContextView{Hist: hist, Priors: extractPriorFromHist(hist)}, nil
	}

	oldSeg := hist[:split]
	recent := hist[split:]

	summary, err := generateHandoffSummary(ctx, client, oldSeg)
	if err != nil {
		return towerContextView{}, err
	}

	// 注入视图与落库正文同源，便于下一轮历史识别检查点
	checkpointBody := formatCheckpointContent(summary)
	merged := make([]towerHistMsg, 0, 1+len(recent))
	merged = append(merged, towerHistMsg{
		Role:    session.RoleAssistant,
		Mode:    session.ModeCheckpoint,
		Content: checkpointBody,
	})
	merged = append(merged, recent...)

	// 仍超预算：优先压近期；检查点最后才截注入视图（落库始终完整）
	merged = fitMergedL2View(merged, budgetTokens, checkpointBody)

	priors := extractPriorFromHist(merged)
	return towerContextView{
		Hist:              merged,
		Priors:            priors,
		CheckpointContent: checkpointBody,
	}, nil
}

// 深层合并后压预算：先折或截近期，不动检查点；仍超才截检查点注入正文
// 截断始终以完整检查点正文为源，避免先折叠再截断丢失交接内容
// 检查点完整正文由调用方单独持有，本函数只改注入历史
func fitMergedL2View(merged []towerHistMsg, budgetTokens int, checkpointBody string) []towerHistMsg {
	if len(merged) == 0 {
		return merged
	}
	out := make([]towerHistMsg, len(merged))
	copy(out, merged)

	const maxPasses = 10_000
	for pass := 0; pass < maxPasses && estimateHistTokens(out) > budgetTokens; pass++ {
		// 优先折近期非诊断，跳过检查点
		if idx := firstUnfoldedNonDiagnosticSkipCheckpoint(out); idx >= 0 {
			out[idx].Content = foldLine(out[idx])
			continue
		}
		// 再截近期诊断
		if idx := firstUntruncatedDiagnostic(out); idx >= 0 {
			prev := out[idx].Content
			out[idx].Content = truncateContentPreview(prev, defaultTruncatedPreviewTokens/2)
			if out[idx].Content == prev {
				out[idx].Content = forceCompactMark(prev)
			}
			continue
		}
		break
	}

	// 近期已尽力仍超：才截检查点注入视图（源用完整正文）
	if estimateHistTokens(out) > budgetTokens {
		for i := range out {
			if out[i].Mode != session.ModeCheckpoint {
				continue
			}
			out[i].Content = truncateContentPreview(checkpointBody, defaultL2SummaryPreviewTokens)
			break
		}
	}
	return out
}

// 返回最旧尚未折叠的非诊断下标，跳过检查点
// 供深层压缩后压预算：先腾近期空间；无候选时返回负一
func firstUnfoldedNonDiagnosticSkipCheckpoint(hist []towerHistMsg) int {
	for i, m := range hist {
		if m.Mode == session.ModeCheckpoint {
			continue
		}
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

// 把深层压缩结构化输出格式化为可落库的检查点正文
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

// 调用大模型对旧段生成交接摘要
// 序列化前对单条再压预览，避免深层请求本身爆窗；摘要为空时规则回退
func generateHandoffSummary(
	ctx context.Context,
	client llm.Client,
	oldSeg []towerHistMsg,
) (compactLLMOutput, error) {
	// 旧段可能很大：先浅层压单条，控制深层请求体积
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
		// 模型空摘要时用规则骨架，避免整轮应答失败
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

// 从历史中按出现顺序收集去重后的运行编号
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

// 模型摘要为空时的规则回退：拼诊断要点骨架，保证检查点非空
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
