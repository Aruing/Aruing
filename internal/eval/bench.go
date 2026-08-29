// bench 遍历：矩阵（表大小 × 预算 × 根因位置 × 种子 × 方法）逐单元「生成 → 投影 → 机械判分」
//
// 主实验跑数框架（实验设计框架 §四）：机械判分确定，重复语义 = 种子数——
// 每个组合 5 个种子即 5 张不同表实例，统计强度优于同表重复；LLM 路径（C4 / 下游）另行真重复
// 本包只做机械循环与 CSV 写出，不调集群、不调模型；llm-rerank 不在 bench（走产品路径需装配重排器）

package eval

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Aruing/Aruing/internal/tools/summary"
	"gopkg.in/yaml.v3"
)

// BenchMatrix 遍历矩阵；各维笛卡尔积，单元数 = 各维长度之积
type BenchMatrix struct {
	// 表行数 N（含根因行）
	Rows []int `yaml:"rows"`
	// 实例行 rune 预算
	Budgets []int `yaml:"budgets"`
	// 根因位置（行号百分比 0–100）
	Positions []int `yaml:"positions"`
	// 随机种子；每个种子生成一张独立表实例
	Seeds []int64 `yaml:"seeds"`
	// 方法臂名：fast / greedy / greedy-knapsack / full / head-tail / uniform / random / simplestat
	Methods []string `yaml:"methods"`
}

// DefaultBenchMatrix 默认全量矩阵：3×4×5×5×8 = 2400 单元，每单元毫秒级
// 全跑不筛选：机械部分成本可忽略，全量报告（统计纪律）
func DefaultBenchMatrix() BenchMatrix {
	return BenchMatrix{
		Rows:      []int{100, 200, 500},
		Budgets:   []int{512, 1024, 2048, 4096},
		Positions: []int{5, 25, 50, 75, 95},
		Seeds:     []int64{1, 2, 3, 4, 5},
		Methods:   []string{"fast", "greedy", "greedy-knapsack", "full", "head-tail", "uniform", "random", "simplestat"},
	}
}

// 机械臂名集合；llm-rerank 不在 bench——它需要装配 LLM 重排器，属产品路径实验
var benchMethods = map[string]bool{
	"fast": true, "greedy": true, "greedy-knapsack": true, "full": true,
	"head-tail": true, "uniform": true, "random": true, "simplestat": true,
}

// Validate 校验矩阵：各维非空、数值合法、方法臂受支持
// 非法矩阵报错不静默修正——位置是实验变量，钳位会毁掉分桶口径
func (m BenchMatrix) Validate() error {
	if len(m.Rows) == 0 || len(m.Budgets) == 0 || len(m.Positions) == 0 || len(m.Seeds) == 0 || len(m.Methods) == 0 {
		return fmt.Errorf("bench matrix: rows/budgets/positions/seeds/methods must all be non-empty")
	}
	for _, n := range m.Rows {
		if n <= 0 {
			return fmt.Errorf("bench matrix: rows must be positive, got %d", n)
		}
	}
	for _, b := range m.Budgets {
		if b <= 0 {
			return fmt.Errorf("bench matrix: budgets must be positive, got %d", b)
		}
	}
	for _, p := range m.Positions {
		if p < 0 || p > 100 {
			return fmt.Errorf("bench matrix: positions must be percent in [0,100], got %d", p)
		}
	}
	for _, name := range m.Methods {
		if name == "llm-rerank" {
			return fmt.Errorf("bench matrix: llm-rerank is not a mechanical arm (requires LLM reranker; use the product path)")
		}
		if !benchMethods[name] {
			return fmt.Errorf("bench matrix: unknown method %q (want one of fast, greedy, greedy-knapsack, full, head-tail, uniform, random, simplestat)", name)
		}
	}
	return nil
}

// Units 矩阵展开后的单元总数
func (m BenchMatrix) Units() int {
	return len(m.Rows) * len(m.Budgets) * len(m.Positions) * len(m.Seeds) * len(m.Methods)
}

// LoadBenchMatrix 从 YAML 文件读矩阵并校验
func LoadBenchMatrix(path string) (BenchMatrix, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return BenchMatrix{}, err
	}
	var m BenchMatrix
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return BenchMatrix{}, fmt.Errorf("parse bench matrix %s: %w", path, err)
	}
	if err := m.Validate(); err != nil {
		return BenchMatrix{}, err
	}
	return m, nil
}

// BenchResult 一个 bench 单元的判分结果；CSV 一行
type BenchResult struct {
	Method         string
	N              int
	Budget         int
	PositionBucket int
	RootRow        int
	Seed           int64
	Hit            bool
	ProjectedRows  int
	ProjectRunes   int
	WallMS         int64
}

