// 判分器：把评测记录对照场景真值出分
//
// 三层口径：
//  ①根因命中——机械包含匹配（真值资源名，大小写不敏感，出现在 supported 结论的理由文本中），
//    不做语义判断（#19 精神：判分器同样纯机械）；语义匹配属第③层的抽样人判
//  ②引用合法——结论引用的证据编号必须存在于本次 run 的证据链，机械可查
//  ③引用支持结论——抽样生成 rubric 表，人工或 LLM 辅助评，回填后另算一致率（本期只出架子）

package eval

import (
	"fmt"
	"math/rand"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// GroundTruth 场景真值四元组；scenario.yaml 的 ground_truth 段
type GroundTruth struct {
	// 资源类型（如 pod / deployment / service）
	ResourceType string `yaml:"resource_type"`
	// 资源名（判分①的机械匹配键）
	ResourceName string `yaml:"resource_name"`
	// 所属命名空间
	Namespace string `yaml:"namespace"`
	// 故障类型标签（如 bad-image-crashloop / selector-mismatch）
	FaultType string `yaml:"fault_type"`
}

// scenario 文件的顶层形状；只取 ground_truth 段，其余字段忽略
type scenarioFile struct {
	// 场景真值段；判分①②的输入
	GroundTruth GroundTruth `yaml:"ground_truth"`
}

// LoadGroundTruth 从场景 manifest（scenario.yaml）读取真值四元组
// 缺段或资源名为空时报错：真值是判分前提，不允许静默空判
func LoadGroundTruth(path string) (GroundTruth, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return GroundTruth{}, fmt.Errorf("read scenario: %w", err)
	}
	var sf scenarioFile
	if err := yaml.Unmarshal(raw, &sf); err != nil {
		return GroundTruth{}, fmt.Errorf("parse scenario %s: %w", path, err)
	}
	gt := sf.GroundTruth
	if strings.TrimSpace(gt.ResourceName) == "" {
		return GroundTruth{}, fmt.Errorf("scenario %s: ground_truth.resource_name is required", path)
	}
	return gt, nil
}

// JudgeResult 一份记录的判分结果（①②层全自动）
type JudgeResult struct {
	// 被判分的运行编号
	RunID string `json:"run_id"`
	// 分组透传（来自评测记录本身，非判分产物）：实验矩阵按方法 / 名义 K /
	// 实测轮数出列；旧记录无 acquire 字段时为零值，照常出列
	AcquireMethod string `json:"acquire_method"`
	MaxRounds     int    `json:"max_rounds"`
	Rounds        int    `json:"rounds"`
	Completed     bool   `json:"completed"`
	// ①根因命中（0/1）
	RootCauseHit bool `json:"root_cause_hit"`
	// 命中的结论理由片段所在条目序号（-1 = 未命中）
	RootCauseHitBy int `json:"root_cause_hit_by"`
	// ②引用合法违规列表（被引用但不存在的证据编号）；空 = 全部合法
	CitationViolations []string `json:"citation_violations"`
}

// JudgeRecord 判分一份评测记录：①根因命中 + ②引用合法
func JudgeRecord(rec RunRecord, gt GroundTruth) JudgeResult {
	res := JudgeResult{
		RunID:              rec.RunID,
		AcquireMethod:      rec.AcquireMethod,
		MaxRounds:          rec.AcquireMaxRounds,
		Rounds:             rec.Rounds,
		Completed:          rec.Completed,
		RootCauseHitBy:     -1,
		CitationViolations: []string{},
	}

	// ①：只在 supported 结论里找真值资源名——refuted/insufficient 提及资源不是根因主张
	want := strings.ToLower(gt.ResourceName)
	for i, c := range rec.RootCauses {
		if c.Result != "supported" {
			continue
		}
		if strings.Contains(strings.ToLower(c.Reason), want) {
			res.RootCauseHit = true
			res.RootCauseHitBy = i
			break
		}
	}

	// ②：引用编号必须存在于本次证据链
	known := make(map[string]struct{}, len(rec.ToolCalls))
	for _, tc := range rec.ToolCalls {
		known[tc.EvidenceID] = struct{}{}
	}
	for _, id := range rec.EvidenceCited {
		if _, ok := known[id]; !ok {
			res.CitationViolations = append(res.CitationViolations, id)
		}
	}
	return res
}

// ProjectionHit 投影命中率判定的机械内核：根因资源名是否出现在投影文本中
// 服务表格投影对比实验（投影是纯函数，判定也是纯函数：行级包含）
func ProjectionHit(summaryText, resourceName string) bool {
	return strings.Contains(strings.ToLower(summaryText), strings.ToLower(resourceName))
}

// RubricRow 第③层抽样评分表的一行：一条 (结论, 引用证据) 对，待人工或 LLM 辅助评
type RubricRow struct {
	// 结论序号（对应记录里的 verdict_root_cause 下标）
	ConclusionIdx int `json:"conclusion_idx"`
	// 结论理由文本
	Reason string `json:"reason"`
	// 被引用证据编号
	EvidenceID string `json:"evidence_id"`
	// 证据摘要（评分时的可见材料）
	Summary string `json:"summary"`
	// 评分留空待填：supports / partial / not_supports
	Verdict string `json:"verdict"`
}

// SampleRubric 从记录的 (结论 × 引用) 对里固定种子抽 n 行，生成③层评分表
// 结论无引用或记录为空时返回空表；种子固定保证抽样可复现
func SampleRubric(rec RunRecord, n int, seed int64) []RubricRow {
	var pairs []RubricRow
	summaries := make(map[string]string, len(rec.ToolCalls))
	for _, tc := range rec.ToolCalls {
		summaries[tc.EvidenceID] = tc.Summary
	}
	for i, c := range rec.RootCauses {
		for _, id := range c.EvidenceIDs {
			pairs = append(pairs, RubricRow{
				ConclusionIdx: i,
				Reason:        c.Reason,
				EvidenceID:    id,
				Summary:       summaries[id],
			})
		}
	}
	if n <= 0 || len(pairs) == 0 {
		return nil
	}
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(pairs), func(i, j int) { pairs[i], pairs[j] = pairs[j], pairs[i] })
	if n > len(pairs) {
		n = len(pairs)
	}
	return pairs[:n]
}

// RenderRubricMarkdown 把抽样表渲染成人读 markdown（评分作业界面）
// 表中 verdict 列留空；评完回填后可另算一致率（本期不自动算）
// 单元格统一净化：竖杠转义 + 换行折叠防空行打穿表格；摘要按码点截断防切新多字节字符
func RenderRubricMarkdown(rows []RubricRow) string {
	var b strings.Builder
	b.WriteString("| 结论# | 理由 | 证据 | 证据摘要 | verdict |\n")
	b.WriteString("| - | --- | --- | --- | --- |\n")
	for _, r := range rows {
		reason := sanitizeTableCell(r.Reason)
		summary := sanitizeTableCell(r.Summary)
		if rs := []rune(summary); len(rs) > 160 {
			summary = string(rs[:160]) + "…"
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s |\n", r.ConclusionIdx, reason, r.EvidenceID, summary, r.Verdict)
	}
	return b.String()
}

// sanitizeTableCell 把单元格文本安全化：竖杠转义（防破列）+ 换行折叠为空格（防打穿表格行）
func sanitizeTableCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
