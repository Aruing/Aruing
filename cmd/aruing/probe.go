// probe 子命令：长会话探针实验装置（0.1.3 步骤 4）
//
// 单进程驱动一条脚本化长会话（普通问答 + 穿插正式诊断 + 尾部探针），
// 逐诊断内嵌 RunRecord、逐探针落记忆观测量与展开后期望，落盘会话级评测记录
// 记忆方法读既有 agent.memory.method（实验臂经 ARUING_AGENT_MEMORY_METHOD 注入，
// 与 eval-sweep 同口径）；轮数与种子走本命令 flag，不进 config
//
// --dry-run 只解析规格、生成并打印轮次计划（零 LLM、零集群）；
// 真跑须 LLM 齐全 + kind 场景集群就绪（穿插诊断走真工具链）
// 真跑数统一推迟实验批（2026-08-30 裁决）；授权型 smoke 路径见 plan 步骤 4

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Aruing/Aruing/internal/agent"
	"github.com/Aruing/Aruing/internal/config"
	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/eval"
	"github.com/Aruing/Aruing/internal/llm"
	"github.com/Aruing/Aruing/internal/session"
)

// runProbe 解析 probe 子命令参数并驱动一条长会话
// --scenario 指向场景的 scenario.yaml（探针规格取同目录 probe.yaml，真值取其
// ground_truth 段——kth_run_pods 展开的资源名过滤键）
func runProbe(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to config YAML (or ARUING_CONFIG / search paths)")
	scenario := fs.String("scenario", "", "scenario.yaml of the target scenario (probe.yaml is read from the same directory)")
	rounds := fs.Int("rounds", 20, "scripted session rounds before the probe tail (>= 1)")
	seed := fs.Int64("seed", 1, "script generation seed (fixed seed = reproducible session script)")
	out := fs.String("out", "", "output directory for the probe session record (default eval/results/0.1.3)")
	dryRun := fs.Bool("dry-run", false, "parse spec, generate and print the turn plan, then exit (no LLM, no cluster)")
	verbose := fs.Bool("verbose", false, "print orchestrator and tower progress to stderr (same as ARUING_DEBUG=1)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aruing probe --scenario <scenario.yaml> [--rounds N --seed S] [--out DIR] [--dry-run]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Drive one scripted long session (Q&A + interspersed diagnoses + tail probes)")
		fmt.Fprintln(stderr, "and write a probe session record (requires LLM; --dry-run does not).")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateProbeFlags(*scenario, *rounds); err != nil {
		return err
	}

	// 真值与规格：真值资源名是 from_ledger 展开的过滤键，两者都不可缺
	gt, err := eval.LoadGroundTruth(*scenario)
	if err != nil {
		return fmt.Errorf("load ground truth: %w", err)
	}
	specPath := filepath.Join(filepath.Dir(*scenario), "probe.yaml")
	spec, err := eval.LoadProbeSpec(specPath)
	if err != nil {
		return fmt.Errorf("load probe spec: %w", err)
	}
	script, err := eval.GenerateProbeScript(spec, *rounds, *seed)
	if err != nil {
		return fmt.Errorf("generate probe script: %w", err)
	}

	if *dryRun {
		printProbePlan(stdout, script)
		return nil
	}

	cfg, usedPath, err := config.LoadResolved(*configPath)
	if err != nil {
		return formatRunError(err)
	}
	if *verbose {
		cfg.Debug = true
	}
	ci := resolveCluster(context.Background(), cfg.Tools, defaultKubectlContext)
	writeStartupBanner(stderr, usedPath, cfg, ci)

	factory := core.NewFactory()
	stack, err := newSessionStackFull(factory, cfg, stderr)
	if err != nil {
		return formatRunError(fmt.Errorf("build session stack: %w", err))
	}
	sess, err := stack.service.NewSession(context.Background())
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	rec, runErr := eval.RunProbeSession(context.Background(), probeDepsOf(stack), eval.ProbeRunOptions{
		SessionID:        sess.ID,
		Model:            cfg.LLM.Model,
		MemoryMethod:     normalizeMemoryMethod(cfg.Agent.Memory.Method),
		ProjectionMethod: cfg.Tools.Projection.Method,
		Acquire: eval.AcquireRecordInfo{
			Method:    normalizeAcquireMethod(cfg),
			MaxRounds: cfg.Agent.Acquire.MaxRounds,
			Seed:      cfg.Agent.Acquire.Seed,
		},
		ResourceName: gt.ResourceName,
	}, spec, script)

	// 失败也落盘（失败 run 全量报告是统计纪律；RunProbeSession 已含部分轮产物）
	outDir := *out
	if outDir == "" {
		outDir = filepath.Join("eval", "results", "0.1.3")
	}
	recPath := filepath.Join(outDir, fmt.Sprintf(
		"probe-session-%s-%s-n%d-s%d.json", spec.Name, rec.MemoryMethod, *rounds, *seed))
	if werr := writeProbeRecord(recPath, rec); werr != nil {
		// 会话记录是本命令的唯一主产物：写失败必须非零退出，
		// 否则跑批把丢记录的单元误计为成功（与 run 路径旁路落盘语义不同）
		return fmt.Errorf("write probe record %s: %w", recPath, werr)
	}
	if runErr != nil {
		fmt.Fprintf(stderr, "长会话中途失败（部分记录已落盘 %s）：%v\n", recPath, runErr)
		return runErr
	}
	fmt.Fprintf(stderr, "probe: %d turns (%d diagnoses), %d probes, record: %s\n",
		rec.TurnsExecuted, len(rec.Diagnoses), len(rec.Probes), recPath)
	fmt.Fprintf(stdout, "%s\n", recPath)
	return nil
}

