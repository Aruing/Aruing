// 按范围回灌：全局压缩丢失旧文后，按用户问题从权威存储定位相关区间、
// 回灌该段原文、必要时只压该窗，作为独立字段注入模型载荷
//
// 触发门只看物理事实（视图是否因压缩丢细节），不猜用户意图，避免漏指代式追问
// 定位规则优先（运行编号锚定、关键词与诊断重叠），规则不中且客户端可用时大模型兜底
// 定位、回灌与窗内压缩均为纯函数，不写库、不持状态：
// 未来换磁盘存储或向量检索只换函数内部，外部接线不动
//
// 回灌的消息原文是对话叙述（不可信），不得当新证据或判决；权威诊断事实仍在诊断账本
package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Aruing/Aruing/internal/llm"
	"github.com/Aruing/Aruing/internal/session"
)

//go:embed prompts/locate.md
var locatePromptTemplate string

const (
	// 回灌窗注入模型的独立子预算（与观察、先前证据同量级）
	// 窗内原文超此预算时由区间压缩折叠或截断到预算内，不静默丢段
	defaultRehydrateBudgetTokens = 8_000
	// 时间线大纲里每条消息预览的字符上限，控制大模型定位请求体积
	defaultTimelinePreviewRunes = 80
)

// 匹配文本里可能的诊断运行编号；锚定时再核对是否真实存在于历史
var runIDRe = regexp.MustCompile(`run_[0-9a-z]+`)

// 回灌窗内的单条消息，带时间线下标便于模型定位是哪一步
type rehydratedMsg struct {
	// 在会话历史中的下标（从零开始），仅用于模型指代，不作业务编号
	Idx int `json:"idx"`
	// 消息角色（用户或助手）
	Role string `json:"role"`
	// 消息正文（可能经区间压缩折叠或截断）
	Content string `json:"content"`
	// 可选展示模式（基线、诊断或检查点）
	Mode string `json:"mode,omitempty"`
	// 可选关联诊断运行编号
	RunID string `json:"runId,omitempty"`
}

// 判断默认视图是否因压缩丢失了旧文细节
// 命中即说明存在「可能需要回灌」的物理条件；是否真回灌由定位决定
// 只看事实不看意图，避免漏掉指代式追问
func viewCompactedAwayDetail(view towerContextView) bool {
	if strings.TrimSpace(view.CheckpointContent) != "" {
		return true
	}
	for _, h := range view.Hist {
		if strings.Contains(h.Content, "[folded]") || strings.Contains(h.Content, "[truncated") {
			return true
		}
	}
	return false
}

// 定位回灌区间（历史下标，闭区间），未命中返回否
// 规则优先（运行编号锚定、关键词与诊断标题重叠），规则不中且客户端可用时大模型兜底
// 客户端为空且规则未中时返回未命中，调用方据此不注入
func locateRange(
	ctx context.Context,
	client llm.Client,
	userText string,
	history []session.Message,
) (lo, hi int, ok bool) {
	if len(history) == 0 {
		return 0, 0, false
	}
	if l, h, hit := ruleLocateRange(userText, history); hit {
		return l, h, true
	}
	if client == nil {
		return 0, 0, false
	}
	return llmLocateRange(ctx, client, userText, history)
}

// 规则定位：运行编号锚定优先，其次关键词与诊断标题重叠，均不调大模型
func ruleLocateRange(userText string, history []session.Message) (int, int, bool) {
	// 运行编号锚定：用户提到的运行编号真实存在时，直接定位该诊断块
	if rid := runIDInText(userText, history); rid != "" {
		for i, m := range history {
			if m.RunID == rid {
				lo, hi := expandAround(len(history), i)
				return lo, hi, true
			}
		}
	}
	// 关键词分支：仅在含追问措辞时，按诊断消息标题重叠选最佳块
	if !referencesPastStep(userText) {
		return 0, 0, false
	}
	bestIdx, bestScore := -1, 0
	for i, m := range history {
		if !isDiagnosticMessage(m) {
			continue
		}
		score := relevanceScore(userText, m.Content)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return 0, 0, false
	}
	lo, hi := expandAround(len(history), bestIdx)
	return lo, hi, true
}

