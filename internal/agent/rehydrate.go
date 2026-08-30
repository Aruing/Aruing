// 分层检索回灌（Algorithm 1 第 6–8 行）：默认视图因压缩丢细节时，按用户问题
// 两级检索定位相关记忆单元、回灌原文或证据预览，作为独立字段注入模型载荷
//
// λ₁ 确定性寻址每轮必跑（纯正则 + 会话级资源名词典，便宜，不调模型）：
// 问题中的编号（run_/e_ 等 ID 族）与资源名整词匹配单元地址集，锚点类问题必命中（引理 1）
// λ₁ 空且视图因压缩丢细节且问题含语义指涉时，λ₂ 大模型按时间线大纲定位一次兜底
// 消息命中仅视图丢细节时回灌原文（原文已在视图时不重复注入，省预算）；
// 证据命中无论压缩与否都回灌原文预览（raw 从不在默认视图）
// 回灌迭代口径对齐定理 1：单次应答单遍回灌；预算不足只回灌预览时 C1 保地址，
// 下一轮 λ₁ 仍可定位剩余缺失，跨轮自然收敛，无轮内多轮循环
//
// 定位、回灌与窗内压缩均为纯函数，不写库、不持状态：
// 未来换磁盘存储或检索实现只换函数内部，外部接线不动
//
// 回灌的消息原文与证据预览是对话叙述/历史材料的重放，不得当新证据或判决；
// 权威诊断事实仍在诊断账本
package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Aruing/Aruing/internal/core"
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
	// 合成证据条目的展示模式：区分对话消息回灌与证据原文预览（tower.md 教学段对应）
	rehydratedModeEvidence = "evidence"
)

// 词典候选须整体形如 DNS-1123 单标签：小写字母数字与连字符、首尾为字母数字
// 资源名（pod/deployment 等）与命名空间均为该形态；大写词（表头/状态列）天然落选
var dns1123TokenRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// 词典 token 长度界：过短是虚词与短 flag 值，过长不是资源名（DNS-1123 标签上限 63）
const (
	minDictTokenLen = 3
	maxDictTokenLen = 63
)

// kubectl 动词、资源类型与常见格式词：出现在命令视图但不是实体，不进词典
// 仅作机械降噪，不构成业务意图枚举；漏掉的常用词命中也只是多回灌，无正确性影响
var dictStopWords = map[string]struct{}{
	"kubectl": {}, "get": {}, "describe": {}, "logs": {}, "log": {},
	"exec": {}, "explain": {}, "top": {}, "edit": {}, "delete": {},
	"apply": {}, "create": {}, "patch": {}, "replace": {}, "rollout": {},
	"scale": {}, "wait": {}, "proxy": {}, "auth": {}, "version": {},
	"config": {}, "drain": {}, "cordon": {}, "uncordon": {}, "taint": {},
	"api-resources": {}, "api-versions": {}, "cluster-info": {},
	"pod": {}, "pods": {}, "deployment": {}, "deployments": {}, "deploy": {},
	"service": {}, "services": {}, "svc": {}, "namespace": {}, "namespaces": {},
	"node": {}, "nodes": {}, "event": {}, "events": {}, "pvc": {}, "pvcs": {},
	"configmap": {}, "configmaps": {}, "secret": {}, "secrets": {},
	"ingress": {}, "ingresses": {}, "endpoint": {}, "endpoints": {},
	"job": {}, "jobs": {}, "cronjob": {}, "cronjobs": {},
	"statefulset": {}, "statefulsets": {}, "daemonset": {}, "daemonsets": {},
	"replicaset": {}, "replicasets": {}, "hpa": {},
	"serviceaccount": {}, "serviceaccounts": {}, "role": {}, "roles": {},
	"rolebinding": {}, "rolebindings": {}, "clusterrole": {},
	"clusterrolebinding": {}, "clusterrolebindings": {},
	"customresourcedefinition": {}, "customresourcedefinitions": {},
	"crd": {}, "crds": {},
	"json": {}, "yaml": {}, "wide": {}, "all": {}, "watch": {},
	"previous": {}, "timestamps": {}, "follow": {}, "tail": {}, "head": {},
	"container": {}, "containers": {}, "selector": {}, "field-selector": {},
	"sort-by": {}, "labels": {}, "label": {}, "output": {}, "name": {},
	"names": {}, "running": {}, "pending": {}, "completed": {},
}

