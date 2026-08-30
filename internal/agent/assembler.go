// tier-aware 组装器：会话历史注入视图按信任类型分层组装（Algorithm 1 第 1–5 行）
//
// ours：V = R ∪ W ∪ C——R 索引卡锁定常驻（不参与替换，式 5）、W 最近 w 轮原文、
// C 中段叙事压缩（每段过 C1）；预算 R 按需取满、剩余 W/C 分配（式 4）。
// 回灌预算 b_r 沿用既有独立 defaultRehydrateBudgetTokens（不从 B 扣，与 Algorithm 的实现偏差，见 plan）
// D1 / D2 为显式配置的实验对照臂（纯记忆策略，无卡片无回灌）：
// D1 last-N 违反 #18，仅实验臂豁免，产品默认 ours
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Aruing/Aruing/internal/llm"
	"github.com/Aruing/Aruing/internal/session"
)

// 记忆组装方法名（config agent.memory.method）
type MemoryMethod string

const (
	// tier-aware 组装（产品默认；空配置等同本值）
	MemoryMethodOurs MemoryMethod = "ours"
	// D1 基线：只保留最近 N 条原文（codex 式断崖对照臂）
	MemoryMethodD1LastN MemoryMethod = "d1-last-n"
	// D2 基线：全历史压成一段摘要常驻（平铺摘要对照臂）
	MemoryMethodD2FlatSummary MemoryMethod = "d2-flat-summary"
)

const (
	// ours 组装的历史总预算 B：R+W+C 共享（沿用既有塔上下文预算口径）
	defaultTieredBudgetTokens = defaultTowerContextBudgetTokens
	// W 层保留的最近轮数（一轮以一条用户消息计，含其后至下一条用户消息前的助手消息）
	defaultWindowRounds = 3
	// W 层最小可用预算：卡片把总预算吃满时也不至于连一轮原文都放不下
	minimumTieredWindowTokens = 400
	// D1 默认保留条数（config agent.memory.last_n 可覆盖；实验扫描用）
	defaultLastN = 20
)

// 记忆组装参数；零值走各默认（预算与窗口不进 config：实验不扫，包内常量即可）
type memoryOptions struct {
	// 历史总预算 B（R+W+C 共享）；非正走默认
	budgetTokens int
	// W 层最近轮数；非正走默认
	windowRounds int
	// D1 last-N 保留条数；非正走默认
	lastN int
	// 卡片墙软预算（κ₂ 触发域观测；本版 κ₂ 为占位实现，仅作为接口注入点）
	cardBudget int
}

// 解析记忆组装方法：空 = ours（产品默认，一开关可达各实验臂）；未知值报错
// 装配层启动时调用，明确失败不静默回落（照 tools.projection / agent.acquire 先例）
func ParseMemoryMethod(s string) (MemoryMethod, error) {
	switch strings.TrimSpace(s) {
	case "", string(MemoryMethodOurs):
		return MemoryMethodOurs, nil
	case string(MemoryMethodD1LastN):
		return MemoryMethodD1LastN, nil
	case string(MemoryMethodD2FlatSummary):
		return MemoryMethodD2FlatSummary, nil
	default:
		return "", fmt.Errorf("unknown memory method %q (want ours | d1-last-n | d2-flat-summary)", strings.TrimSpace(s))
	}
}

// ours 组装：R 锁定常驻 + W 最近 w 轮原文 + C 中段压缩
// 返回注入视图（Hist = [checkpoint(C)] + W）与 R 层卡片（注入 prior_run_details）
// 预算内短会话全量注入、不产 checkpoint（与既有行为等价）
// 深层摘要失败时报错（由调用方决定应答失败，不静默丢中段）
func assembleTieredView(
	ctx context.Context,
	client llm.Client,
	history []session.Message,
	records []session.DiagnosticRecord,
	opts memoryOptions,
) (towerContextView, []towerPriorRunDetail, error) {
	if opts.budgetTokens <= 0 {
		opts.budgetTokens = defaultTieredBudgetTokens
	}
	if opts.windowRounds <= 0 {
		opts.windowRounds = defaultWindowRounds
	}

	// R 层：索引卡先组装（锁定常驻，不参与 W/C 预算竞争）；κ₂ 接口注入点（本版原样返回）
	cards := buildMemoryCards(records)
	cards = noopCardWallCompactor(cards, opts.cardBudget)
	cardTokens := estimatePriorRunTokens(cards)

	hist := messagesToHist(history)
	priors := extractPriorDiagnostics(history)
	// 预算内全量：短会话直接原文，不产 checkpoint
	if estimateHistTokens(hist)+estimatePriorTokens(priors)+cardTokens <= opts.budgetTokens {
		return towerContextView{Hist: hist, Priors: priors}, cards, nil
	}

	// W 层预算 = 总预算减卡片（R 先取满；卡片吃满时保最小窗口）
	avail := opts.budgetTokens - cardTokens
	if avail < minimumTieredWindowTokens {
		avail = minimumTieredWindowTokens
	}
	window := compactL0(
		takeWindowRounds(hist, opts.windowRounds, avail),
		defaultMaxMessageContentTokens, defaultTruncatedPreviewTokens)
	mid := hist[:len(hist)-len(window)]

	// C 层：中段压成单一交接摘要（复用 L2 摘要器；C1 兜地址，#129 起 L0 截断已保地址）
	var checkpointBody string
	merged := make([]towerHistMsg, 0, len(window)+1)
	if len(mid) == 0 {
		merged = append(merged, window...)
	} else if client == nil {
		// 无客户端路径（单测与降级）：中段退化为 L0/L1 压缩，不产 checkpoint
		compressed := compactL1(
			compactL0(mid, defaultMaxMessageContentTokens, defaultTruncatedPreviewTokens),
			avail-estimateHistTokens(window), nil)
		merged = append(merged, compressed...)
		merged = append(merged, window...)
	} else {
		seg := compactL0(mid, defaultMaxMessageContentTokens, defaultTruncatedPreviewTokens)
		summary, err := generateHandoffSummary(ctx, client, seg)
		if err != nil {
			return towerContextView{}, nil, fmt.Errorf("tiered mid compact: %w", err)
		}
		checkpointBody = ensureAddrCoverage(histAddrSource(seg), formatCheckpointContent(summary), nil)
		// 注入副本收进 W 之外的剩余预算（C1 保地址）；落库正文始终完整
		remaining := avail - estimateHistTokens(window)
		cpContent := checkpointBody
		if estimateTokens(cpContent) > remaining {
			cpContent = truncateContentPreview(cpContent, remaining)
		}
		merged = append(merged, towerHistMsg{
			Role:    session.RoleAssistant,
			Mode:    session.ModeCheckpoint,
			Content: cpContent,
		})
		merged = append(merged, window...)
	}

	return towerContextView{
		Hist:              merged,
		Priors:            extractPriorFromHist(merged),
		CheckpointContent: checkpointBody,
	}, cards, nil
}

