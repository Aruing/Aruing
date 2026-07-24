package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"aruing/internal/agent"
)

// ErrLLMOutputInconsistent 应附带模型/prompt 处置提示
func TestFormatRunErrorInconsistent(t *testing.T) {
	t.Parallel()

	err := formatRunError(fmt.Errorf("execute diagnosis: %w", agent.ErrLLMOutputInconsistent))
	if err == nil {
		t.Fatal("want non-nil error")
	}
	if !errors.Is(err, agent.ErrLLMOutputInconsistent) {
		t.Fatalf("chain lost: %v", err)
	}
	if !strings.Contains(err.Error(), "提示:") {
		t.Fatalf("missing hint: %v", err)
	}
	if !strings.Contains(err.Error(), "模型输出") {
		t.Fatalf("unexpected hint: %v", err)
	}
}

// 配置类错误应提示检查 ARUING_LLM_*
func TestFormatRunErrorLLMConfig(t *testing.T) {
	t.Parallel()

	err := formatRunError(fmt.Errorf("build orchestrator: build llm client: llm config: BaseURL is required"))
	if !strings.Contains(err.Error(), "ARUING_LLM_BASE_URL") {
		t.Fatalf("want env hint, got %v", err)
	}
}

// 无关错误不附加提示
func TestFormatRunErrorPassthrough(t *testing.T) {
	t.Parallel()

	orig := errors.New("create run ID: boom")
	err := formatRunError(orig)
	if err != orig {
		t.Fatalf("want same error without wrap, got %v", err)
	}
}

func TestFormatRunErrorNil(t *testing.T) {
	t.Parallel()
	if formatRunError(nil) != nil {
		t.Fatal("nil in → nil out")
	}
}
