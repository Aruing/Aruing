// 探针实验装置（0.1.3 步骤 4）：脚本化长会话的规格与脚本生成
//
// 长会话实验单元：普通问答轮与正式诊断请求轮交替铺满 N 轮（种子驱动、可复现），
// 尾部统一发探针问题，只考会话历史里埋过的事实；答案机械核对（①层包含判定）
// 探针真值两类来源：静态字面量（场景资源名等稳定事实）与 from_ledger 规则
// （第 k 次诊断的 pod 名 / 命令串——真值依赖运行时账本，跑数进程内展开成字面量，
// 判分侧只做机械包含，不做语义判断，#19 口径同构）
// 本文件只做规格解析、校验、生成与展开，不调集群、不调模型

package eval

import (
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strings"

	"github.com/Aruing/Aruing/internal/session"
	"gopkg.in/yaml.v3"
)

// 探针轮次类型
const (
	// 普通问答轮（基线对话，铺长会话主体）
	ProbeTurnQA = "qa"
	// 正式诊断请求轮（触发 Tower 升格，往会话埋诊断事实）
	ProbeTurnDiagnose = "diagnose"
	// 尾部探针轮（只考历史埋过的事实）
	ProbeTurnProbe = "probe"
)

// 探针问题类别（实验设计框架 §五三类；判分口径同为①层，类别供分组统计）
const (
	// 证据回灌类：考证据细节能否经回灌找回
	ProbeClassEvidence = "evidence"
	// 跨压缩综合类：考跨多次诊断结论的综合比对
	ProbeClassSynthesis = "synthesis"
	// 证据链细节类：考当时执行过的命令
	ProbeClassChain = "chain"
)

// from_ledger 规则名
const (
	// 第 k 次诊断证据中的资源实体名（含场景资源名子串的 DNS-1123 token）
	LedgerRulePods = "kth_run_pods"
	// 第 k 次诊断全部证据的命令视图
	LedgerRuleCommands = "kth_run_commands"
)

// 展开状态：进成功率分母的只有 expanded
const (
	// 期望组全部展开成功，判分有效
	ExpectExpanded = "expanded"
	// 探针引用的第 k 次诊断实际未发生（升格被路由成回复），不进分母单列报告
	ExpectNoDiagnosis = "no_diagnosis"
	// 诊断发生了但账本里抽不出该类事实（如无含资源名的证据），不进分母单列报告
	ExpectNoFacts = "no_facts"
)

// DNS-1123 单标签形态：小写字母数字与连字符、首尾为字母数字
// 资源实体名（pod/deployment 等）均为该形态；用于从证据文本机械抽候选
var probeTokenRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// 探针实验规格：场景目录 probe.yaml 的顶层形状
// 与 scenario.yaml 同目录、互不感知；run/judge 主路径零改动
type ProbeSpec struct {
	// 规格名（一般与场景名一致；记录里作 scenario 字段）
	Name string `yaml:"name"`
	// 正式诊断请求模板文本（诊断轮的用户输入；措辞要强到让 Tower 升格）
	DiagnoseRequest string `yaml:"diagnose_request"`
	// 普通问答池（问答轮按种子轮换取用；至少一条）
	QAPool []string `yaml:"qa_pool"`
	// 尾部探针问题池（含真值声明；至少一条）
	Probes []ProbeQuestion `yaml:"probes"`
}

// 单条探针问题与其真值声明
type ProbeQuestion struct {
	// 探针编号（记录内标识，须唯一）
	ID string `yaml:"id"`
	// 类别：evidence / synthesis / chain（供分组统计）
	Class string `yaml:"class"`
	// 探针问题原文（尾部逐条发出）
	Question string `yaml:"question"`
	// 期望组列表：命中 = 每组至少一串被答案包含（大小写不敏感）；
	// 单组即普通包含；综合类靠多组（首/末诊断事实 + 资源名）天然机械可判
	Expect []ExpectGroup `yaml:"expect"`
}

// 一个期望组：静态字面量或 from_ledger 规则，二选一
type ExpectGroup struct {
	// 静态字面量候选（稳定事实，如 deployment 名）
	Literal string `yaml:"literal,omitempty"`
	// 账本抽取规则（运行时展开成候选串）
	FromLedger *LedgerRule `yaml:"from_ledger,omitempty"`
}

// from_ledger 抽取规则：从本会话诊断账本机械展开真值候选串
type LedgerRule struct {
	// 规则名：kth_run_pods / kth_run_commands
	Rule string `yaml:"rule"`
	// 第 k 次诊断（1 起；-1 = 最后一次）；k 超实际诊断数时该探针记 no_diagnosis
	K int `yaml:"k"`
}

