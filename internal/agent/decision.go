// 决策规划与强度判定的输出结构：主动取证决策循环（0.1.2）的 LLM 语义素材载体
//
// 分工边界（与 acquire 包对齐）：LLM 只产出语义素材——假设先验、动作提议、
// 判别矩阵 D(o|hᵢ)、(d,s) 强度判定；本文件负责把这些素材从 JSON 解析成结构、
// 并做机械校验（形状、值域、互斥），不做任何语义判断与算术
//
// 容错口径（冻结裁决）：判别矩阵按动作级容错——非法动作（形状错、负值、
// 非有限值）丢弃并计数，计划级只要剩一个有效动作即成功，全坏才报错；
// 丢弃必须可观测（DroppedActions 计数 + 调用方日志），不得静默吞（#18）
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/Aruing/Aruing/internal/core"
)

// 问用户动作的固定成本粗档：约十倍最轻工具（裁决与思考文档 §6 对齐——
// 成本模型让 argmax EIG/c 自动推迟问用户，代码无需特判）；接线层可再覆写
const askCost = 10

// 一次决策规划的产物：带先验的候选假设 + 带判别矩阵的取证动作提议
//
// 不是持久化实体：假设的系统编号由决策规划器经工厂回填，动作提议在执行时
// 由编排映射为 core.Task（#6：Task 不加专用引用字段），两者都不直接入库
type PlanDecision struct {
	// 候选假设列表，顺序即判别矩阵的行序；先验已写入各假设的 Confidence，
	// 系统编号与创建时间由决策规划器经工厂回填
	Hypotheses []core.Hypothesis
	// 有效动作提议列表；非法动作已在解析期丢弃，不进入本列表
	Actions []ActionProposal
	// 解析期被丢弃的非法动作数量；观测用，调用方应记日志供实验归因
	DroppedActions int
}

// 一条取证动作提议：可执行参数 + 预期结果的判别矩阵
//
// 形状由冻结裁决固定为 kubectl argv 朝向（当前后端只有 k8s 工具）；
// 执行时由接线层映射 core.Task（ToolName=后端工具，Arguments={"argv":...}），
// 本结构不进 core（动作提议是规划中间态，执行才落 Task/Evidence）
type ActionProposal struct {
	// 动作名，人读标识，计划内唯一；用于日志、实验记录与编排映射
	Name string
	// 只读 kubectl 参数（不含 kubectl 本身），例如 ["get","pods","-n","demo"]
	// 为空表示非工具动作（问用户）；与 Ask 互斥
	Argv []string
	// 问用户动作的问题文本；非空表示问用户，此时 Argv 必须为空
	// 动作的结果类别（Outcomes）即用户可能给出的回答类别
	Ask string
	// 本次取证要证明或排除什么；映射 Task 时作为 Purpose
	Purpose string
	// 成本粗档：1 轻查 / 2 普查 / 5 重扫；问用户动作固定 askCost
	// 解析期已归一为正有限值（非法值回 1，与 acquire.NewAction 同口径）
	Cost float64
	// 结果类别名列表，顺序即矩阵列序；须唯一且非空
	Outcomes []string
	// 判别矩阵 d[i][j] = P(Outcomes[j] | 第 i 个假设成立)，行序对齐
	// PlanDecision.Hypotheses；行归一交给 acquire.NewAction 在构造期做
	Matrix [][]float64
}

// 判定一条富文本证据相对每个假设的方向与强度（(d,s) 素材）
//
// 由验证器兼职产出（冻结裁决：决策循环内每观测一次的轻调用，MSPRT 停止后
// 才出正式 Verdict）；数值语义与 acquire.StrengthEvidence 对齐——方向 +1 支持 /
// 0 无关 / −1 反驳，强度 [0,1]，消费侧组装 StrengthEvidence 做贝叶斯更新
type StrengthJudgement struct {
	// 对应假设的系统编号，必须属于输入假设列表
	HypothesisID string
	// 方向：+1 支持 / 0 无关 / −1 反驳；由枚举字符串解析而来
	Direction int
	// 强度 [0,1]，越界已钳位；无关方向时强度无更新作用，钳位后通常为零
	Strength float64
}

// 模型侧的决策规划输出，只含内容，不含系统编号或时间
type decisionLLMOutput struct {
	// 候选假设列表，顺序即动作矩阵的行序
	Hypotheses []decisionHypothesisOut `json:"hypotheses"`
	// 动作提议列表
	Actions []decisionActionOut `json:"actions"`
}

// 模型侧一条假设，先验随语句一起给出
type decisionHypothesisOut struct {
	// 候选原因描述
	Statement string `json:"statement"`
	// 提出理由
	Reason string `json:"reason"`
	// 预期可观测信号
	ExpectedSignals []string `json:"expected_signals"`
	// 先验可信度，[0,1]，相对权重不必归一
	Prior float64 `json:"prior"`
}

