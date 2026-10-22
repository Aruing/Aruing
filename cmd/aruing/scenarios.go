// 场景列表子命令：列出 kind 场景库及每个场景的难度理论分
//
// 纯读取命令：不校验大模型配置、不碰集群、不读 manifests，
// 只解析每个场景的 scenario.yaml（ground_truth 与 difficulty 段）
// 难度分由 eval.DeriveDifficulty 机械派生，本文件只负责展示；
// 不在这里解析 yaml 是脚本侧纪律的一部分（见 scripts/scenario-list.sh）
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/Aruing/Aruing/internal/eval"
)

// 解析场景列表子命令参数并输出场景与难度分。
// 未标注难度的场景在标准错误提示一次，不进入标准输出契约
func runScenarios(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("scenarios", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "scenarios", "directory holding kind scenarios")
	format := fs.String("format", "table", "output format: table|json")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aruing scenarios [flags]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "List kind scenarios with their difficulty theory scores (pure read).")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("scenarios takes no positional arguments")
	}
	switch *format {
	case "table", "json":
	default:
		return fmt.Errorf("unknown format %q: use table or json", *format)
	}

	entries, err := eval.LoadScenarioSpecs(*dir)
	if err != nil {
		return err
	}
	// 难度数据不可用的场景只提示到标准错误，避免污染机器可读的标准输出
	for _, e := range entries {
		if e.Err != nil {
			fmt.Fprintf(stderr, "warning: %s: %v\n", e.Name, e.Err)
		}
	}

	if *format == "json" {
		return writeScenariosJSON(stdout, entries)
	}
	writeScenariosTable(stdout, entries)
	return nil
}

// 难度数据是否可用于展示：读取失败或未标注难度都显示占位
func scenarioScored(e eval.ScenarioEntry) bool {
	return e.Err == nil && e.Spec.Difficulty != nil
}

// 表格视图：场景名 + 总分 + 六维分 + 信息缺口标记。
// 信息缺口不进总分，单独成列，标的是题型而非难度
func writeScenariosTable(w io.Writer, entries []eval.ScenarioEntry) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SCENARIO\tTOTAL\tFAULTS\tCHAIN\tDECOYS\tENV\tLAYERS\tROUNDS\tGAP")
	for _, e := range entries {
		if !scenarioScored(e) {
			fmt.Fprintf(tw, "%s\t-\t-\t-\t-\t-\t-\t-\t-\n", e.Name)
			continue
		}
		s := eval.DeriveDifficulty(*e.Spec.Difficulty)
		gap := "no"
		if e.Spec.Difficulty.InfoGap {
			gap = "yes"
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n",
			e.Name, s.Total, s.Faults, s.EvidenceChain, s.Decoys,
			s.Environment, s.Layers, s.SessionLength, gap)
	}
	_ = tw.Flush()
}

// json 视图的行形状：原子事实与派生分数并列，便于机器侧复核派生口径
type scenarioJSONRow struct {
	// 场景名（目录名）
	Name string `json:"name"`
	// 难度原子事实；不可用时为 null
	Difficulty *eval.Difficulty `json:"difficulty"`
	// 六维派生分数；不可用时为 null
	Score *eval.DifficultyScore `json:"score"`
	// 难度数据不可用的原因；可用时省略
	Error string `json:"error,omitempty"`
}

// json 视图：空场景目录也输出合法空数组
func writeScenariosJSON(w io.Writer, entries []eval.ScenarioEntry) error {
	rows := make([]scenarioJSONRow, 0, len(entries))
	for _, e := range entries {
		row := scenarioJSONRow{Name: e.Name}
		if e.Err != nil {
			row.Error = e.Err.Error()
		}
		if scenarioScored(e) {
			score := eval.DeriveDifficulty(*e.Spec.Difficulty)
			row.Difficulty = e.Spec.Difficulty
			row.Score = &score
		}
		rows = append(rows, row)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}