// D1 基线：只保留最近 N 条原文（单条超长 L0 预览），无卡片 / 无 checkpoint / 无回灌
// codex 式断崖对照臂；Store 全量不动，仅注入视图裁剪（#18 实验臂豁免口径）
func assembleLastN(history []session.Message, lastN int) towerContextView {
	if lastN <= 0 {
		lastN = defaultLastN
	}
	if len(history) <= lastN {
		return towerContextView{Hist: messagesToHist(history), Priors: extractPriorDiagnostics(history)}
	}
	hist := messagesToHist(history[len(history)-lastN:])
	hist = compactL0(hist, defaultMaxMessageContentTokens, defaultTruncatedPreviewTokens)
	return towerContextView{Hist: hist, Priors: extractPriorFromHist(hist)}
}

// D2 基线：全历史压成一段摘要常驻，无窗口无卡片
// 复用 L2 交接摘要器（含空摘要规则回退）；注入副本收进预算，落库 checkpoint 正文完整
// 无客户端路径退化为 L0/L1（单测可用，不调模型）
func assembleFlatSummary(
	ctx context.Context,
	client llm.Client,
	history []session.Message,
	budgetTokens int,
) (towerContextView, error) {
	if budgetTokens <= 0 {
		budgetTokens = defaultTieredBudgetTokens
	}
	hist := messagesToHist(history)
	if len(hist) == 0 {
		return towerContextView{}, nil
	}
	if client == nil {
		compressed := compactL1(
			compactL0(hist, defaultMaxMessageContentTokens, defaultTruncatedPreviewTokens),
			budgetTokens, nil)
		return towerContextView{Hist: compressed, Priors: extractPriorFromHist(compressed)}, nil
	}
	seg := compactL0(hist, defaultMaxMessageContentTokens, defaultTruncatedPreviewTokens)
	summary, err := generateHandoffSummary(ctx, client, seg)
	if err != nil {
		return towerContextView{}, fmt.Errorf("flat summary compact: %w", err)
	}
	body := ensureAddrCoverage(histAddrSource(seg), formatCheckpointContent(summary), nil)
	cp := body
	if estimateTokens(cp) > budgetTokens {
		cp = truncateContentPreview(cp, budgetTokens)
	}
	return towerContextView{
		Hist:              []towerHistMsg{{Role: session.RoleAssistant, Mode: session.ModeCheckpoint, Content: cp}},
		CheckpointContent: body,
	}, nil
}

// 从尾部按轮取最近 w 轮原文（一轮 = 一条用户消息起至下一条用户消息前）
// 预算不足时从旧侧整轮丢弃；只剩最后一轮且仍超时不丢（截断由 L0 兜底，不静默丢段）
func takeWindowRounds(hist []towerHistMsg, rounds, budgetTokens int) []towerHistMsg {
	if len(hist) == 0 {
		return hist
	}
	if rounds <= 0 {
		rounds = defaultWindowRounds
	}
	lo := 0
	seen := 0
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Role == session.RoleUser {
			seen++
			if seen >= rounds {
				lo = i
				break
			}
		}
	}
	// 从旧侧整轮丢弃直到进预算；保最后一轮
	for lo < len(hist) && estimateHistTokens(hist[lo:]) > budgetTokens {
		next := lo + 1
		for next < len(hist) && hist[next].Role != session.RoleUser {
			next++
		}
		if next >= len(hist) {
			break
		}
		lo = next
	}
	return hist[lo:]
}

// 估算索引卡列表的注入词元（对齐 prior_run_details 注入形状的字段开销）
func estimatePriorRunTokens(details []towerPriorRunDetail) int {
	total := 0
	for _, d := range details {
		total += estimateTokens(d.RunID) + estimateTokens(d.Question) +
			estimateTokens(d.Title) + estimateTokens(d.Summary) + 8
		for _, c := range d.Conclusions {
			total += estimateTokens(c.Result) + estimateTokens(c.Reason) +
				estimateTokens(strings.Join(c.EvidenceIDs, ",")) + 4
		}
		for _, s := range d.Suggestions {
			total += estimateTokens(s) + 2
		}
		for _, e := range d.Evidence {
			total += estimateTokens(e.ID) + estimateTokens(e.ToolName) +
				estimateTokens(e.Summary) + estimateTokens(e.CommandView) +
				estimateTokens(e.Error) + 4
		}
	}
	return total
}