// RunBench 展开矩阵逐单元跑：生成表 → 按方法臂渲染 → ProjectionHit 机械判分
// 遍历序固定（N → 位置 → 预算 → 种子 → 方法），CSV 逐字节可复现（wall_ms 除外）
func RunBench(m BenchMatrix) ([]BenchResult, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	var results []BenchResult
	for _, n := range m.Rows {
		for _, pos := range m.Positions {
			rootRow := n * pos / 100
			if rootRow >= n {
				rootRow = n - 1
			}
			for _, budget := range m.Budgets {
				for _, seed := range m.Seeds {
					tbl, err := GenerateTable(TableSpec{Rows: n, RootRow: rootRow, Seed: seed})
					if err != nil {
						return nil, fmt.Errorf("generate table: %w", err)
					}
					for _, method := range m.Methods {
						start := time.Now()
						text, stats, err := renderArm(method, tbl, budget, seed)
						if err != nil {
							return nil, err
						}
						results = append(results, BenchResult{
							Method:         method,
							N:              n,
							Budget:         budget,
							PositionBucket: pos,
							RootRow:        rootRow,
							Seed:           seed,
							Hit:            ProjectionHit(text, tbl.RootName),
							ProjectedRows:  stats.RowsIncluded,
							ProjectRunes:   stats.InstanceRunes,
							WallMS:         time.Since(start).Milliseconds(),
						})
					}
				}
			}
		}
	}
	return results, nil
}

// renderArm 渲染一个方法臂：机械六方法走 summary 分发；simplestat 是 greedy + 简单统计量开关；
// random 均匀随机抽行（k 按预算/中位行长折算，无锚——消融口径：随机替换贪心选择本身）
// 各臂渲染共用同一表实例；返回文本与观测量（判分与 CSV 同源）
func renderArm(method string, tbl GeneratedTable, budget int, seed int64) (string, summary.RenderStats, error) {
	switch method {
	case "random":
		text, stats := renderRandomArm(tbl, budget, seed)
		return text, stats, nil
	case "simplestat":
		text, stats := summary.RenderWithStats("pods", tbl.Columns, tbl.Rows, false, summary.RenderOptions{
			Method:      summary.MethodGreedy,
			BudgetRunes: budget,
			SimpleStat:  true,
		})
		return text, stats, nil
	default:
		m, err := summary.ParseMethod(method)
		if err != nil {
			// 矩阵已校验方法臂，正常不可达；仍走错误返回不吞异常路径
			return "", summary.RenderStats{}, fmt.Errorf("bench method %q: %w", method, err)
		}
		text, stats := summary.RenderWithStats("pods", tbl.Columns, tbl.Rows, false, summary.RenderOptions{
			Method:      m,
			BudgetRunes: budget,
		})
		return text, stats, nil
	}
}

// renderRandomArm 随机臂渲染：与 greedy 同视觉框架（列频次段给全貌），子集换均匀随机行
// k = 预算 / 中位行长（与均匀采样基线同折算）；随机臂无头尾锚——消融对照「贪心选择」而非「锚约束」
func renderRandomArm(tbl GeneratedTable, budget int, seed int64) (string, summary.RenderStats) {
	rows := tbl.Rows
	k := 1
	if med := summary.MedianRowRunes(rows); med > 0 {
		k = budget / med
	}
	if k < 1 {
		k = 1
	}
	idxs := RandomPick(len(rows), k, seed)

	var b strings.Builder
	fmt.Fprintf(&b, "pods · %d 行 · 列: %s\n", len(rows), strings.Join(tbl.Columns, " "))
	b.WriteString("  （大表：均匀随机代表性投影——随机选择消融臂；全量在 raw）\n")
	hists := summary.ColumnHistograms(tbl.Columns, rows)
	for i, col := range tbl.Columns {
		if line := summary.RenderColumnFreq(col, hists[i]); line != "" {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	summary.WriteRows(&b, summary.RowsByIndex(rows, idxs))
	if omitted := len(rows) - len(idxs); omitted > 0 {
		fmt.Fprintf(&b, "  （随机采样 %d 行，已省略 %d 行，见 raw）\n", len(idxs), omitted)
	}
	runes := 0
	for _, i := range idxs {
		runes += summary.RowRunes(rows[i])
	}
	return strings.TrimRight(b.String(), "\n"), summary.RenderStats{RowsIncluded: len(idxs), InstanceRunes: runes}
}

// WriteBenchCSV 把结果写为 CSV（表头 + 每单元一行）；hit 列输出 0/1 便于直接聚合
func WriteBenchCSV(w io.Writer, results []BenchResult) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{
		"method", "N", "budget", "position_bucket", "root_row", "seed",
		"hit", "projected_rows", "project_runes", "wall_ms",
	}); err != nil {
		return err
	}
	for _, r := range results {
		hit := 0
		if r.Hit {
			hit = 1
		}
		if err := cw.Write([]string{
			r.Method,
			fmt.Sprintf("%d", r.N),
			fmt.Sprintf("%d", r.Budget),
			fmt.Sprintf("%d", r.PositionBucket),
			fmt.Sprintf("%d", r.RootRow),
			fmt.Sprintf("%d", r.Seed),
			fmt.Sprintf("%d", hit),
			fmt.Sprintf("%d", r.ProjectedRows),
			fmt.Sprintf("%d", r.ProjectRunes),
			fmt.Sprintf("%d", r.WallMS),
		}); err != nil {
			return err
		}
	}
	return cw.Error()
}
