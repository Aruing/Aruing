// 命令行入口：解析参数、组装依赖、输出诊断结果
//
// 配置为配置文件加环境变量覆盖；产品路径要求大模型配置齐全
// 依赖在装配文件中组装；本文件只负责命令行边界与输出
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"aruing/internal/config"
	"aruing/internal/core"
	"aruing/internal/session"
	"aruing/internal/tui"

	"golang.org/x/term"
)

// 构建期版本信息，经 -ldflags -X 注入（Makefile 与发布流水线同源注入）
// 源码直接 go run / go build 不经注入时保留默认值，仅表示开发自建
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// 顶层帮助文案覆盖当前支持的入口，保持和命令分发逻辑一致
const usage = `aruing is a Kubernetes diagnosis assistant.

Usage:
  aruing <command> [flags]

Commands:
  version          Print version information
  help             Print this help message
  run <question>   Run a one-shot diagnosis (requires LLM)
  chat [question]  Multi-turn chat via Session.Turn + Tower (requires LLM)

Configuration (file then env, env wins):
  --config PATH            config YAML (or ARUING_CONFIG)
  playground/config.yaml   default search from cwd
  $XDG_CONFIG_HOME/aruing/config.yaml
  /etc/aruing/config.yaml  (non-Windows)

LLM (required for run/chat):
  llm.base_url / ARUING_LLM_BASE_URL
  llm.api_key  / ARUING_LLM_API_KEY
  llm.model    / ARUING_LLM_MODEL

Examples:
  aruing version
  aruing run --config playground/config.yaml why is demo-api unreachable
  aruing chat hello
  aruing chat --session sess_xxx what about the redis dependency
`

// 进程入口只负责连接系统参数、标准输出和标准错误，实际逻辑放在可测试的运行函数中
func main() {
	if err := dispatch(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "aruing:", err)
		os.Exit(1)
	}
}

// 根据首个参数选择子命令，并把输出目标透传给具体处理函数
// 空参数默认打印帮助，避免用户误触发有副作用的诊断流程
func dispatch(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return nil
	}

	// 入口分发只识别稳定的顶层命令，未知命令直接返回包含帮助文案的错误
	switch args[0] {
	case "version":
		return runVersion(args[1:], stdout)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	case "run":
		return runRun(args[1:], stdout, stderr)
	case "chat":
		return runChat(args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
}

// 解析版本子命令的参数并输出当前版本信息
// 这里保留独立参数集，后续扩展版本命令时不会影响其他子命令
// 首行格式（aruing version <版本>）保持稳定，供脚本与后续自更新能力解析
func runVersion(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() { fmt.Fprint(stdout, usage) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "aruing version %s\n", version)
	fmt.Fprintf(stdout, "commit: %s\n", commit)
	fmt.Fprintf(stdout, "built:  %s\n", date)
	return nil
}

// 解析运行子命令的用户问题，执行诊断编排并按指定格式输出报告
// 所有非标志参数拼接为问题文本，因此用户不需要用引号包裹带空格的问题
// 默认输出标记文本报告；结构化格式输出结构化结果供机器消费
// 标准错误用于参数错误和局部帮助，标准输出只承载正常命令结果
func runRun(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "markdown", "output format: markdown|json")
	configPath := fs.String("config", "", "path to config YAML (or ARUING_CONFIG / search paths)")
	verbose := fs.Bool("verbose", false, "print orchestrator progress to stderr (same as ARUING_DEBUG=1)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aruing run [flags] <question>")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Run a diagnosis for the given question (requires LLM).")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	// 拼接所有非标志参数作为问题文本，支持带空格的自然语言提问
	question := strings.Join(fs.Args(), " ")
	if question == "" {
		return errors.New("run requires a question, e.g. aruing run why is demo-api unreachable")
	}
	switch *format {
	case "markdown", "json":
	default:
		return fmt.Errorf("unknown format %q: use markdown or json", *format)
	}

	cfg, usedPath, err := config.LoadResolved(*configPath)
	if err != nil {
		return formatRunError(err)
	}
	if *verbose {
		cfg.Debug = true
	}
	// 会话开始前打印生效配置来源、模型与 k8s 连接信息，便于确认覆盖结果与集群归属
	ci := resolveCluster(context.Background(), cfg.Tools, defaultKubectlContext)
	writeStartupBanner(stderr, usedPath, cfg, ci)

	factory := core.NewFactory()
	runID, err := factory.NewID("run")
	if err != nil {
		return fmt.Errorf("create run ID: %w", err)
	}
	now := factory.Now()
	run := core.Run{
		ID:        runID,
		Question:  question,
		Status:    core.RunStatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}

	orchestrator, err := newOrchestrator(factory, cfg, stderr)
	if err != nil {
		return formatRunError(fmt.Errorf("build orchestrator: %w", err))
	}
	outcome, err := orchestrator.Execute(context.Background(), run)
	if err != nil {
		return formatRunError(fmt.Errorf("execute diagnosis: %w", err))
	}
	if outcome.Suspension != nil {
		// 单次 run 无会话环：澄清挂起时把问题打印到标准错误并退出非零
		fmt.Fprintf(stderr, "需要澄清才能继续（run %s）：\n%s\n", outcome.Suspension.RunID, outcome.Suspension.Question)
		if len(outcome.Suspension.Options) > 0 {
			for _, opt := range outcome.Suspension.Options {
				fmt.Fprintf(stderr, "  - %s\n", opt)
			}
		}
		fmt.Fprintln(stderr, "提示：在 chat 会话中回复可恢复；单次 run 无交互环。")
		return fmt.Errorf("diagnosis suspended waiting for user clarification")
	}
	if outcome.Report == nil {
		return formatRunError(fmt.Errorf("execute diagnosis: empty outcome"))
	}
	return writeReport(stdout, *format, *outcome.Report, outcome.Evidence)
}