// 模型侧一条动作提议：argv 与 ask 二选一
type decisionActionOut struct {
	// 动作名，计划内唯一
	Name string `json:"name"`
	// 只读 kubectl 参数；问用户动作省略
	Argv []string `json:"argv"`
	// 问用户动作的问题文本；工具动作省略
	Ask string `json:"ask"`
	// 取证目的
	Purpose string `json:"purpose"`
	// 成本粗档（1/2/5；问用户 10）
	Cost float64 `json:"cost"`
	// 结果类别名，顺序即矩阵列序
	Outcomes []string `json:"outcomes"`
	// 判别矩阵，行 = 假设序，列 = 结果序
	Matrix [][]float64 `json:"matrix"`
}

// 解析并校验决策规划输出：假设进 Plan 级校验（缺失即报错），动作按动作级容错
// （非法丢弃并计数，至少一个有效动作才成功）
//
// 行归一不在本层做（acquire.NewAction 构造期归一）；本层只挡形状与值域
func parseDecisionOutput(data []byte) (PlanDecision, error) {
	var out decisionLLMOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return PlanDecision{}, fmt.Errorf("decision output: %w", err)
	}

	// 假设是 EIG 的行坐标系，缺失或空语句让矩阵无从对齐，整计划拒绝
	if len(out.Hypotheses) == 0 {
		return PlanDecision{}, errors.New("decision output requires at least one hypothesis")
	}
	decision := PlanDecision{
		Hypotheses: make([]core.Hypothesis, 0, len(out.Hypotheses)),
	}
	for i, h := range out.Hypotheses {
		if strings.TrimSpace(h.Statement) == "" {
			return PlanDecision{}, fmt.Errorf("decision hypothesis[%d] statement is required", i)
		}
		// 先验直接进 Confidence（语义随写入方界定：这里写的是先验）；
		// 是相对权重不必归一，全部为零表示模型未表达倾向，消费方回退均匀先验
		decision.Hypotheses = append(decision.Hypotheses, core.Hypothesis{
			Statement:       strings.TrimSpace(h.Statement),
			Reason:          strings.TrimSpace(h.Reason),
			ExpectedSignals: append([]string(nil), h.ExpectedSignals...),
			Confidence:      clampProbability(h.Prior),
		})
	}

	names := make(map[string]struct{}, len(out.Actions))
	for _, a := range out.Actions {
		proposal, err := buildActionProposal(a, len(out.Hypotheses))
		if err != nil {
			// 动作级容错：LLM 输出有随机性，一个坏动作不该毁掉整个计划；
			// 丢弃必须计数，调用方记日志保证可观测（#18 不静默吞）
			decision.DroppedActions++
			continue
		}
		if _, dup := names[proposal.Name]; dup {
			decision.DroppedActions++
			continue
		}
		names[proposal.Name] = struct{}{}
		decision.Actions = append(decision.Actions, proposal)
	}
	if len(decision.Actions) == 0 {
		return PlanDecision{}, errors.New("decision output requires at least one valid action")
	}
	return decision, nil
}

// 校验并组装单条动作提议；任何形状或值域违规返回错误（由调用方按动作级容错丢弃）
func buildActionProposal(a decisionActionOut, hypothesisCount int) (ActionProposal, error) {
	name := strings.TrimSpace(a.Name)
	if name == "" {
		return ActionProposal{}, errors.New("action name is required")
	}

	ask := strings.TrimSpace(a.Ask)
	hasArgv := len(a.Argv) > 0
	if ask != "" && hasArgv {
		return ActionProposal{}, errors.New("action cannot carry both argv and ask")
	}
	if ask == "" && !hasArgv {
		return ActionProposal{}, errors.New("action requires either argv or ask")
	}
	argv := make([]string, 0, len(a.Argv))
	for _, arg := range a.Argv {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			return ActionProposal{}, errors.New("action argv contains empty value")
		}
		argv = append(argv, arg)
	}

	// 问用户成本固定粗档（模型不该自行压价问用户）；工具动作非法成本回 1，
	// 与 acquire.NewAction 的归一口径一致，不因此丢弃整个动作
	cost := a.Cost
	if ask != "" {
		cost = askCost
	} else if !(cost > 0) || math.IsInf(cost, 0) { // NaN 与零/负一并归一
		cost = 1
	}

	outcomes := make([]string, 0, len(a.Outcomes))
	seen := make(map[string]struct{}, len(a.Outcomes))
	for _, o := range a.Outcomes {
		o = strings.TrimSpace(o)
		if o == "" {
			return ActionProposal{}, errors.New("action outcome name is required")
		}
		if _, dup := seen[o]; dup {
			return ActionProposal{}, errors.New("action outcome names must be unique")
		}
		seen[o] = struct{}{}
		outcomes = append(outcomes, o)
	}
	if len(outcomes) == 0 {
		return ActionProposal{}, errors.New("action requires at least one outcome")
	}

	// 矩阵形状：行数对齐假设数（缺行会被当作概率 1 预测，必须拒绝），
	// 列数对齐结果数；元素非负有限（NaN/±Inf 经行归一会静默污染下游）
	if len(a.Matrix) != hypothesisCount {
		return ActionProposal{}, errors.New("action matrix rows must align with hypotheses")
	}
	matrix := make([][]float64, len(a.Matrix))
	for i, row := range a.Matrix {
		if len(row) != len(outcomes) {
			return ActionProposal{}, errors.New("action matrix columns must align with outcomes")
		}
		matrix[i] = make([]float64, len(row))
		for j, v := range row {
			if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
				return ActionProposal{}, errors.New("action matrix entries must be non-negative finite")
			}
			matrix[i][j] = v
		}
	}

	return ActionProposal{
		Name:     name,
		Argv:     argv,
		Ask:      ask,
		Purpose:  strings.TrimSpace(a.Purpose),
		Cost:     cost,
		Outcomes: outcomes,
		Matrix:   matrix,
	}, nil
}