// 回灌窗内的单条消息，带时间线下标便于模型定位是哪一步
type rehydratedMsg struct {
	// 在会话历史中的下标（从零开始），仅用于模型指代，不作业务编号；
	// 合成证据条目无历史位置，取 -1
	Idx int `json:"idx"`
	// 消息角色（用户或助手；证据预览条目为助手）
	Role string `json:"role"`
	// 消息正文（可能经区间压缩折叠或截断）
	Content string `json:"content"`
	// 可选展示模式（基线、诊断、检查点；证据预览条目为 evidence）
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

// 会话级资源名词典：诊断账本全部证据的命令视图与摘要机械抽取
// DNS-1123 形 token（kubectl 命令里的资源名/命名空间），按出现序去重
// 纯代码零模型调用；flag 名、含冒号/斜杠/等号的 token（镜像/路径/选择器）不进词典，
// 宁可词典少收（锚点主力是 ID 族），不收噪声
func buildEntityDict(records []session.DiagnosticRecord) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, rec := range records {
		for _, e := range rec.Evidence {
			for _, tok := range entityCandidates(e.CommandView + " " + e.Summary) {
				if _, ok := seen[tok]; ok {
					continue
				}
				seen[tok] = struct{}{}
				out = append(out, tok)
			}
		}
	}
	return out
}

// 从一段文本抽取词典候选：空白切分后逐 token 过滤噪声
func entityCandidates(text string) []string {
	out := make([]string, 0)
	for _, tok := range strings.Fields(text) {
		if isEntityToken(tok) {
			out = append(out, tok)
		}
	}
	return out
}

// 判定单个 token 是否为词典候选
// 词内出现冒号/斜杠/等号/竖线说明是镜像标签、路径或选择器赋值，整体作废；
// 词边缘的表格渲染标点（括号引号逗号等）剥掉后再验本体；
// 整体须形如 DNS-1123 单标签且至少含一个小写字母（排除纯数字端口/版本号），
// 最后滤掉 kubectl 常用词
func isEntityToken(raw string) bool {
	if strings.ContainsAny(raw, ":/=|") {
		return false
	}
	tok := strings.Trim(raw, "\"'`([{<>,.;:!?)]}")
	n := len(tok)
	if n < minDictTokenLen || n > maxDictTokenLen {
		return false
	}
	if !dns1123TokenRe.MatchString(tok) {
		return false
	}
	if !strings.ContainsFunc(tok, func(r rune) bool { return r >= 'a' && r <= 'z' }) {
		return false
	}
	if _, ok := dictStopWords[tok]; ok {
		return false
	}
	return true
}

// λ₁ 确定性寻址（Algorithm 1 第 6 行）：问题提及 ∩ 单元地址集，纯代码零模型
// 单元两类：会话消息（地址 = 正文 ID 族 + RunID 字段 + 命中的词典实体）与
// 诊断证据（地址 = e_ 编号 + 命令视图/摘要中的词典实体）
// 返回命中消息下标（升序）与命中证据编号（按账本出现序）；
// 锚点类问题（提及出现在依据单元地址中）必命中，recall = 1（引理 1）
func locateByAddress(
	userText string,
	history []session.Message,
	records []session.DiagnosticRecord,
	dict []string,
) (msgIdx []int, evIDs []string) {
	idMentions, entityMentions := splitMentions(extractAddrs(userText, dict), dict)
	if len(idMentions) == 0 && len(entityMentions) == 0 {
		return nil, nil
	}
	for i, m := range history {
		if messageMentionsAddress(m, idMentions, entityMentions) {
			msgIdx = append(msgIdx, i)
		}
	}
	for _, rec := range records {
		for _, e := range rec.Evidence {
			if evidenceMentionsAddress(e, idMentions, entityMentions) {
				evIDs = append(evIDs, e.ID)
			}
		}
	}
	return msgIdx, evIDs
}

// 拆分问题提及为编号与实体两组：在词典内的是实体，其余为 ID 族编号
// 两类无交集（ID 含下划线，DNS-1123 不含）
func splitMentions(addrs []string, dict []string) (idMentions map[string]struct{}, entityMentions []string) {
	dictSet := make(map[string]struct{}, len(dict))
	for _, d := range dict {
		dictSet[d] = struct{}{}
	}
	idMentions = make(map[string]struct{})
	seenEnt := make(map[string]struct{})
	for _, a := range addrs {
		if _, ok := dictSet[a]; ok {
			if _, dup := seenEnt[a]; !dup {
				seenEnt[a] = struct{}{}
				entityMentions = append(entityMentions, a)
			}
			continue
		}
		idMentions[a] = struct{}{}
	}
	return idMentions, entityMentions
}

// 消息单元地址命中：编号提及命中正文 ID 族或 RunID 字段，
// 或词典实体整词出现在正文（大小写不敏感）
func messageMentionsAddress(m session.Message, idMentions map[string]struct{}, entityMentions []string) bool {
	runID := strings.TrimSpace(m.RunID)
	if runID != "" {
		if _, ok := idMentions[runID]; ok {
			return true
		}
	}
	for _, a := range extractIDAddrs(m.Content) {
		if _, ok := idMentions[a]; ok {
			return true
		}
	}
	if len(entityMentions) == 0 {
		return false
	}
	lower := strings.ToLower(m.Content)
	for _, ent := range entityMentions {
		if containsWholeWord(lower, strings.ToLower(ent)) {
			return true
		}
	}
	return false
}

