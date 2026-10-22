// 场景规格读取：scenario.yaml 的 ground_truth 与 difficulty 两段
//
// difficulty 段只存原子事实（可数的设计意图），六维分数由 DeriveDifficulty 机械派生；
// 分数不写进场景文件，避免人直接给分不可复现、无法辩护
// 两条读取口径：判分侧走 LoadScenarioSpec（严格：缺段或越界报错），
// 展示侧走 LoadScenarioSpecs（宽松：缺段保留 nil，界面显示占位而不中断整表）
package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// 输出体量的两档取值；其他取值视为非法，写错档位会让派生分数撒谎
const (
	// 输出体量普通，投影 / 分片 / 落盘页式路径不被逼出来
	OutputScalePlain = "plain"
	// 输出体量巨大，逼出投影 / 分片 / 落盘页式路径
	OutputScaleLarge = "large"
)

// Difficulty 场景难度的原子事实；scenario.yaml 的 difficulty 段
// 只放能从场景文件数出来的量，不放结论性评分
type Difficulty struct {
	// 真故障个数，不含诱饵
	Faults int `yaml:"faults" json:"faults"`
	// 闭环所需不同来源证据的种数（事件 / 容器状态 / 日志 / 配置 / 网络策略等）
	EvidenceSources int `yaml:"evidence_sources" json:"evidence_sources"`
	// 故意埋的假线索个数
	Decoys int `yaml:"decoys" json:"decoys"`
	// 输出体量档位：plain | large
	OutputScale string `yaml:"output_scale" json:"output_scale"`
	// 是否权限或能力受限（如 RBAC 拒绝读、数据面无法实测）
	CapabilityRestricted bool `yaml:"capability_restricted" json:"capability_restricted"`
	// 跨层数：命名空间 / 节点 / CRD / 控制面与数据面之间跨越的层数
	Layers int `yaml:"layers" json:"layers"`
	// 设计所需会话轮数，单轮记 1
	SessionRounds int `yaml:"session_rounds" json:"session_rounds"`
	// 信息缺口：正确答案是否要求主动澄清或明确报缺口；横切标记，不进总分
	InfoGap bool `yaml:"info_gap" json:"info_gap"`
}

// DifficultyScore 六维派生分数与总分；字段顺序即展示顺序
type DifficultyScore struct {
	// 故障数维得分（0-3）
	Faults int `json:"faults"`
	// 证据链深度维得分（0-3）
	EvidenceChain int `json:"evidence_chain"`
	// 诱饵维得分（0-3）
	Decoys int `json:"decoys"`
	// 环境严苛度维得分（0-3）
	Environment int `json:"environment"`
	// 跨层维得分（0-3）
	Layers int `json:"layers"`
	// 会话长度维得分（0-3）
	SessionLength int `json:"session_length"`
	// 六维之和（0-18）；不设权重，保持可直接复算
	Total int `json:"total"`
}

// 把原子事实按固定规则折算成六维分数
// 规则集中在此处：调整规则即全员重算，避免各场景分数口径漂移
func DeriveDifficulty(d Difficulty) DifficultyScore {
	s := DifficultyScore{
		Faults:        scaleUpTo3(d.Faults),
		EvidenceChain: scaleUpTo3(d.EvidenceSources),
		Decoys:        scaleUpTo3(d.Decoys),
		Layers:        scaleUpTo3(d.Layers),
		SessionLength: sessionLengthScore(d.SessionRounds),
		Environment:   environmentScore(d.OutputScale, d.CapabilityRestricted),
	}
	s.Total = s.Faults + s.EvidenceChain + s.Decoys + s.Environment +
		s.Layers + s.SessionLength
	return s
}

// 计数量折算：0 折 0 分（没有即不是难度来源），1 / 2 / 3 以上折 1 / 2 / 3 分
func scaleUpTo3(n int) int {
	switch {
	case n <= 0:
		return 0
	case n == 1:
		return 1
	case n == 2:
		return 2
	default:
		return 3
	}
}

// 会话长度折算：单轮 1 分，2-3 轮 2 分，4 轮及以上 3 分
// 非法输入（0 或负数）折 0 分而不是默认 1 分：让未校验的零值整体折出全零，
// 避免「未标注」被派生成一个看起来正常的非零分
func sessionLengthScore(rounds int) int {
	switch {
	case rounds <= 0:
		return 0
	case rounds == 1:
		return 1
	case rounds <= 3:
		return 2
	default:
		return 3
	}
}

// 环境严苛度折算：体量档位 plain 0 / large 2 分，能力受限 +1 分，两者兼有 3 分
func environmentScore(scale string, restricted bool) int {
	score := 0
	if scale == OutputScaleLarge {
		score += 2
	}
	if restricted {
		score++
	}
	return score
}