// 返回文本里第一个真实存在于历史中的运行编号，避免误锚定无关字面量
func runIDInText(userText string, history []session.Message) string {
	known := make(map[string]struct{})
	for _, m := range history {
		if id := strings.TrimSpace(m.RunID); id != "" {
			known[id] = struct{}{}
		}
	}
	for _, c := range runIDRe.FindAllString(userText, -1) {
		c = strings.TrimSpace(c)
		if _, ok := known[c]; ok {
			return c
		}
	}
	return ""
}

// 追问过去步骤的措辞信号（非封闭意图表，仅作关键词分支的便宜闸）
func referencesPastStep(userText string) bool {
	keywords := []string{
		"之前", "上次", "刚才", "某步", "那一步", "这一步",
		"当时", "回顾", "再讲讲", "展开", "again", "earlier", "previous",
	}
	lower := strings.ToLower(userText)
	for _, k := range keywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// 判断是否为正式诊断助手消息（诊断模式或带运行编号），检查点不算
func isDiagnosticMessage(m session.Message) bool {
	if m.Mode == session.ModeCheckpoint {
		return false
	}
	return m.Role == session.RoleAssistant &&
		(m.Mode == session.ModeDiagnostic || strings.TrimSpace(m.RunID) != "")
}

// 在锚点周围扩一圈邻接消息，保证因果链完整：前一轮用户、本条、后一轮
func expandAround(histLen, i int) (int, int) {
	lo := i - 1
	if lo < 0 {
		lo = 0
	}
	hi := i + 1
	if hi >= histLen {
		hi = histLen - 1
	}
	if lo > hi {
		lo = hi
	}
	return lo, hi
}

// 大模型兜底定位：给模型便宜的时间线大纲，语义选出相关区间
// 越界、反转或解析失败一律视为未命中；不编造、不报错（优雅降级）
func llmLocateRange(
	ctx context.Context,
	client llm.Client,
	userText string,
	history []session.Message,
) (int, int, bool) {
	if client == nil || len(history) == 0 {
		return 0, 0, false
	}
	payload, err := json.Marshal(struct {
		UserText string          `json:"user_text"`
		Timeline []timelineEntry `json:"timeline"`
	}{UserText: userText, Timeline: buildTimelineOutline(history)})
	if err != nil {
		return 0, 0, false
	}

	var out struct {
		Found bool `json:"found"`
		Lo    *int `json:"lo"` // 指针区分 0 与缺失
		Hi    *int `json:"hi"`
	}
	if err := client.GenerateJSON(ctx, llm.Request{
		System: locatePromptTemplate,
		User:   string(payload),
	}, &out); err != nil {
		return 0, 0, false
	}
	if !out.Found || out.Lo == nil || out.Hi == nil {
		return 0, 0, false
	}
	lo, hi := *out.Lo, *out.Hi
	if lo < 0 {
		lo = 0
	}
	if hi >= len(history) {
		hi = len(history) - 1
	}
	if lo > hi {
		return 0, 0, false
	}
	return lo, hi, true
}

// 时间线大纲的单条预览项，控制大模型定位请求体积
type timelineEntry struct {
	// 历史下标，对应回灌区间端点
	Idx int `json:"idx"`
	// 消息角色
	Role string `json:"role"`
	// 可选展示模式
	Mode string `json:"mode,omitempty"`
	// 可选关联诊断运行编号
	RunID string `json:"runId,omitempty"`
	// 消息正文首句预览
	Preview string `json:"preview"`
}

// 组装时间线大纲：每条消息一行预览，保留下标、角色、模式与运行编号供模型定位
func buildTimelineOutline(history []session.Message) []timelineEntry {
	out := make([]timelineEntry, 0, len(history))
	for i, m := range history {
		out = append(out, timelineEntry{
			Idx:     i,
			Role:    m.Role,
			Mode:    m.Mode,
			RunID:   m.RunID,
			Preview: firstSentence(m.Content, defaultTimelinePreviewRunes),
		})
	}
	return out
}

// 取正文首句/首段预览，按字符上限截断，保证大纲便宜可扫读
func firstSentence(content string, maxRunes int) string {
	s := strings.TrimSpace(content)
	if s == "" {
		return ""
	}
	if idx := strings.IndexAny(s, "\n。；"); idx > 0 {
		s = s[:idx]
	}
	r := []rune(s)
	if maxRunes > 0 && len(r) > maxRunes {
		s = string(r[:maxRunes]) + "…"
	}
	return s
}

// 从权威存储全量历史取区间原文，含下标；越界自动收敛
// 不取已折叠预览——回灌的就是被压缩丢掉的原文
func rehydrateRange(history []session.Message, lo, hi int) []rehydratedMsg {
	if len(history) == 0 {
		return nil
	}
	if lo < 0 {
		lo = 0
	}
	if hi >= len(history) {
		hi = len(history) - 1
	}
	if lo > hi {
		return nil
	}
	out := make([]rehydratedMsg, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		m := history[i]
		out = append(out, rehydratedMsg{
			Idx:     i,
			Role:    m.Role,
			Content: m.Content,
			Mode:    m.Mode,
			RunID:   m.RunID,
		})
	}
	return out
}

// 对回灌窗做区间压缩以塞进子预算，复用全局浅层与中层压缩
// 浅层截单条超长预览，中层折叠到预算内，故窗恒可塞入
// 带折叠或截断标记，不静默丢段；切片长度不变，下标原样保留
func compactRange(window []rehydratedMsg, budgetTokens int) []rehydratedMsg {
	if len(window) == 0 {
		return window
	}
	if budgetTokens <= 0 {
		budgetTokens = defaultRehydrateBudgetTokens
	}
	hist := rehydratedToHist(window)
	hist = compactL0(hist, defaultMaxMessageContentTokens, defaultTruncatedPreviewTokens)
	hist = compactL1(hist, budgetTokens, nil)
	return histToRehydrated(hist, window)
}

// 把回灌窗转为压缩可消费的历史视图（丢下标，压缩后再回填）
func rehydratedToHist(window []rehydratedMsg) []towerHistMsg {
	out := make([]towerHistMsg, len(window))
	for i, m := range window {
		out[i] = towerHistMsg{
			Role:    m.Role,
			Content: m.Content,
			Mode:    m.Mode,
			RunID:   m.RunID,
		}
	}
	return out
}

// 把压缩后的历史映射回回灌窗，下标取自原窗（压缩不改切片长度）
func histToRehydrated(hist []towerHistMsg, window []rehydratedMsg) []rehydratedMsg {
	out := make([]rehydratedMsg, len(hist))
	for i := range hist {
		idx := 0
		if i < len(window) {
			idx = window[i].Idx
		}
		out[i] = rehydratedMsg{
			Idx:     idx,
			Role:    hist[i].Role,
			Content: hist[i].Content,
			Mode:    hist[i].Mode,
			RunID:   hist[i].RunID,
		}
	}
	return out
}

// 便宜的相关度评分：查询与内容的字符二元组重叠数，中英文皆适用
// 仅用于关键词分支在多条诊断间择优，非精确检索
func relevanceScore(query, content string) int {
	q := strings.ToLower(query)
	c := strings.ToLower(content)
	if q == "" || c == "" {
		return 0
	}
	qr := []rune(q)
	grams := make(map[string]struct{})
	for i := 0; i+2 <= len(qr); i++ {
		g := string(qr[i : i+2])
		if isJunkGram(g) {
			continue
		}
		grams[g] = struct{}{}
	}
	score := 0
	for g := range grams {
		if strings.Contains(c, g) {
			score++
		}
	}
	return score
}

// 判断二元组是否为空白/标点等无信息量片段，跳过以降噪
func isJunkGram(g string) bool {
	for _, r := range g {
		if r == ' ' || r == '\t' || r == '\n' ||
			(r >= '!' && r <= '/') || (r >= ':' && r <= '@') ||
			(r >= '[' && r <= '`') || (r >= '{' && r <= '~') {
			return true
		}
	}
	return false
}