// 生成的长会话脚本：轮次序列可复现（同规格同参数两次生成全等）
type ProbeScript struct {
	// 规格名
	Name string
	// 主体轮数 N（探针轮不计入）
	Rounds int
	// 生成种子（记录留档，可复现）
	Seed int64
	// 轮次序列：N 轮主体（qa/diagnose 交替）+ 尾部探针轮
	Turns []ProbeTurn
}

// 单轮计划
type ProbeTurn struct {
	// 轮次类型：qa / diagnose / probe
	Kind string
	// 本轮用户输入原文
	Text string
	// 探针轮的问题编号；其余轮为空
	ProbeID string
}

// LoadProbeSpec 从场景目录的 probe.yaml 读取探针规格
// 路径指向文件本身；解析后全量校验，缺段或非法值在启动期报错，不静默空判
func LoadProbeSpec(path string) (ProbeSpec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ProbeSpec{}, fmt.Errorf("read probe spec: %w", err)
	}
	var spec ProbeSpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return ProbeSpec{}, fmt.Errorf("parse probe spec %s: %w", path, err)
	}
	if err := spec.Validate(); err != nil {
		return ProbeSpec{}, fmt.Errorf("probe spec %s: %w", path, err)
	}
	return spec, nil
}

// Validate 全量校验规格：编号唯一、类别与规则名合法、每期望组恰一来源、k 取值合法
func (s ProbeSpec) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(s.DiagnoseRequest) == "" {
		return fmt.Errorf("diagnose_request is required")
	}
	if len(s.QAPool) == 0 {
		return fmt.Errorf("qa_pool requires at least one entry")
	}
	if len(s.Probes) == 0 {
		return fmt.Errorf("probes requires at least one entry")
	}
	ids := make(map[string]struct{}, len(s.Probes))
	for i, p := range s.Probes {
		if strings.TrimSpace(p.ID) == "" {
			return fmt.Errorf("probes[%d].id is required", i)
		}
		if _, dup := ids[p.ID]; dup {
			return fmt.Errorf("probes[%d].id %q is duplicated", i, p.ID)
		}
		ids[p.ID] = struct{}{}
		switch p.Class {
		case ProbeClassEvidence, ProbeClassSynthesis, ProbeClassChain:
		default:
			return fmt.Errorf("probes[%d].class %q: want evidence|synthesis|chain", i, p.Class)
		}
		if strings.TrimSpace(p.Question) == "" {
			return fmt.Errorf("probes[%d].question is required", i)
		}
		if len(p.Expect) == 0 {
			return fmt.Errorf("probes[%d].expect requires at least one group", i)
		}
		for j, g := range p.Expect {
			hasLiteral := strings.TrimSpace(g.Literal) != ""
			if hasLiteral == (g.FromLedger != nil) {
				return fmt.Errorf("probes[%d].expect[%d]: exactly one of literal or from_ledger", i, j)
			}
			if g.FromLedger != nil {
				switch g.FromLedger.Rule {
				case LedgerRulePods, LedgerRuleCommands:
				default:
					return fmt.Errorf("probes[%d].expect[%d].from_ledger.rule %q: want %s|%s",
						i, j, g.FromLedger.Rule, LedgerRulePods, LedgerRuleCommands)
				}
				if g.FromLedger.K == 0 || g.FromLedger.K < -1 {
					return fmt.Errorf("probes[%d].expect[%d].from_ledger.k %d: want >=1 or -1", i, j, g.FromLedger.K)
				}
			}
		}
	}
	return nil
}

// maxLedgerK 规格里引用的最大诊断序号（-1 记 1；静态校验生成器用）
func (s ProbeSpec) maxLedgerK() int {
	maxK := 0
	for _, p := range s.Probes {
		for _, g := range p.Expect {
			if g.FromLedger == nil {
				continue
			}
			k := g.FromLedger.K
			if k == -1 {
				k = 1
			}
			if k > maxK {
				maxK = k
			}
		}
	}
	return maxK
}