// 模型侧的强度判定输出，逐假设一条
type strengthLLMOutput struct {
	// 对每条输入假设的判定列表
	Judgements []strengthJudgementOut `json:"judgements"`
}

// 模型侧一条强度判定，方向为枚举字符串
type strengthJudgementOut struct {
	// 对应输入假设的系统编号
	HypothesisID string `json:"hypothesis_id"`
	// 方向：supports 支持 / refutes 反驳 / irrelevant 无关
	Direction string `json:"direction"`
	// 强度 [0,1]，越界钳位
	Strength float64 `json:"strength"`
}

// 解析并校验强度判定输出：每条输入假设恰好一条，方向枚举合法，强度钳位
// 与 Verify 同级的严格口径——这是 EIG 决策的燃料，漏判或错枚举让更新静默失真
func parseStrengthOutput(data []byte, hypothesisIDs []string) ([]StrengthJudgement, error) {
	var out strengthLLMOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("strength output: %w", err)
	}
	if len(out.Judgements) == 0 {
		return nil, errors.New("at least one strength judgement is required")
	}

	known := make(map[string]struct{}, len(hypothesisIDs))
	for _, id := range hypothesisIDs {
		known[id] = struct{}{}
	}
	byID := make(map[string]StrengthJudgement, len(out.Judgements))
	for i, j := range out.Judgements {
		hypID := strings.TrimSpace(j.HypothesisID)
		if hypID == "" {
			return nil, fmt.Errorf("strength judgement[%d] hypothesis_id is required", i)
		}
		if _, ok := known[hypID]; !ok {
			return nil, fmt.Errorf("strength judgement[%d] references unknown hypothesis %q", i, hypID)
		}
		if _, dup := byID[hypID]; dup {
			return nil, fmt.Errorf("strength judgement[%d] duplicates hypothesis %q", i, hypID)
		}
		direction, err := parseStrengthDirection(j.Direction)
		if err != nil {
			return nil, fmt.Errorf("strength judgement[%d] %w", i, err)
		}
		byID[hypID] = StrengthJudgement{
			HypothesisID: hypID,
			Direction:    direction,
			Strength:     clampProbability(j.Strength),
		}
	}
	// 漏判假设即报错：消费方按假设索引组装更新向量，缺项无从回退
	for _, id := range hypothesisIDs {
		if _, ok := byID[id]; !ok {
			return nil, fmt.Errorf("missing strength judgement for hypothesis %q", id)
		}
	}

	judgements := make([]StrengthJudgement, 0, len(hypothesisIDs))
	for _, id := range hypothesisIDs {
		judgements = append(judgements, byID[id])
	}
	return judgements, nil
}

// 把模型侧方向枚举解析为 acquire 的数值方向：supports→+1 / refutes→−1 / irrelevant→0
func parseStrengthDirection(direction string) (int, error) {
	switch strings.TrimSpace(strings.ToLower(direction)) {
	case "supports":
		return 1, nil
	case "refutes":
		return -1, nil
	case "irrelevant":
		return 0, nil
	default:
		return 0, fmt.Errorf("direction %q is invalid", direction)
	}
}

// 概率值钳位到 [0,1]（先验与强度共用；方向不钳——非法方向是解析错误）
// NaN 显式归零：两比较皆 false 会原样穿透，污染下游对数运算
func clampProbability(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