// validateProbeFlags 启动期拦截缺参与非法轮数
func validateProbeFlags(scenario string, rounds int) error {
	if scenario == "" {
		return fmt.Errorf("probe requires --scenario <scenario.yaml>")
	}
	if rounds < 1 {
		return fmt.Errorf("rounds %d: want >= 1", rounds)
	}
	return nil
}

// printProbePlan 干跑输出：逐轮计划 + 汇总（零 LLM 零集群，核对装置用）
func printProbePlan(w io.Writer, script eval.ProbeScript) {
	for i, t := range script.Turns {
		fmt.Fprintf(w, "%3d %-9s %s\n", i, t.Kind, previewRunes(t.Text, 72))
	}
	diag := 0
	probe := 0
	for _, t := range script.Turns {
		switch t.Kind {
		case eval.ProbeTurnDiagnose:
			diag++
		case eval.ProbeTurnProbe:
			probe++
		}
	}
	fmt.Fprintf(w, "plan: %d rounds, %d diagnose turns, %d probes (seed=%d)\n",
		script.Rounds, diag, probe, script.Seed)
}

// previewRunes 截断到指定字符数，干跑计划单行可读
func previewRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// probeDepsOf 把会话栈全量句柄转成驱动依赖（观测量在此侧做工程投影转换）
func probeDepsOf(stack *sessionStack) eval.ProbeDeps {
	var tokens func() map[string]llm.UsageTotals
	if stack.tracker != nil {
		tokens = func() map[string]llm.UsageTotals {
			return stack.tracker.UsageSnapshot()
		}
	}
	return eval.ProbeDeps{
		Turn: func(ctx context.Context, sessionID, userText string) (session.TurnResult, error) {
			return stack.service.Turn(ctx, sessionID, userText)
		},
		MemStats: func() eval.MemoryStats {
			s := stack.tower.LastMemoryStats()
			return eval.MemoryStats{
				Method:             s.Method,
				LocateLayer:        s.LocateLayer,
				Lambda2Called:      s.Lambda2Called,
				RehydratedMsgs:     s.RehydratedMsgs,
				RehydratedEvidence: s.RehydratedEvidence,
				HistTurns:          s.HistTurns,
			}
		},
		DiagnoseStats: func() eval.DiagnoseStats {
			s := stack.orch.LastRunStats()
			return eval.DiagnoseStats{
				Rounds: s.InvestigateRounds,
				Exit:   s.AcquireExit,
				Gap:    s.AcquireGap,
				Trace:  convertDecisionTrace(s.DecisionTrace),
			}
		},
		Ledger: stack.ledger,
		Tokens: tokens,
	}
}

// normalizeMemoryMethod 记忆方法规范名入记录（空串与 ours 归一；解析失败不会
// 到这里——newSessionStackFull 启动期已校验过同一配置）
func normalizeMemoryMethod(raw string) string {
	if m, err := agent.ParseMemoryMethod(raw); err == nil {
		return string(m)
	}
	return raw
}

// normalizeAcquireMethod 取证决策方法规范名（口径同 run --eval-json 路径）
func normalizeAcquireMethod(cfg config.Config) string {
	method := cfg.Agent.Acquire.Method
	if m, err := agent.ParseAcquireMethod(method); err == nil {
		return m.String()
	}
	return method
}

// writeProbeRecord 会话级记录落盘；父目录自动创建
// 目标已存在时明确报错不覆盖：同参数重跑须换 seed 或 out，静默覆盖会丢旧记录
// （记录是本命令唯一主产物；sweep 脚本按矩阵坐标改名、种子=重复号，天然不冲突）
func writeProbeRecord(path string, rec eval.ProbeSessionRecord) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("probe record already exists: %s (rerun with a different seed or --out)", path)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create probe out dir: %w", err)
		}
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal probe record: %w", err)
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}