// GenerateProbeScript 按规格铺长会话脚本（固定种子，可复现）
// 布局：首次诊断落在第 3–5 轮（1 起），此后间隔 3–5 轮再诊断，其余轮从问答池
// 按种子轮换；主体 N 轮铺完后按规格顺序追加探针轮
// 主体轮数不足以支撑探针引用的诊断序号时报错（静态校验：k 超脚本实际诊断数）
func GenerateProbeScript(spec ProbeSpec, rounds int, seed int64) (ProbeScript, error) {
	if err := spec.Validate(); err != nil {
		return ProbeScript{}, err
	}
	if rounds < 1 {
		return ProbeScript{}, fmt.Errorf("rounds %d: want >= 1", rounds)
	}
	rng := rand.New(rand.NewSource(seed))
	script := ProbeScript{Name: spec.Name, Rounds: rounds, Seed: seed}
	nextDiag := 3 + rng.Intn(3)
	qaIdx := rng.Intn(len(spec.QAPool))
	for i := 1; i <= rounds; i++ {
		if i == nextDiag {
			script.Turns = append(script.Turns, ProbeTurn{Kind: ProbeTurnDiagnose, Text: spec.DiagnoseRequest})
			nextDiag = i + 3 + rng.Intn(3)
			continue
		}
		script.Turns = append(script.Turns, ProbeTurn{Kind: ProbeTurnQA, Text: spec.QAPool[qaIdx%len(spec.QAPool)]})
		qaIdx++
	}
	for _, p := range spec.Probes {
		script.Turns = append(script.Turns, ProbeTurn{Kind: ProbeTurnProbe, Text: p.Question, ProbeID: p.ID})
	}
	diagCount := 0
	for _, t := range script.Turns {
		if t.Kind == ProbeTurnDiagnose {
			diagCount++
		}
	}
	if maxK := spec.maxLedgerK(); maxK > diagCount {
		return ProbeScript{}, fmt.Errorf(
			"rounds %d only fits %d diagnose turns; probes reference run k=%d (increase rounds or lower k)",
			rounds, diagCount, maxK)
	}
	return script, nil
}

// ExpandExpectations 把探针期望组展开成字面量候选串（判分①层的机械输入）
// resourceName 为场景真值资源名（kth_run_pods 的包含子串过滤键）
// 返回展开后的组列表与状态：expanded 进分母；no_diagnosis / no_facts 不进分母单列；
// 规格非法（k 越界值）在本函数不会再出现——规格校验已拦，此处只处理运行期缺失
func ExpandExpectations(
	probes []ProbeQuestion,
	probeID string,
	records []session.DiagnosticRecord,
	resourceName string,
) ([][]string, string, error) {
	var q *ProbeQuestion
	for i := range probes {
		if probes[i].ID == probeID {
			q = &probes[i]
			break
		}
	}
	if q == nil {
		return nil, "", fmt.Errorf("probe %q not found in spec", probeID)
	}
	groups := make([][]string, 0, len(q.Expect))
	for _, g := range q.Expect {
		// 与规格校验（hasLiteral）同口径 Trim（pr-agent R1 采纳）：字面量首尾空白
		// 在 Contains 判分下永不命中（假阴性）；Trim 后为空的条目必带 from_ledger
		// （校验期 exactly-one 已拦），落到下方账本展开
		if lit := strings.TrimSpace(g.Literal); lit != "" {
			groups = append(groups, []string{lit})
			continue
		}
		k := g.FromLedger.K
		if k == -1 {
			k = len(records)
		}
		if k < 1 || k > len(records) {
			return nil, ExpectNoDiagnosis, nil
		}
		var cands []string
		switch g.FromLedger.Rule {
		case LedgerRulePods:
			cands = podCandidates(records[k-1], resourceName)
		case LedgerRuleCommands:
			cands = commandCandidates(records[k-1])
		}
		if len(cands) == 0 {
			return nil, ExpectNoFacts, nil
		}
		groups = append(groups, cands)
	}
	return groups, ExpectExpanded, nil
}

// podCandidates 从单次诊断的证据文本机械抽含资源名子串的实体名候选
// 只看摘要与原带（命令视图一般是资源名参数而非输出）；按出现序去重
// kind 生成的 pod 名带随机后缀，无法静态埋值——这是 from_ledger 展开的根本动机
func podCandidates(rec session.DiagnosticRecord, resourceName string) []string {
	resourceName = strings.ToLower(strings.TrimSpace(resourceName))
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, e := range rec.Evidence {
		text := e.Summary + "\n" + string(e.Raw)
		for _, tok := range strings.Fields(text) {
			tok = strings.Trim(tok, "\"'`([{<>,.;:!?)]}")
			if len(tok) < 3 || len(tok) > 63 {
				continue
			}
			if !probeTokenRe.MatchString(tok) {
				continue
			}
			if resourceName != "" && !strings.Contains(tok, resourceName) {
				continue
			}
			if _, dup := seen[tok]; dup {
				continue
			}
			seen[tok] = struct{}{}
			out = append(out, tok)
		}
	}
	return out
}

// commandCandidates 取单次诊断全部证据的命令视图（去空去重，保序）
// 每条命令视图一个候选串：判分包含判定下，答案含任一实际执行过的命令即算命中
func commandCandidates(rec session.DiagnosticRecord) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, e := range rec.Evidence {
		cv := strings.TrimSpace(e.CommandView)
		if cv == "" {
			continue
		}
		if _, dup := seen[cv]; dup {
			continue
		}
		seen[cv] = struct{}{}
		out = append(out, cv)
	}
	return out
}
