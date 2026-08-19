// aruing connect 交互式配置向导：引导填写大模型三件套，连通测试后写入用户级配置文件
//
// 设计边界：只管 llm 段（base_url / api_key / model），kubectl 等由既有路径探测；
// 已有配置文件时确认后仅覆盖 llm 段、保留其余段（config.SaveLLM 承担合并）；
// 远期多渠道演进（channels + 优先级降级）时升级文件 schema，本命令仍是唯一配置入口
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Aruing/Aruing/internal/config"
	"github.com/Aruing/Aruing/internal/llm"

	"golang.org/x/term"
)

// 连通测试的超时；网关慢时给足首次建连 + 一次最小补全的余量
const connectTestTimeout = 20 * time.Second

// 解析 connect 子命令参数并执行配置向导
//
// 三件套参数全给时进入非交互模式（脚本/CI 可编程）；否则逐项提问
// --no-test 跳过连通测试（本地网关可能不支持最小补全探测）
// --config 显式指定写入路径；默认写入用户级配置（config.UserConfigPath）
func runConnect(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	baseURL := fs.String("base-url", "", "LLM base URL (non-interactive mode)")
	apiKey := fs.String("api-key", "", "LLM API key (non-interactive mode)")
	model := fs.String("model", "", "LLM model name (non-interactive mode)")
	noTest := fs.Bool("no-test", false, "skip connectivity test")
	configPath := fs.String("config", "", "write to this config path instead of the user config")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aruing connect [flags]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Interactive wizard that writes LLM config (base_url / api_key / model).")
		fmt.Fprintln(stderr, "Pass all three flags to run non-interactively.")
		fmt.Fprintln(stderr, "")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 写入目标：显式路径优先，否则用户级配置；解析失败给出替代指引
	target := strings.TrimSpace(*configPath)
	if target == "" {
		p, err := config.UserConfigPath()
		if err != nil {
			return fmt.Errorf("cannot resolve user config dir: %w (use --config PATH instead)", err)
		}
		target = p
	}

	llmCfg := config.LLM{
		BaseURL: strings.TrimSpace(*baseURL),
		APIKey:  strings.TrimSpace(*apiKey),
		Model:   strings.TrimSpace(*model),
	}

	interactive := llmCfg.BaseURL == "" || llmCfg.APIKey == "" || llmCfg.Model == ""
	// 向导全程共用一个 bufio reader：bufio 预读缓冲会吃掉后续行，
	// 多处各自建 reader 会互抢输入流（提问与确认在同一段输入里）
	reader := bufio.NewReader(stdin)
	if interactive {
		var err error
		llmCfg, err = promptLLMConfig(reader, llmCfg, stdin, stdout, stderr)
		if err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "non-interactive mode: writing llm config to %s\n", target)
	}

	if !llmCfg.Ready() {
		return fmt.Errorf("incomplete LLM config (base_url / api_key / model all required)")
	}

	// 已有配置：确认后覆盖 llm 段（保留其余段）；显示现值摘要辅助决策，密钥不回显全文
	if _, err := os.Stat(target); err == nil {
		if interactive {
			if old, loadErr := config.LoadFile(target); loadErr == nil && old.LLM.Ready() {
				masked := "****"
				if old.LLM.APIKey != "" {
					masked = old.LLM.APIKey[:1] + "****"
				}
				fmt.Fprintf(stdout, "existing config at %s: base_url=%s model=%s api_key=%s\n",
					target, old.LLM.BaseURL, old.LLM.Model, masked)
				ok, err := promptConfirm(reader, stdout, "overwrite llm section? [y/N] ")
				if err != nil {
					return err
				}
				if !ok {
					fmt.Fprintln(stdout, "aborted, nothing written")
					return nil
				}
			}
		}
	}

	// 连通测试在写盘前（决策 2A）：错配不落地；--no-test 逃生门
	if !*noTest {
		if err := testLLMConnectivity(stdout, llmCfg); err != nil {
			return fmt.Errorf("connectivity test failed, nothing written (fix inputs or rerun with --no-test): %w", err)
		}
		fmt.Fprintf(stdout, "connectivity test ok (model: %s)\n", llmCfg.Model)
	}

	if err := config.SaveLLM(target, llmCfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "==> wrote llm config to %s\n", target)
	fmt.Fprintln(stdout, "Try it: aruing chat hello")
	return nil
}

// 逐项提问补全缺失的大模型三件套；已给值的项跳过不问
func promptLLMConfig(reader *bufio.Reader, partial config.LLM, stdin io.Reader, stdout, stderr io.Writer) (config.LLM, error) {
	ask := func(label, example string) (string, error) {
		fmt.Fprintf(stdout, "%s (e.g. %s): ", label, example)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}

	var err error
	cfg := partial
	if cfg.BaseURL == "" {
		if cfg.BaseURL, err = ask("base URL", "https://api.openai.com/v1"); err != nil {
			return cfg, err
		}
	}
	if cfg.APIKey == "" {
		// 密钥隐藏输入；stdin 非 TTY（管道/脚本）退化为明文读（测试与自动化路径）
		fmt.Fprint(stdout, "api key (input hidden): ")
		if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
			b, readErr := term.ReadPassword(int(f.Fd()))
			fmt.Fprintln(stdout)
			if readErr != nil {
				return cfg, readErr
			}
			cfg.APIKey = strings.TrimSpace(string(b))
		} else {
			line, readErr := reader.ReadString('\n')
			if readErr != nil && line == "" {
				return cfg, readErr
			}
			cfg.APIKey = strings.TrimSpace(line)
		}
	}
	if cfg.Model == "" {
		if cfg.Model, err = ask("model", "gpt-4o-mini / deepseek-chat"); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}

// 是非确认提问；默认否（直接回车 / EOF 视为不覆盖，宁可让用户重来也不误写）
func promptConfirm(reader *bufio.Reader, stdout io.Writer, question string) (bool, error) {
	fmt.Fprint(stdout, question)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// 用与正式路径同一客户端发一次最小补全探测网关可达与凭证有效
//
// 只为验证连通与鉴权（发一个字的回复即停），不为内容；进度打到 stdout
func testLLMConnectivity(stdout io.Writer, cfg config.LLM) error {
	client, err := llm.NewClient(cfg.ToClientConfig())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTestTimeout)
	defer cancel()
	fmt.Fprint(stdout, "testing connection...\n")
	_, err = client.Generate(ctx, llm.Request{
		System: "You are a connectivity probe. Reply with a single word and stop.",
		User:   "ping",
	})
	return err
}
