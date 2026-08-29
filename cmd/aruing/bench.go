// bench 子命令：机械判分主实验遍历（场景生成器 × 方法 × 预算 × 位置 × 种子）
//
// 读矩阵（--matrix，省略用内置默认全量矩阵）→ 逐单元生成大表 → 按方法臂渲染 →
// eval.ProjectionHit 判分 → CSV 落盘；旁落一份矩阵快照（实验数据自文档化）
// 零 LLM、零集群：投影与判分都是纯函数；LLM 路径（C4 / 下游诊断）走 run --eval-json + judge

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Aruing/Aruing/internal/eval"

	"gopkg.in/yaml.v3"
)

// runBench 解析 bench 子命令参数并执行遍历
// --out 为 CSV 输出路径（默认 bench/results/projection-bench.csv，父目录自动创建）
func runBench(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	matrix := fs.String("matrix", "", "path to bench matrix YAML (omit = built-in default full matrix)")
	out := fs.String("out", filepath.Join("bench", "results", "projection-bench.csv"), "output CSV path")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aruing bench [--matrix <yaml>] [--out <csv>]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Run the mechanical projection benchmark: table generator x method x budget x")
		fmt.Fprintln(stderr, "root-cause position x seed, judged by eval.ProjectionHit (no LLM, no cluster).")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Methods: fast, greedy, greedy-knapsack, full, head-tail, uniform, random, simplestat.")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	var m eval.BenchMatrix
	if path := *matrix; path != "" {
		loaded, err := eval.LoadBenchMatrix(path)
		if err != nil {
			return fmt.Errorf("load bench matrix: %w", err)
		}
		m = loaded
	} else {
		m = eval.DefaultBenchMatrix()
		if err := m.Validate(); err != nil {
			return fmt.Errorf("default bench matrix: %w", err)
		}
	}

	results, err := eval.RunBench(m)
	if err != nil {
		return fmt.Errorf("run bench: %w", err)
	}

	if dir := filepath.Dir(*out); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create bench out dir: %w", err)
		}
	}
	if err := writeBenchCSV(*out, results); err != nil {
		return fmt.Errorf("write bench csv: %w", err)
	}
	// 矩阵快照与 CSV 同目录同名落盘（-matrix 后缀）：实验数据自带跑数配置，可复现
	snapPath := snapPathFor(*out)
	if err := writeMatrixSnapshot(snapPath, m); err != nil {
		return fmt.Errorf("write matrix snapshot: %w", err)
	}

	hits := 0
	for _, r := range results {
		if r.Hit {
			hits++
		}
	}
	fmt.Fprintf(stderr, "bench: %d units, %d hits (%.1f%%)\n", len(results), hits, 100*float64(hits)/float64(len(results)))
	fmt.Fprintf(stderr, "csv: %s\nmatrix: %s\n", *out, snapPath)
	fmt.Fprintf(stdout, "%s\n", *out)
	return nil
}

// writeBenchCSV 落盘 CSV（表头 + 每单元一行）
func writeBenchCSV(path string, results []eval.BenchResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return eval.WriteBenchCSV(f, results)
}

// snapPathFor 矩阵快照路径：去掉 CSV 扩展名加 -matrix.yaml
func snapPathFor(csvPath string) string {
	ext := filepath.Ext(csvPath)
	return csvPath[:len(csvPath)-len(ext)] + "-matrix.yaml"
}

// writeMatrixSnapshot 把生效矩阵写成 YAML 快照
func writeMatrixSnapshot(path string, m eval.BenchMatrix) error {
	raw, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}
