// 判分子命令：把评测记录（--eval-json 产物）对照场景真值出分
//
// 四种模式：
//   默认——打分：读记录（单文件或目录下全部 *.json），①根因命中 + ②引用合法，输出 JSON
//   --sample N——逐记录抽样：每份记录抽 N 行，输出 rubric markdown，供③层人工阅读作业
//   --sample-total N——全池化抽样：跨记录合并抽 N 行，输出 rubric JSON（机器可回填；配
//   --rubric-llm 时由同模型逐行打分回填，裁决 #6 的 LLM 辅助评）
//   --agree A.json B.json——两组已回填 rubric 的逐行一致率（error 行不计入分母）
//
// 本命令的机械判分与抽样输出不做语义判断；真值来自场景 manifest 的 ground_truth 段；
// --rubric-llm 的语义判断在 LLM，评分口径三值由 eval 侧统一收口

package main

import (
	"context"
	_ "embed" // prompts 经 go:embed 内嵌（#9：prompt 不写死代码）
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Aruing/Aruing/internal/config"
	"github.com/Aruing/Aruing/internal/eval"
	"github.com/Aruing/Aruing/internal/llm"
)

//go:embed prompts/rubric-judge.md
var rubricJudgePrompt string

// 解析 judge 子命令参数并执行判分
// --run-json 与 --scenario 必填：记录与真值缺一不可，空判不允许
func runJudge(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("judge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	runJSON := fs.String("run-json", "", "evaluation record from `aruing run --eval-json` (file or directory of *.json)")
	scenario := fs.String("scenario", "", "scenario.yaml containing ground_truth")
	sample := fs.Int("sample", 0, "emit a rubric markdown sampling N (conclusion, evidence) pairs per record for layer-3 review instead of scoring")
	sampleTotal := fs.Int("sample-total", 0, "emit a rubric JSON sampling N pairs pooled across all records (layer-3, machine-backfillable)")
	rubricLLM := fs.Bool("rubric-llm", false, "with --sample-total: score each row via LLM (same model as experiments) and fill verdicts")
	agree := fs.Bool("agree", false, "compare two backfilled rubric JSON files passed as positional args and emit the agreement rate")
	cfgPath := fs.String("config", "", "config YAML for --rubric-llm (LLM credentials; default search path applies)")
	seed := fs.Int64("seed", 0, "random seed for --sample / --sample-total (fixed seed = reproducible sampling)")
	probe := fs.Bool("probe", false, "score probe session records (aruing probe output) instead of run records")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aruing judge [--probe] --run-json <file|dir> --scenario <scenario.yaml> [--sample N --seed S]")
		fmt.Fprintln(stderr, "       aruing judge --run-json <dir> --scenario <yaml> --sample-total N [--rubric-llm --config C] [--seed S]")
		fmt.Fprintln(stderr, "       aruing judge --run-json <dir> --scenario <yaml> --agree A.json B.json")
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

	gt, gtErr := eval.LoadGroundTruth(*scenario)
	if gtErr != nil {
		return fmt.Errorf("load ground truth: %w", gtErr)
	}

	// probe 模式：判分会话级探针记录（探针①层 + 内嵌诊断复判），与 run 记录同 flag 入口
	if *probe {
		probeRecs, perr := loadProbeRecords(*runJSON)
		if perr != nil {
			return fmt.Errorf("load probe records: %w", perr)
		}
		if len(probeRecs) == 0 {
			return fmt.Errorf("no probe session records found at %s", *runJSON)
		}
		results := make([]eval.ProbeJudgeResult, 0, len(probeRecs))
		for _, rec := range probeRecs {
			results = append(results, eval.JudgeProbeSession(rec, gt))
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			return fmt.Errorf("encode probe judge results: %w", err)
		}
		return nil
	}

	records, err := loadEvalRecords(*runJSON)
	if err != nil {
		return fmt.Errorf("load run records: %w", err)
	}
	if len(records) == 0 {
		return fmt.Errorf("no eval records found at %s", *runJSON)
	}

	if *sample > 0 {
		// 逐记录抽样：单记录为主用例，人读 markdown 作业面
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

	// 全池化抽样：跨记录合并抽 N 行输出机器可回填的 rubric JSON（裁决 #6 抽样口径）
	if *sampleTotal > 0 {
		rows := eval.SampleRubricTotal(records, *sampleTotal, *seed)
		if len(rows) == 0 {
			return fmt.Errorf("no (conclusion, evidence) pairs to sample")
		}
		if *rubricLLM {
			cfg, _, cfgErr := config.LoadResolved(*cfgPath)
			if cfgErr != nil {
				return fmt.Errorf("load config for --rubric-llm: %w", cfgErr)
			}
			if err := config.ValidateLLM(cfg); err != nil {
				return fmt.Errorf("--rubric-llm: %w", err)
			}
			client, clientErr := llm.NewClient(cfg.LLM.ToClientConfig())
			if clientErr != nil {
				return fmt.Errorf("build llm client: %w", clientErr)
			}
			ctx := context.Background()
			for i := range rows {
				rows[i].Verdict = scoreRubricRow(ctx, client, rows[i])
			}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			return fmt.Errorf("encode rubric rows: %w", err)
		}
		return nil
	}

	// --agree：两组已回填 rubric 的逐行一致率；不消费记录内容，
	// 但 --run-json/--scenario 必填校验维持现状，传任一合法路径即可
	if *agree {
		files := fs.Args()
		if len(files) != 2 {
			return fmt.Errorf("--agree expects exactly two positional rubric JSON files (got %d)", len(files))
		}
		a, err := loadRubricJSON(files[0])
		if err != nil {
			return fmt.Errorf("load %s: %w", files[0], err)
		}
		b, err := loadRubricJSON(files[1])
		if err != nil {
			return fmt.Errorf("load %s: %w", files[1], err)
		}
		agreeN, total, err := eval.Agreement(a, b)
		if err != nil {
			return fmt.Errorf("agreement: %w", err)
		}
		rate := 0.0
		if total > 0 {
			rate = float64(agreeN) / float64(total)
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"agree": agreeN, "total": total, "rate": rate}); err != nil {
			return fmt.Errorf("encode agreement: %w", err)
		}
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

// 列出单份 JSON 文件或目录下全部 *.json（按文件名排序，输出顺序稳定）
func listJSONFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		files = append(files, filepath.Join(path, e.Name()))
	}
	return files, nil
}

// loadEvalRecords 读单份记录文件或目录下全部 *.json 记录
func loadEvalRecords(path string) ([]eval.RunRecord, error) {
	files, err := listJSONFiles(path)
	if err != nil {
		return nil, err
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

// loadProbeRecords 读单份探针会话记录或目录下全部 *.json（judge --probe 入口）
func loadProbeRecords(path string) ([]eval.ProbeSessionRecord, error) {
	files, err := listJSONFiles(path)
	if err != nil {
		return nil, err
	}
	records := make([]eval.ProbeSessionRecord, 0, len(files))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var rec eval.ProbeSessionRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, fmt.Errorf("parse %s: %w", f, err)
		}
		records = append(records, rec)
	}
	return records, nil
}

// rubricVerdictOutput LLM 辅助评的输出契约：单字段三值 verdict
type rubricVerdictOutput struct {
	Verdict string `json:"verdict"`
}

// scoreRubricRow 单行 LLM 辅助评：结论理由 + 证据摘要进 prompt（与人工作业面同材料），
// 出三值 verdict；非法输出（调用失败 / 解析失败 / 非三值）重试 2 次后记 error，
// 不中断整表（#18：单行失败不废批，抽样表其余行仍可用）
func scoreRubricRow(ctx context.Context, client llm.Client, row eval.RubricRow) string {
	user := fmt.Sprintf("结论理由：%s\n\n证据摘要：%s", row.Reason, row.Summary)
	for attempt := 0; attempt < 3; attempt++ {
		var out rubricVerdictOutput
		if err := client.GenerateJSON(ctx, llm.Request{System: rubricJudgePrompt, User: user}, &out); err == nil {
			if v, verr := eval.NormalizeVerdict(out.Verdict); verr == nil {
				return v
			}
		}
	}
	return eval.VerdictError
}

// loadRubricJSON 读一份已回填的 rubric JSON（--sample-total 产物或其人工回填副本）
func loadRubricJSON(path string) ([]eval.RubricRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []eval.RubricRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("parse rubric rows: %w", err)
	}
	return rows, nil
}