// 多轮入口：会话轮次加基线塔；无位置参数进入交互式 TUI
// 必须配置大模型；进度与会话编号写标准错误，助手内容与诊断报告写标准输出
func runChat(args []string, stdout, stderr io.Writer) error {
	return runChatWith(args, stdout, stderr)
}

// 与多轮入口相同；单句模式直接 Turn，交互模式进入 TUI
func runChatWith(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sessionIDFlag := fs.String("session", "", "existing session id (omit to create a new session)")
	format := fs.String("format", "markdown", "diagnostic report format: markdown|json (baseline is always plain text)")
	configPath := fs.String("config", "", "path to config YAML (or ARUING_CONFIG / search paths)")
	verbose := fs.Bool("verbose", false, "print Tower debug progress to stderr (same as ARUING_DEBUG=1)")
	uiMode := fs.String("ui", "", "interactive chat UI: inline|app (overrides tui.mode config; default inline)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aruing chat [flags] [question]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Multi-turn chat via Session.Turn and Tower (requires LLM).")
		fmt.Fprintln(stderr, "With a question: one Turn then exit. Without: read lines from stdin until exit/quit/EOF.")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	question := strings.Join(fs.Args(), " ")
	formatVal := *format
	switch formatVal {
	case "markdown", "json":
	default:
		return fmt.Errorf("unknown format %q: use markdown or json", formatVal)
	}
	// 交互模式值校验提前失败（覆盖 cfg 在加载后做）
	if m := strings.TrimSpace(*uiMode); m != "" && m != "inline" && m != "app" {
		return fmt.Errorf("unknown ui mode %q: use inline or app", m)
	}

	cfg, usedPath, err := config.LoadResolved(*configPath)
	if err != nil {
		return formatRunError(err)
	}
	if *verbose {
		cfg.Debug = true
	}
	// --ui 非空时覆盖配置的模式（与 --verbose 覆盖 Debug 同模式）；仅影响交互模式
	if m := strings.TrimSpace(*uiMode); m != "" {
		cfg.TUI.Mode = m
	}
	// 会话开始前打印生效配置来源、模型与 k8s 连接信息，便于确认覆盖结果与集群归属
	ci := resolveCluster(context.Background(), cfg.Tools, defaultKubectlContext)
	writeStartupBanner(stderr, usedPath, cfg, ci)

	factory := core.NewFactory()
	// 进度协调器：编排/Tower 的 progress 行先落到这里；inline 模式绑定终端后
	// 与 spinner 同屏重绘，其余模式（单句 / stdin 行 / app）透传 stderr 维持现状
	progress := tui.NewTurnProgress(stderr)
	svc, err := newSessionStack(factory, cfg, progress)
	if err != nil {
		return formatRunError(err)
	}

	ctx := context.Background()
	sessionID := strings.TrimSpace(*sessionIDFlag)
	if sessionID == "" {
		sess, err := svc.NewSession(ctx)
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		sessionID = sess.ID
		fmt.Fprintf(stderr, "session: %s\n", sessionID)
	}

	// 单句模式：一次会话后退出。无交互界面，TUI 主题/样式不参与（纯文本输出），
	// 加提示避免「配置了主题没生效」的误解
	if question != "" {
		if cfg.TUI.ThemeFile != "" {
			fmt.Fprintln(stderr, "config: note: 单句模式无界面，tui.theme_file 不参与渲染（交互模式生效）")
		}
		return chatTurn(ctx, svc, sessionID, question, formatVal, stdout)
	}

	// 主题文件相对路径以 config 文件所在目录为基准（配置引用跟着配置走，与 cwd 无关）
	resolveThemeFilePath(&cfg, usedPath)

	// 非 tty stdin：逐行读取直到 exit/quit/EOF（usage 承诺的行为；供脚本/smoke 驱动多轮）
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return chatStdinLoop(ctx, svc, sessionID, formatVal, stdout, os.Stdin)
	}

	// 交互模式：bubbletea TUI 接管终端（Step 2）；单句模式已在上方返回
	return tui.Run(ctx, svc, sessionID, formatVal, stdout, cfg.TUI, progress)
}

