// 判分子命令：把评测记录（--eval-json 产物）对照场景真值出分
//
// 两种模式：
//   默认——打分：读记录（单文件或目录下全部 *.json），①根因命中 + ②引用合法，输出 JSON
//   --sample N——抽样：从记录的 (结论 × 引用) 对里固定种子抽 N 行，输出 rubric markdown，
//   供③层人工或 LLM 辅助评（verdict 列留空待填）
//
// 本命令只做机械判分与抽样输出，不做语义判断；真值来自场景 manifest 的 ground_truth 段

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Aruing/Aruing/internal/eval"
)

// 解析 judge 子命令参数并执行判分
// --run-json 与 --scenario 必填：记录与真值缺一不可，空判不允许
func runJudge(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("judge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runJSON := fs.String("run-json", "", "evaluation record from `aruing run --eval-json` (file or directory of *.json)")
	scenario := fs.String("scenario", "", "scenario.yaml containing ground_truth")
	sample := fs.Int("sample", 0, "emit a rubric markdown sampling N (conclusion, evidence) pairs for layer-3 review instead of scoring")
	seed := fs.Int64("seed", 0, "random seed for --sample (fixed seed = reproducible sampling)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aruing judge --run-json <file|dir> --scenario <scenario.yaml> [--sample N --seed S]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Score eval records against scenario ground truth (layers 1-2), or")
		fmt.Fprintln(stderr, "sample a layer-3 review rubric with --sample.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*runJSON) == "" || strings.TrimSpace(*scenario) == "" {
		fs.Usage()
		return fmt.Errorf("judge requires --run-json and --scenario")
	}

	gt, err := eval.LoadGroundTruth(*scenario)
	if err != nil {
		return fmt.Errorf("load ground truth: %w", err)
	}

	records, err := loadEvalRecords(*runJSON)
	if err != nil {
		return fmt.Errorf("load run records: %w", err)
	}
	if len(records) == 0 {
		return fmt.Errorf("no eval records found at %s", *runJSON)
	}

	if *sample > 0 {
		// 抽样模式：逐记录抽样后合并渲染（单记录为主用例）
		var rows []eval.RubricRow
		for _, rec := range records {
			rows = append(rows, eval.SampleRubric(rec, *sample, *seed)...)
		}
		if len(rows) == 0 {
			return fmt.Errorf("no (conclusion, evidence) pairs to sample")
		}
		fmt.Fprint(stdout, eval.RenderRubricMarkdown(rows))
		return nil
	}

	results := make([]eval.JudgeResult, 0, len(records))
	for _, rec := range records {
		results = append(results, eval.JudgeRecord(rec, gt))
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		return fmt.Errorf("encode judge results: %w", err)
	}
	return nil
}

// loadEvalRecords 读单份记录文件或目录下全部 *.json 记录
// 目录按文件名排序保证输出顺序稳定
func loadEvalRecords(path string) ([]eval.RunRecord, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	var files []string
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			files = append(files, filepath.Join(path, e.Name()))
		}
	} else {
		files = []string{path}
	}
	records := make([]eval.RunRecord, 0, len(files))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var rec eval.RunRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		records = append(records, rec)
	}
	return records, nil
}
