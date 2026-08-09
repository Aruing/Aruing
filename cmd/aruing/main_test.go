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

// run 无 LLM 应明确失败（产品路径不再走 fake）
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

// 非法 format 应报错
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

// usage 与分发应识别 chat 子命令
func TestDispatchChatRecognized(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := dispatch(nil, &stdout, &stderr); err != nil {
		t.Fatalf("dispatch help: %v", err)
	}
	if !strings.Contains(stdout.String(), "chat") {
		t.Fatalf("usage missing chat:\n%s", stdout.String())
	}

	// 无 LLM 时单句 chat 应明确失败，而不是 unknown command
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

// chat 非法 format 应报错
func TestDispatchChatBadFormat(t *testing.T) {
	clearLLMEnv(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := dispatch([]string{"chat", "--format", "xml", "hello"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("want error for unknown format")
	}
	// format 校验在 stack 组装前，不依赖 LLM
	if !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// newSessionStack 无 LLM 应硬失败
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

// newOrchestrator 无 LLM 应硬失败（无 fake 回退）
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

// baseline 轮只写 Content；diagnostic 附加报告块
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

// 注入 Echo 的 Service 证明 chatTurn 接线形状：两轮后 ListMessages 4 条交错
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

// 清空 LLM 相关 env，保证无凭据路径可测
func clearLLMEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ARUING_LLM_BASE_URL", "")
	t.Setenv("ARUING_LLM_API_KEY", "")
	t.Setenv("ARUING_LLM_MODEL", "")
}

// 写一份无 LLM 字段的配置文件路径，并阻断 ARUING_CONFIG / 默认搜索
func emptyConfigPath(t *testing.T) string {
	t.Helper()
	clearLLMEnv(t)
	dir := t.TempDir()
	path := dir + "/config.yaml"
	if err := os.WriteFile(path, []byte("debug: false\n"), 0o600); err != nil {
		t.Fatalf("write empty config: %v", err)
	}
	// 避免进程环境里已有 ARUING_CONFIG 指向完整配置
	t.Setenv("ARUING_CONFIG", path)
	return path
}