// 非 tty stdin 行模式：每行一 Turn，同会话；空行忽略，exit/quit 停止。
// 任一 Turn 失败即返回（严格失败，便于脚本感知）。
func chatStdinLoop(ctx context.Context, svc *session.Service, sessionID, format string, stdout io.Writer, in io.Reader) error {
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			return nil
		}
		if err := chatTurn(ctx, svc, sessionID, line, format, stdout); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// 执行一轮会话并按约定写标准输出
func chatTurn(ctx context.Context, svc *session.Service, sessionID, userText, format string, stdout io.Writer) error {
	result, err := svc.Turn(ctx, sessionID, userText)
	if err != nil {
		return formatRunError(fmt.Errorf("turn: %w", err))
	}
	return writeTurnResult(stdout, format, result)
}

// 基线模式只写助手正文；诊断模式在正文后可选分隔线与报告（证据本步不透传）
func writeTurnResult(stdout io.Writer, format string, result session.TurnResult) error {
	content := result.AssistantMessage.Content
	if content != "" {
		if _, err := fmt.Fprintln(stdout, content); err != nil {
			return fmt.Errorf("write content: %w", err)
		}
	}

	if result.Report == nil {
		return nil
	}
	// 仅诊断模式附加报告块；无模式标记但已有报告时仍打印（防御）
	if result.AssistantMessage.Mode != "" && result.AssistantMessage.Mode != session.ModeDiagnostic {
		return nil
	}
	if content != "" {
		if _, err := fmt.Fprintln(stdout, "---"); err != nil {
			return fmt.Errorf("write separator: %w", err)
		}
	}
	// 本步回合结果无证据切片，明细表以运行子命令为准
	return writeReport(stdout, format, *result.Report, nil)
}

// 按指定格式把报告写入标准输出
func writeReport(stdout io.Writer, format string, report core.Report, evidence []core.Evidence) error {
	switch format {
	case "markdown":
		if _, err := io.WriteString(stdout, renderMarkdown(report, evidence)); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		return nil
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown format %q: use markdown or json", format)
	}
}

// theme_file 相对路径基准：config 文件所在目录（绝对路径不动；configPath 空则不动）
func resolveThemeFilePath(cfg *config.Config, configPath string) {
	if cfg == nil || cfg.TUI.ThemeFile == "" || configPath == "" {
		return
	}
	if filepath.IsAbs(cfg.TUI.ThemeFile) {
		return
	}
	cfg.TUI.ThemeFile = filepath.Join(filepath.Dir(configPath), cfg.TUI.ThemeFile)
}
