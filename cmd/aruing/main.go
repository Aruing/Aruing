// 命令行程序是项目当前的入口
//
// 这里只做命令解析和依赖组装：
// - run：直连 Orchestrator.Execute（单轮诊断）
// - chat：Session.Turn + Tower（多轮基线，需要根因时升格诊断）
// 诊断推理、工具调用、存储和报告生成都放在内部模块中
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
	"strings"

	"aruing/internal/config"
	"aruing/internal/core"
	"aruing/internal/session"
)

// 对外展示的版本号，发布时应作为命令行输出的唯一来源
const version = "0.1.0"

// 顶层帮助文案覆盖当前支持的入口，保持和命令分发逻辑一致
const usage = `aruing is a Kubernetes diagnosis assistant.

Usage:
  aruing <command> [flags]

Commands:
  version          Print version information
  help             Print this help message
  run <question>   Run a one-shot diagnosis for the given question
  chat [question]  Multi-turn chat via Session.Turn + Tower (requires LLM)

Examples:
  aruing version
  aruing run why is demo-api unreachable in default namespace
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
func runVersion(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() { fmt.Fprint(stdout, usage) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "aruing version %s\n", version)
	return nil
}

// 解析运行子命令的用户问题，执行诊断编排并按指定格式输出报告
// 所有非标志参数拼接为问题文本，因此用户不需要用引号包裹带空格的问题
// 默认输出 Markdown 报告；--format json 输出结构化 JSON 供机器消费
// 标准错误用于参数错误和局部帮助，标准输出只承载正常命令结果
func runRun(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "markdown", "output format: markdown|json")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aruing run [flags] <question>")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Run a diagnosis for the given question.")
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

	// 配置由 internal/config 统一从 env 读取；LLM 三件套不全时 newOrchestrator 走全 fake
	cfg := config.Load()
	orchestrator, err := newOrchestrator(factory, cfg, stderr)
	if err != nil {
		return formatRunError(fmt.Errorf("build orchestrator: %w", err))
	}
	report, evidence, err := orchestrator.Execute(context.Background(), run)
	if err != nil {
		return formatRunError(fmt.Errorf("execute diagnosis: %w", err))
	}

	return writeReport(stdout, *format, report, evidence)
}

// 多轮入口：Session.Turn + Tower；无位置参数则从 stdin 交互读行
// 必须配置 LLM；进度与 session id 写 stderr，助手内容与诊断报告写 stdout
func runChat(args []string, stdout, stderr io.Writer) error {
	return runChatWith(args, stdout, stderr, os.Stdin)
}

// runChatWith 与 runChat 相同，stdin 可注入便于测试
func runChatWith(args []string, stdout, stderr io.Writer, stdin io.Reader) error {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(stderr)
	sessionIDFlag := fs.String("session", "", "existing session id (omit to create a new session)")
	format := fs.String("format", "markdown", "diagnostic report format: markdown|json (baseline is always plain text)")
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

	cfg := config.Load()
	factory := core.NewFactory()
	svc, err := newSessionStack(factory, cfg, stderr)
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

	// 单句模式：一次 Turn 后退出
	if question != "" {
		return chatTurn(ctx, svc, sessionID, question, formatVal, stdout)
	}

	// 交互：空行跳过；exit / quit / EOF 结束
	scanner := bufio.NewScanner(stdin)
	// 允许较长输入行（默认 64K 通常够用；略放大防极端粘贴）
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read input: %w", err)
			}
			return nil
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			return nil
		}
		if err := chatTurn(ctx, svc, sessionID, line, formatVal, stdout); err != nil {
			return err
		}
	}
}

// 执行一轮 Turn 并按约定写 stdout
func chatTurn(ctx context.Context, svc *session.Service, sessionID, userText, format string, stdout io.Writer) error {
	result, err := svc.Turn(ctx, sessionID, userText)
	if err != nil {
		return formatRunError(fmt.Errorf("turn: %w", err))
	}
	return writeTurnResult(stdout, format, result)
}

// baseline：只写 Content；diagnostic：Content 后可选分隔线 + 报告（evidence 切片本步不透传）
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
	// 仅 diagnostic 附加报告块；无 Mode 时若有 Report 仍打印（防御）
	if result.AssistantMessage.Mode != "" && result.AssistantMessage.Mode != session.ModeDiagnostic {
		return nil
	}
	if content != "" {
		if _, err := fmt.Fprintln(stdout, "---"); err != nil {
			return fmt.Errorf("write separator: %w", err)
		}
	}
	// 本步 TurnResult 无 evidence 切片，明细表以 aruing run 为准
	return writeReport(stdout, format, *result.Report, nil)
}

// 按指定格式把报告写入 stdout
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
