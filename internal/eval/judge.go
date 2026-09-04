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

// GroundTruth 场景真值；scenario.yaml 的 ground_truth 段
type GroundTruth struct {
	// 资源类型（如 pod / deployment / service）
	ResourceType string `yaml:"resource_type"`
	// 资源名（判分①的机械匹配键之一）
	ResourceName string `yaml:"resource_name"`
	// 所属命名空间
	Namespace string `yaml:"namespace"`
	// 故障类型标签（如 bad-image-crashloop / selector-mismatch）
	FaultType string `yaml:"fault_type"`
	// 故障特征词（判分①的机械匹配键之二，任一命中即算）：模型结论常述故障
	// 机制而不复述资源名（试点实测 crashloop 类 1/10 点名），特征词与资源名
	// 任一命中即根因命中；缺省为空 = 退回纯资源名匹配（旧口径兼容）
	FaultSignature []string `yaml:"fault_signature"`
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

	// ①：只在 supported 结论里找真值锚点——资源名或故障特征词（任一命中）；
	// refuted/insufficient 提及资源不是根因主张。模型结论常述故障机制而不复述
	// 资源名（试点实测），特征词与资源名同权机械包含，不做语义判断
	want := strings.ToLower(gt.ResourceName)
	for i, c := range rec.RootCauses {
		if c.Result != "supported" {
			continue
		}
		if reasonHitsSignature(c.Reason, want, gt.FaultSignature) {
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

// reasonHitsSignature 判分①的机械包含内核：结论理由含资源名或任一故障特征词
// （大小写归一；空特征词列表退回纯资源名匹配）
func reasonHitsSignature(reason, resourceName string, signatures []string) bool {
	lowered := strings.ToLower(reason)
	if resourceName != "" && strings.Contains(lowered, resourceName) {
		return true
	}
	for _, sig := range signatures {
		if sig = strings.TrimSpace(strings.ToLower(sig)); sig != "" && strings.Contains(lowered, sig) {
			return true
		}
	}
	return false
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

// ③层评分三值口径：证据是否支持结论（裁决 #6：LLM 辅助评与人工评共用同一取值域）
const (
	// VerdictSupports 证据摘要明确支持结论
	VerdictSupports = "supports"
	// VerdictPartial 证据部分支持或相关性不足以下定论
	VerdictPartial = "partial"
	// VerdictNotSupports 证据与结论矛盾或无关
	VerdictNotSupports = "not_supports"
	// VerdictError LLM 辅助评失败行的占位（重试耗尽；非评判值，不进一致率分母）
	VerdictError = "error"
)

// NormalizeVerdict 规范化③层评分：容忍首尾空白，仅接受三值口径，其余报错
func NormalizeVerdict(s string) (string, error) {
	v := strings.TrimSpace(s)
	switch v {
	case VerdictSupports, VerdictPartial, VerdictNotSupports:
		return v, nil
	}
	return "", fmt.Errorf("invalid rubric verdict %q (want supports/partial/not_supports)", s)
}

// Agreement 两组同长度已回填 rubric 的逐行一致计数
// 任一侧 verdict 为 error 的行不计入分母（非评判值）；未回填或非法值报错
func Agreement(a, b []RubricRow) (agree int, total int, err error) {
	if len(a) != len(b) {
		return 0, 0, fmt.Errorf("rubric length mismatch: %d vs %d", len(a), len(b))
	}
	for i := range a {
		skip := false
		va, ea := NormalizeVerdict(a[i].Verdict)
		if ea != nil {
			// error 是失败占位非非法输入：跳过该行，不当报错
			if strings.TrimSpace(a[i].Verdict) != VerdictError {
				return 0, 0, fmt.Errorf("row %d (A): %w", i, ea)
			}
			skip = true
		}
		vb, eb := NormalizeVerdict(b[i].Verdict)
		if eb != nil {
			if strings.TrimSpace(b[i].Verdict) != VerdictError {
				return 0, 0, fmt.Errorf("row %d (B): %w", i, eb)
			}
			skip = true
		}
		if skip {
			continue
		}
		total++
		if va == vb {
			agree++
		}
	}
	return agree, total, nil
}

// collectRubricPairs 收集一批记录的全部 (结论 × 引用证据) 对，供逐记录与全池化抽样共用
func collectRubricPairs(recs []RunRecord) []RubricRow {
	var pairs []RubricRow
	for _, rec := range recs {
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
	}
	return pairs
}

// sampleRubricRows 池化后固定种子抽 n 行（n 超对数时全取；种子固定保证可复现）
func sampleRubricRows(pairs []RubricRow, n int, seed int64) []RubricRow {
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

// SampleRubric 从单记录的 (结论 × 引用) 对里固定种子抽 n 行，生成③层评分表
// 结论无引用或记录为空时返回空表（逐记录口径，与历史行为一致）
func SampleRubric(rec RunRecord, n int, seed int64) []RubricRow {
	return sampleRubricRows(collectRubricPairs([]RunRecord{rec}), n, seed)
}

// SampleRubricTotal 全记录池化后固定种子抽 n 行（裁决 #6 全局抽样口径）：
// 跨记录合并全部对再洗牌，避免逐记录抽样在大批量记录下行数超采
func SampleRubricTotal(recs []RunRecord, n int, seed int64) []RubricRow {
	return sampleRubricRows(collectRubricPairs(recs), n, seed)
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

// 探针判分状态（判分侧产出；no_* 两态透传自记录的期望展开状态）
const (
	// 答案满足全部期望组
	ProbeStatusHit = "hit"
	// 答案未满足全部期望组
	ProbeStatusMiss = "miss"
)

// 单条探针的判分结果
type ProbeVerdict struct {
	// 探针编号
	ProbeID string `json:"probe_id"`
	// 类别：evidence / synthesis / chain
	Class string `json:"class"`
	// 判分状态：hit / miss / no_diagnosis / no_facts（后两态透传，不进成功率分母）
	Status string `json:"status"`
	// 是否命中（status=hit 时为真）
	Hit bool `json:"hit"`
}

// 一条长会话记录的探针判分汇总（judge --probe 的输出单元）
// 成功率分母只含可判探针（hit+miss）；no_diagnosis / no_facts 单列计数，
// 不把诊断路由失败混进记忆度量，也不静默剔除（全量报告）
type ProbeJudgeResult struct {
	// 会话编号
	SessionID string `json:"session_id"`
	// 场景名
	Scenario string `json:"scenario"`
	// 记忆方法（分组变量）
	MemoryMethod string `json:"memory_method"`
	// 主体轮数（断崖曲线横轴）
	Rounds int `json:"rounds"`
	// 逐探针判分
	Probes []ProbeVerdict `json:"probes"`
	// 探针总数
	ProbeTotal int `json:"probe_total"`
	// 可判探针数（成功率分母）
	ProbeScored int `json:"probe_scored"`
	// 命中数
	ProbeHits int `json:"probe_hits"`
	// 成功率 = hits / scored；scored 为 0 时取 0
	SuccessRate float64 `json:"success_rate"`
	// 引用的诊断未发生数（单列报告）
	NoDiagnosis int `json:"no_diagnosis"`
	// 诊断无该类事实数（单列报告）
	NoFacts int `json:"no_facts"`
	// 内嵌诊断的逐条复判（复用单次诊断判分：完成率 / 根因命中 / 引用合法）
	Diagnoses []JudgeResult `json:"diagnoses"`
	// 穿插诊断总数
	DiagnoseTotal int `json:"diagnose_total"`
	// 完成数（报告落账本）
	DiagnoseCompleted int `json:"diagnose_completed"`
	// 根因命中数（内嵌复判①层）
	DiagnoseRootCauseHits int `json:"diagnose_root_cause_hits"`
}

// JudgeProbeSession 判分一条探针会话记录
// 探针层：①层机械包含——每期望组至少一串被答案包含（大小写不敏感），全组满足才 hit；
// 内嵌诊断层：逐条复用 JudgeRecord（同场景真值），供完成率列与探针质量交叉核对
func JudgeProbeSession(rec ProbeSessionRecord, gt GroundTruth) ProbeJudgeResult {
	res := ProbeJudgeResult{
		SessionID:    rec.SessionID,
		Scenario:     rec.Scenario,
		MemoryMethod: rec.MemoryMethod,
		Rounds:       rec.Rounds,
		Probes:       make([]ProbeVerdict, 0, len(rec.Probes)),
		Diagnoses:    make([]JudgeResult, 0, len(rec.Diagnoses)),
	}
	for _, p := range rec.Probes {
		v := ProbeVerdict{ProbeID: p.ProbeID, Class: p.Class}
		switch p.ExpectStatus {
		case ExpectNoDiagnosis:
			v.Status = ExpectNoDiagnosis
			res.NoDiagnosis++
		case ExpectNoFacts:
			v.Status = ExpectNoFacts
			res.NoFacts++
		default:
			v.Hit = probeAnswerHits(p.Answer, p.Expected)
			if v.Hit {
				v.Status = ProbeStatusHit
				res.ProbeHits++
			} else {
				v.Status = ProbeStatusMiss
			}
			res.ProbeScored++
		}
		res.ProbeTotal++
		res.Probes = append(res.Probes, v)
	}
	if res.ProbeScored > 0 {
		res.SuccessRate = float64(res.ProbeHits) / float64(res.ProbeScored)
	}
	for _, d := range rec.Diagnoses {
		jr := JudgeRecord(d.Record, gt)
		res.Diagnoses = append(res.Diagnoses, jr)
		res.DiagnoseTotal++
		if d.Status == "completed" {
			res.DiagnoseCompleted++
		}
		if jr.RootCauseHit {
			res.DiagnoseRootCauseHits++
		}
	}
	return res
}

// probeAnswerHits 包含判定：每个期望组至少一串候选被答案包含（大小写不敏感）
// 空组列表视为不命中（无期望即无从判对；规格校验已保证至少一组）
func probeAnswerHits(answer string, groups [][]string) bool {
	if len(groups) == 0 {
		return false
	}
	lower := strings.ToLower(answer)
	for _, g := range groups {
		if len(g) == 0 {
			return false
		}
		matched := false
		for _, want := range g {
			if want != "" && strings.Contains(lower, strings.ToLower(want)) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
