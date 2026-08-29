// 表格投影方法开关、预算口径与渲染长度工具
//
// 所有方法都是机械投影（#19）：不按资源类型映射、不下健康判断
// fast 是产品默认（beta12 三段式）；greedy 系是加权贪心；
// full / head-tail / uniform 是实验基线（C1 / C2 / C3）——同一二进制经配置切换，
// 排除实现差异对对比实验的干扰；C4（LLM 重排）的方法分发在此层，
// 重排器回调由实验框架在装配层注入（默认不装配，产品路径纯机械不变）

package summary

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// DefaultBudgetRunes 投影实例行的默认 rune 预算
// 与 fast 路径实际占用同量级（头尾 + 异常 + 覆盖约 28 行 + 列频次），保证方法间同预算公平对比
const DefaultBudgetRunes = 4096

// Method 投影方法；零值即 MethodFast，零值 RenderOptions 与历史 Render 行为一致
type Method int

const (
	// 三段式快路径：列频次 + PCA 异常段 + 取值覆盖段（beta12 实现）
	MethodFast Method = iota
	// 加权贪心代表性投影：覆盖 + T² 异常目标，头尾锚预置，基数折算 lazy greedy
	MethodGreedy
	// 加权贪心的 knapsack 对照变体：按收益/行长装入，无基数折算、无 1−1/e 口径
	MethodGreedyKnapsack
	// C1 全量注入基线：所有行原样渲染，不设预算（预算充足时的上限参照）
	MethodFull
	// C2 头尾截断基线：预算对半装头尾行（业界默认形态，中段系统性丢失的来源）
	MethodHeadTail
	// C3 均匀采样基线：等步长抽行装满预算（无结构参照）
	MethodUniform
	// C4 LLM 重排对照臂：经 Rerank 回调让模型选行（实验专用，须显式配置且装配重排器）
	// 选行后的装入与标注仍是机械的；选行本身是模型行为，不进产品默认路径（#19）
	MethodLLMRerank
)

// methodNames 配置字符串到方法的映射；名字一经使用不再改动（config / env / bench 依赖）
var methodNames = map[string]Method{
	"fast":            MethodFast,
	"greedy":          MethodGreedy,
	"greedy-knapsack": MethodGreedyKnapsack,
	"full":            MethodFull,
	"head-tail":       MethodHeadTail,
	"uniform":         MethodUniform,
	"llm-rerank":      MethodLLMRerank,
}

// ParseMethod 解析配置字符串为方法
// 空串 = 默认 fast；未知值返回错误——启动明确失败，不静默回落（#18 精神）
func ParseMethod(s string) (Method, error) {
	if s == "" {
		return MethodFast, nil
	}
	m, ok := methodNames[s]
	if !ok {
		return MethodFast, fmt.Errorf("unknown projection method %q (want one of fast, greedy, greedy-knapsack, full, head-tail, uniform, llm-rerank)", s)
	}
	return m, nil
}

// RenderOptions 投影渲染选项；零值 = fast 默认路径（历史行为，既有调用方零改）
type RenderOptions struct {
	// 投影方法；零值 MethodFast
	Method Method
	// 实例行 rune 预算，只影响 greedy / head-tail / uniform；<=0 用 DefaultBudgetRunes
	BudgetRunes int
	// 贪心 f_anom 权重 λ；<=0 视为 1（λ 的对照经 GreedyOptions 的开关项表达）
	Lambda float64
	// 覆盖权重均匀化（对照实验）；默认 log2 稀有度加权
	UniformWeight bool
	// 简单统计量消融臂（bench 注入）：异常分不用 PCA/T²，用行长 |z| + 非常规取值计数
	// 不进 config 与方法名——消融臂不是产品开关，配置面零暴露
	SimpleStat bool
	// C4 LLM 重排回调；method=llm-rerank 时必非 nil（装配层校验，渲染层防御性报错）
	Rerank RerankFunc
}

// RerankFunc C4 LLM 重排回调：输入全表列、行与实例行 rune 预算，返回选中的 0 基行号
// 由实验框架在装配层注入（k8s reranker 用 llm 客户端构造）；summary 不感知模型。
// 回调无须排序去重：渲染层机械过滤越界/重复后按表序装入预算。
// 无 ctx：回调方自行负责超时（llm 客户端自带整体超时），实验专用臂不污染渲染签名
type RerankFunc func(columns []string, rows [][]string, budgetRunes int) ([]int, error)

// budget 返回生效预算（零值归一到默认）
func (o RenderOptions) budget() int {
	if o.BudgetRunes <= 0 {
		return DefaultBudgetRunes
	}
	return o.BudgetRunes
}

// RowRunes 一行按 WriteRows 口径渲染的 rune 长度（两空格缩进 + 两空格连单元格）
// 预算记账必须与渲染口径一致；导出供实验侧（bench 随机臂）统一记账口径
func RowRunes(row []string) int {
	return 2 + utf8.RuneCountInString(strings.Join(row, "  "))
}

// renderedRowRunes 同口径的包内旧名；保留避免大画幅改动
func renderedRowRunes(row []string) int { return RowRunes(row) }

// MedianRowRunes 给定行集的中位渲染行长；贪心基数折算与均匀采样折算目标行数用
// 空行集返回 0，由调用方兜底；导出供 bench 随机臂按同一口径折算基数
func MedianRowRunes(rows [][]string) int {
	if len(rows) == 0 {
		return 0
	}
	costs := make([]int, len(rows))
	for i, r := range rows {
		costs[i] = RowRunes(r)
	}
	return medianInt(costs)
}

// medianInt 中位数；偶数个取中间两数均值（向下取整），保证确定性
// 调用方保证非空
func medianInt(vals []int) int {
	sorted := append([]int(nil), vals...)
	sort.Ints(sorted)
	m := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[m]
	}
	return (sorted[m-1] + sorted[m]) / 2
}

// reverseInts 原序的反转副本；头尾基线从尾部倒序收集后按表序渲染用
func reverseInts(in []int) []int {
	out := make([]int, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}
