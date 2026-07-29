package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"aruing/internal/config"
	"aruing/internal/core"
	"aruing/internal/session"
	"aruing/internal/store"
)

// 默认输出应为 Markdown：含标题、结论段、证据编号，而非 JSON
func TestDispatchRun(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := dispatch([]string{"run", "demo-api", "为什么访问不了"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("dispatch run: %v", err)
	}
	// 进度日志默认走 stderr；验证流程跑通且不含错误噪声
	if !strings.Contains(stderr.String(), "生成报告") {
		t.Errorf("stderr missing progress marker, got: %q", stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "skeleton") {
		t.Fatalf("stdout still contains skeleton output: %q", out)
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("default output should be markdown, got JSON: %q", out)
	}
	// 假报告标题固定含「诊断报告」，证据编号以 e_ 开头应出现在 Markdown 中
	if !strings.Contains(out, "诊断报告") {
		t.Errorf("missing title in markdown:\n%s", out)
	}
	if !strings.Contains(out, "`e_") {
		t.Errorf("missing evidence id in markdown:\n%s", out)
	}
}

// --format json 应输出可解析的结构化报告，证据链完整
func TestDispatchRunJSON(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := dispatch([]string{"run", "--format", "json", "demo-api", "为什么访问不了"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("dispatch run: %v", err)
	}

	var report core.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if !strings.HasPrefix(report.ID, "rep_") || !strings.HasPrefix(report.RunID, "run_") {
		t.Errorf("report identity was not generated: %#v", report)
	}
	if len(report.Conclusions) != 1 || len(report.Conclusions[0].EvidenceIDs) != 1 {
		t.Fatalf("report evidence chain is incomplete: %#v", report.Conclusions)
	}
	if !strings.HasPrefix(report.Conclusions[0].EvidenceIDs[0], "e_") {
		t.Errorf("evidence ID = %q, want e_ prefix", report.Conclusions[0].EvidenceIDs[0])
	}
}

// 非法 format 应报错
func TestDispatchRunBadFormat(t *testing.T) {
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
	clearLLMEnv(t)
	err := dispatch([]string{"chat", "hello"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("want error when LLM is not configured")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ARUING_LLM") {
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

// newSessionStack 无 LLM 应硬失败，不用 Fake 冒充产品路径
func TestNewSessionStackRequiresLLM(t *testing.T) {
	cfg := config.Config{}
	_, err := newSessionStack(core.NewFactory(), cfg, &bytes.Buffer{})
	if err == nil {
		t.Fatal("want error without LLM")
	}
	if !strings.Contains(err.Error(), "ARUING_LLM") {
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
	if err := chatTurn(ctx, svc, sess.ID, "hello", "markdown", &out); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if err := chatTurn(ctx, svc, sess.ID, "again", "markdown", &out); err != nil {
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

// 清空 LLM 相关 env，保证 chat 无凭据路径可测
func clearLLMEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ARUING_LLM_BASE_URL", "")
	t.Setenv("ARUING_LLM_API_KEY", "")
	t.Setenv("ARUING_LLM_MODEL", "")
}