// ScenarioSpec 一个场景的全部可读事实；scenario.yaml 的顶层形状
type ScenarioSpec struct {
	// 场景名（manifest 自述，与目录名一致）
	Name string `yaml:"name"`
	// 场景真值段；判分①②的输入
	GroundTruth GroundTruth `yaml:"ground_truth"`
	// 难度原子事实；缺段时为 nil，表示尚未标注
	Difficulty *Difficulty `yaml:"difficulty"`
}

// 读入场景文件原始字节，统一包装读取错误
func readScenarioFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scenario: %w", err)
	}
	return raw, nil
}

// 宽松读入场景文件，只校验 YAML 形状与必填真值
// 判分与展示两条口径共用，差异只在是否进一步校验 difficulty
func parseScenarioFile(path string) (ScenarioSpec, error) {
	raw, err := readScenarioFile(path)
	if err != nil {
		return ScenarioSpec{}, err
	}
	var spec ScenarioSpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return ScenarioSpec{}, fmt.Errorf("parse scenario %s: %w", path, err)
	}
	if strings.TrimSpace(spec.GroundTruth.ResourceName) == "" {
		return ScenarioSpec{}, fmt.Errorf("scenario %s: ground_truth.resource_name is required", path)
	}
	return spec, nil
}

// 严格读入场景文件，供判分与理论 / 实际对比使用
// 缺 difficulty 段或原子事实越界时报错：静默补零会让难度表撒谎
func LoadScenarioSpec(path string) (ScenarioSpec, error) {
	spec, err := parseScenarioFile(path)
	if err != nil {
		return ScenarioSpec{}, err
	}
	if spec.Difficulty == nil {
		return ScenarioSpec{}, fmt.Errorf("scenario %s: difficulty is required", path)
	}
	if err := validateDifficulty(*spec.Difficulty); err != nil {
		return ScenarioSpec{}, fmt.Errorf("scenario %s: %w", path, err)
	}
	return spec, nil
}

// 校验原子事实取值；越界一律报错而不是夹取，避免展示与实际口径分叉
func validateDifficulty(d Difficulty) error {
	if d.OutputScale != OutputScalePlain && d.OutputScale != OutputScaleLarge {
		return fmt.Errorf("difficulty.output_scale %q: use plain or large", d.OutputScale)
	}
	for _, f := range []struct {
		name string
		val  int
	}{
		{"faults", d.Faults},
		{"evidence_sources", d.EvidenceSources},
		{"decoys", d.Decoys},
		{"layers", d.Layers},
		{"session_rounds", d.SessionRounds},
	} {
		if f.val < 0 {
			return fmt.Errorf("difficulty.%s must not be negative", f.name)
		}
	}
	// 没有真故障的场景不构成故障场景，写成 0 多半是笔误
	if d.Faults < 1 {
		return fmt.Errorf("difficulty.faults must be at least 1")
	}
	if d.SessionRounds < 1 {
		return fmt.Errorf("difficulty.session_rounds must be at least 1")
	}
	return nil
}

// groundTruthOnly 判分侧的形状：只解 ground_truth 段，difficulty 段整段忽略
// 难度标注与判分真值是两条独立数据，难度写坏不得让场景变得不可判分
// （unmarshal 仍会标记未知字段为忽略，但不会因未知字段的类型报错）
type groundTruthOnly struct {
	// 场景真值段；判分①②的输入
	GroundTruth GroundTruth `yaml:"ground_truth"`
}

// ScenarioEntry 场景发现结果；单场景失败不影响其他场景
type ScenarioEntry struct {
	// 场景目录名，脚本与 kind 集群名都以它索引
	Name string
	// 场景文件路径
	Path string
	// 宽松读入的规格；读取失败时为零值
	Spec ScenarioSpec
	// 读取或校验失败原因；非空表示该场景的难度数据不可用
	Err error
}

// 遍历场景根目录，按目录名升序返回所有含 manifests/ 的场景
// 宽松口径：单场景读失败不中断列表，原因留在 Err 里由展示层提示
func LoadScenarioSpecs(root string) ([]ScenarioEntry, error) {
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read scenarios dir: %w", err)
	}
	entries := make([]ScenarioEntry, 0, len(dirs))
	for _, de := range dirs {
		// 跳过非目录与隐藏目录（.kube / .data 等运行期产物）
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, de.Name())
		// manifests/ 是「这是一个场景」的判据，与脚本侧口径一致
		if st, err := os.Stat(filepath.Join(dir, "manifests")); err != nil || !st.IsDir() {
			continue
		}
		entry := ScenarioEntry{Name: de.Name(), Path: filepath.Join(dir, "scenario.yaml")}
		spec, err := parseScenarioFile(entry.Path)
		switch {
		case err != nil:
			entry.Err = err
		case spec.Difficulty != nil:
			// 有标注但取值越界：数据不可用，交由展示层显示占位
			if verr := validateDifficulty(*spec.Difficulty); verr != nil {
				entry.Err = fmt.Errorf("scenario %s: %w", entry.Path, verr)
			}
		}
		entry.Spec = spec
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}
