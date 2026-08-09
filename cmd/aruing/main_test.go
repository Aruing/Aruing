package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"aruing/internal/config"
	"aruing/internal/core"
	"aruing/internal/session"
	"aruing/internal/store"
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

// 组装编排器在无大模型配置时应硬失败，无假实现回退
func TestNewOrchestratorRequiresLLM(t *testing.T) {
	cfg := config.Config{}
	_, err := newOrchestrator(core.NewFactory(), cfg, &bytes.Buffer{})
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