// 证据卡地址命中：编号直接命中，或词典实体整词出现在命令视图/摘要
func evidenceMentionsAddress(e core.Evidence, idMentions map[string]struct{}, entityMentions []string) bool {
	if _, ok := idMentions[e.ID]; ok {
		return true
	}
	if len(entityMentions) == 0 {
		return false
	}
	lower := strings.ToLower(e.CommandView + " " + e.Summary)
	for _, ent := range entityMentions {
		if containsWholeWord(lower, strings.ToLower(ent)) {
			return true
		}
	}
	return false
}

// 追问过去步骤的措辞信号（非封闭意图表，仅作 λ₂ 兜底的便宜闸）
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

// 大模型兜底定位（λ₂）：给模型便宜的时间线大纲，语义选出相关区间
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

// 分层检索回灌总装（Algorithm 1 第 6–8 行），轮内单遍执行
// λ₁ 每轮必跑；λ₁ 命中即取回，不再调 λ₂
// 消息回灌仅在视图因压缩丢细节时注入（原文已在视图时不重复注入，省预算）；
// 证据 raw 预览无论压缩与否都注入（raw 从不在默认视图）
// 消息与证据条目共享回灌子预算，超预算由窗压缩截断（C1 保地址，跨轮收敛）
// 客户端为空时 λ₂ 跳过，λ₁ 命中路径不受影响（单测与降级路径可用）
func rehydrateLayered(
	ctx context.Context,
	client llm.Client,
	userText string,
	history []session.Message,
	records []session.DiagnosticRecord,
	view towerContextView,
) []rehydratedMsg {
	dict := buildEntityDict(records)
	msgIdx, evIDs := locateByAddress(userText, history, records, dict)

	var window []rehydratedMsg
	if len(msgIdx) > 0 && viewCompactedAwayDetail(view) {
		window = rehydrateIndices(history, msgIdx)
	}
	window = append(window, evidencePreviewWindow(evIDs, records)...)
	if len(window) > 0 {
		return compactRange(window, defaultRehydrateBudgetTokens)
	}

	// λ₂ 兜底：λ₁ 空、视图丢细节且问题含语义指涉时，时间线大纲定位一次
	if !viewCompactedAwayDetail(view) || !referencesPastStep(userText) || client == nil {
		return nil
	}
	if lo, hi, ok := llmLocateRange(ctx, client, userText, history); ok {
		return compactRange(rehydrateRange(history, lo, hi), defaultRehydrateBudgetTokens)
	}
	return nil
}

// λ₁ 命中消息取原文：各自扩一圈邻接因果链（前一轮用户 + 本条 + 后一轮），
// 下标并集升序去重后逐条取原文，保留下标供模型指代
func rehydrateIndices(history []session.Message, idxs []int) []rehydratedMsg {
	if len(history) == 0 || len(idxs) == 0 {
		return nil
	}
	set := make(map[int]struct{})
	for _, i := range idxs {
		if i < 0 || i >= len(history) {
			continue
		}
		lo, hi := expandAround(len(history), i)
		for j := lo; j <= hi; j++ {
			set[j] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	keys := make([]int, 0, len(set))
	for j := range set {
		keys = append(keys, j)
	}
	sort.Ints(keys)
	out := make([]rehydratedMsg, 0, len(keys))
	for _, j := range keys {
		m := history[j]
		out = append(out, rehydratedMsg{
			Idx:     j,
			Role:    m.Role,
			Content: m.Content,
			Mode:    m.Mode,
			RunID:   m.RunID,
		})
	}
	return out
}

// 证据命中合成回灌条目：raw 从不在默认视图，命中即回灌原文预览
// 头部带编号/工具/命令视图，正文为原始输出；超长由窗压缩截断（C1 保地址）
// raw 为空（如纯错误证据）时退回摘要与错误，保证条目仍有可答内容
// 下标取 -1：合成条目无历史位置，模型按编号（e_xxx）指代
func evidencePreviewWindow(evIDs []string, records []session.DiagnosticRecord) []rehydratedMsg {
	if len(evIDs) == 0 {
		return nil
	}
	want := make(map[string]struct{}, len(evIDs))
	for _, id := range evIDs {
		want[id] = struct{}{}
	}
	out := make([]rehydratedMsg, 0, len(evIDs))
	for _, rec := range records {
		for _, e := range rec.Evidence {
			if _, ok := want[e.ID]; !ok {
				continue
			}
			body := string(e.Raw)
			if strings.TrimSpace(body) == "" {
				body = strings.TrimSpace(e.Summary)
				if msg := strings.TrimSpace(e.Error); msg != "" {
					body = strings.TrimSpace(body + " " + msg)
				}
			}
			content := fmt.Sprintf("证据 %s 原文预览（tool=%s, %s）:\n%s", e.ID, e.ToolName, e.CommandView, body)
			out = append(out, rehydratedMsg{
				Idx:     -1,
				Role:    session.RoleAssistant,
				Mode:    rehydratedModeEvidence,
				RunID:   rec.RunID,
				Content: content,
			})
		}
	}
	return out
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
