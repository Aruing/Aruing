package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/config"
	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 单次运行在无大模型配置时应明确失败，不再走假实现
func TestDispatchRunRequiresLLM(t *testing.T) {
	cfgPath := emptyConfigPath(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := dispatch([]string{"run", "--config", cfgPath, "demo-api", "为什么访问不了"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("want error when LLM is not configured")
	}
	msg := err.Error()
	if !strings.Contains(msg, "LLM") && !strings.Contains(msg, "ARUING_LLM") && !strings.Contains(msg, "llm.") {
		t.Fatalf("error should mention LLM config, got: %v", err)
	}
}

// 单次运行非法输出格式应报错
func TestDispatchRunBadFormat(t *testing.T) {
	clearLLMEnv(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := dispatch([]string{"run", "--format", "xml", "whatever"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("want error for unknown format")
	}
	if !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// 用法说明与分发应识别多轮对话子命令
func TestDispatchChatRecognized(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := dispatch(nil, &stdout, &stderr); err != nil {
		t.Fatalf("dispatch help: %v", err)
	}
	if !strings.Contains(stdout.String(), "chat") {
		t.Fatalf("usage missing chat:\n%s", stdout.String())
	}

	// 无大模型时单句对话应明确失败，而不是未知命令
	cfgPath := emptyConfigPath(t)
	err := dispatch([]string{"chat", "--config", cfgPath, "hello"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("want error when LLM is not configured")
	}
	msg := err.Error()
	if !strings.Contains(msg, "LLM") && !strings.Contains(msg, "ARUING_LLM") && !strings.Contains(msg, "llm.") {
		t.Fatalf("error should mention LLM env vars, got: %v", err)
	}
	if strings.Contains(msg, "unknown command") {
		t.Fatalf("chat should be recognized: %v", err)
	}
}

// 多轮对话非法输出格式应报错
func TestDispatchChatBadFormat(t *testing.T) {
	clearLLMEnv(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := dispatch([]string{"chat", "--format", "xml", "hello"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("want error for unknown format")
	}
	// 格式校验在栈组装前，不依赖大模型
	if !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// 版本子命令首行格式稳定，供脚本与后续自更新能力解析；注入值应原样透出
func TestRunVersion(t *testing.T) {
	cases := []struct {
		name    string
		version string
		commit  string
		date    string
		want    []string
	}{
		{
			name:    "注入发布信息",
			version: "0.1.0",
			commit:  "e7b862c",
			date:    "2026-08-18T15:00:00Z",
			want:    []string{"aruing version 0.1.0\n", "commit: e7b862c\n", "built:  2026-08-18T15:00:00Z\n"},
		},
		{
			name:    "未注入时输出默认值",
			version: "dev",
			commit:  "none",
			date:    "unknown",
			want:    []string{"aruing version dev\n", "commit: none\n", "built:  unknown\n"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 临时改写包级变量模拟链接期注入，用完恢复，避免影响其他用例
			oldVersion, oldCommit, oldDate := version, commit, date
			t.Cleanup(func() { version, commit, date = oldVersion, oldCommit, oldDate })
			version, commit, date = tc.version, tc.commit, tc.date

			var stdout bytes.Buffer
			if err := runVersion(nil, &stdout); err != nil {
				t.Fatalf("runVersion: %v", err)
			}
			got := stdout.String()
			for _, line := range tc.want {
				if !strings.Contains(got, line) {
					t.Fatalf("output missing %q, got:\n%s", line, got)
				}
			}
		})
	}
}

// 组装会话栈在无大模型配置时应硬失败
func TestNewSessionStackRequiresLLM(t *testing.T) {
	cfg := config.Config{}
	_, err := newSessionStack(core.NewFactory(), cfg, &bytes.Buffer{})
	if err == nil {
		t.Fatal("want error without LLM")
	}
	if !strings.Contains(err.Error(), "LLM") && !strings.Contains(err.Error(), "llm.") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// 启动横幅应展示路径、模型、kubectl/context，且不泄露密钥
func TestWriteStartupBanner(t *testing.T) {
	var stderr bytes.Buffer
	writeStartupBanner(&stderr, "/tmp/demo.yaml", config.Config{
		LLM: config.LLM{
			BaseURL: "https://example.com/v1",
			APIKey:  "sk-secret",
			Model:   "demo-model",
		},
	}, clusterInfo{
		kubectlPath:   "/usr/local/bin/kubectl",
		kubectlSource: sourcePATH,
		context:       "kind-demo",
	})
	got := stderr.String()
	for _, want := range []string{
		"path=/tmp/demo.yaml",
		"llm_model=demo-model",
		"ready=true",
		"kubectl=/usr/local/bin/kubectl source=PATH",
		"context=kind-demo",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "sk-secret") {
		t.Fatalf("must not print api key: %q", got)
	}
}

// kubectl 缺失时应降级标记并提示不可用，不阻断启动
func TestWriteStartupBannerKubectlMissing(t *testing.T) {
	var stderr bytes.Buffer
	writeStartupBanner(&stderr, "", config.Config{}, clusterInfo{
		kubectlSource: sourceMissing,
		context:       contextMissing,
	})
	got := stderr.String()
	for _, want := range []string{
		"path=env-only",
		"ready=false",
		"kubectl=<missing> source=missing",
		"context=<n/a>",
		"k8s tool not registered",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// 组装编排器在无大模型配置时应硬失败，无假实现回退
func TestNewOrchestratorRequiresLLM(t *testing.T) {
	cfg := config.Config{}
	_, _, err := newOrchestrator(core.NewFactory(), cfg, &bytes.Buffer{})
	if err == nil {
		t.Fatal("want error without LLM")
	}
	if !strings.Contains(err.Error(), "LLM") && !strings.Contains(err.Error(), "llm.") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// 基线轮只写助手正文；诊断轮附加报告块
func TestWriteTurnResult(t *testing.T) {
	t.Run("baseline", func(t *testing.T) {
		var out bytes.Buffer
		err := writeTurnResult(&out, "markdown", session.TurnResult{
			AssistantMessage: session.Message{
				Content: "hi there",
				Mode:    session.ModeBaseline,
			},
		})
		if err != nil {
			t.Fatalf("writeTurnResult: %v", err)
		}
		got := out.String()
		if got != "hi there\n" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("diagnostic", func(t *testing.T) {
		var out bytes.Buffer
		report := core.Report{
			ID:      "rep_test",
			RunID:   "run_test",
			Title:   "诊断报告",
			Summary: "摘要一行",
		}
		err := writeTurnResult(&out, "markdown", session.TurnResult{
			AssistantMessage: session.Message{
				Content: "已完成诊断",
				Mode:    session.ModeDiagnostic,
			},
			Report: &report,
		})
		if err != nil {
			t.Fatalf("writeTurnResult: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "已完成诊断") {
			t.Errorf("missing content: %q", got)
		}
		if !strings.Contains(got, "---") {
			t.Errorf("missing separator: %q", got)
		}
		if !strings.Contains(got, "诊断报告") {
			t.Errorf("missing report title: %q", got)
		}
	})
}

// 注入回显响应器证明对话接线：两轮后应有四条用户与助手交错消息
func TestChatTurnEchoStack(t *testing.T) {
	factory := core.NewFactory()
	mem := store.NewMemoryStore()
	svc := session.NewService(mem, factory, session.EchoResponder{})
	ctx := context.Background()
	sess, err := svc.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	var out bytes.Buffer
	if err = chatTurn(ctx, svc, sess.ID, "hello", "markdown", &out); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if err = chatTurn(ctx, svc, sess.ID, "again", "markdown", &out); err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	msgs, err := mem.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4", len(msgs))
	}
	if msgs[0].Role != session.RoleUser || msgs[1].Role != session.RoleAssistant {
		t.Fatalf("want user/assistant interleave, got %s/%s", msgs[0].Role, msgs[1].Role)
	}
	if !strings.Contains(out.String(), "hello") || !strings.Contains(out.String(), "again") {
		t.Fatalf("stdout missing echo content: %q", out.String())
	}
}

// 清空大模型相关环境变量，保证无凭据路径可测
func clearLLMEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ARUING_LLM_BASE_URL", "")
	t.Setenv("ARUING_LLM_API_KEY", "")
	t.Setenv("ARUING_LLM_MODEL", "")
}

// 写入无大模型字段的配置文件，并阻断环境配置与默认搜索路径
func emptyConfigPath(t *testing.T) string {
	t.Helper()
	clearLLMEnv(t)
	dir := t.TempDir()
	path := dir + "/config.yaml"
	if err := os.WriteFile(path, []byte("debug: false\n"), 0o600); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	// 避免进程环境里已有配置路径环境变量指向完整配置
	t.Setenv("ARUING_CONFIG", path)
	return path
}

// --ui 非法值应明确报错（在组装前校验）
func TestChatBadUIMode(t *testing.T) {
	clearLLMEnv(t)
	var stdout, stderr bytes.Buffer
	err := dispatch([]string{"chat", "--ui", "bogus", "hello"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown ui mode") {
		t.Fatalf("err = %v, want unknown ui mode", err)
	}
}

// theme_file 相对路径以 config 文件目录为基准；绝对路径与空值不动
func TestResolveThemeFilePath(t *testing.T) {
	cfg := &config.Config{}
	cfg.TUI.ThemeFile = "theme.yaml"
	resolveThemeFilePath(cfg, "/etc/aruing/config.yaml")
	if cfg.TUI.ThemeFile != "/etc/aruing/theme.yaml" {
		t.Fatalf("relative: %q", cfg.TUI.ThemeFile)
	}
	cfg.TUI.ThemeFile = "/abs/theme.yaml"
	resolveThemeFilePath(cfg, "/etc/aruing/config.yaml")
	if cfg.TUI.ThemeFile != "/abs/theme.yaml" {
		t.Fatalf("absolute: %q", cfg.TUI.ThemeFile)
	}
	cfg.TUI.ThemeFile = ""
	resolveThemeFilePath(cfg, "/etc/aruing/config.yaml")
	if cfg.TUI.ThemeFile != "" {
		t.Fatalf("empty: %q", cfg.TUI.ThemeFile)
	}
	resolveThemeFilePath(nil, "/x") // nil 不 panic
}

// 非 tty stdin 行模式：逐行同会话跑 Turn；空行忽略、exit 停止（smoke 脚本依赖此行为）
func TestChatStdinLoop(t *testing.T) {
	factory := core.NewFactory()
	mem := store.NewMemoryStore()
	svc := session.NewService(mem, factory, session.EchoResponder{})
	ctx := context.Background()
	sess, err := svc.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	in := strings.NewReader("hello\n\nagain\nexit\nnever-sent\n")
	var out bytes.Buffer
	if err = chatStdinLoop(ctx, svc, sess.ID, "markdown", &out, in); err != nil {
		t.Fatalf("stdin loop: %v", err)
	}

	// exit 之后的行不应产生 Turn
	msgs, err := mem.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("messages = %d, want 4 (2 turns, exit stops)", len(msgs))
	}
	if !strings.Contains(out.String(), "hello") || !strings.Contains(out.String(), "again") {
		t.Fatalf("stdout missing echo content: %q", out.String())
	}
}
