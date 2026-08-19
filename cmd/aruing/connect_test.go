package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/config"
)

// 逐项提问应能从管道输入收集三件套并写盘（--no-test 跳过连通测试）
func TestRunConnectInteractiveWrites(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	input := bytes.NewBufferString("https://api.example.com/v1\nsk-secret\ngpt-4o-mini\n")
	var stdout, stderr bytes.Buffer

	err := runConnect([]string{"--no-test", "--config", cfgPath}, input, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runConnect: %v", err)
	}

	// stdout 提示词不泄露密钥明文（隐藏输入路径在本测试退化为明文读，但不回显）
	if strings.Contains(stdout.String(), "sk-secret") {
		t.Fatalf("api key leaked to stdout:\n%s", stdout.String())
	}

	got, err := config.LoadFile(cfgPath)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got.LLM.BaseURL != "https://api.example.com/v1" || got.LLM.APIKey != "sk-secret" || got.LLM.Model != "gpt-4o-mini" {
		t.Fatalf("unexpected llm config: %+v", got.LLM)
	}
}

// 非交互模式（三参全给）应跳过提问直接写盘
func TestRunConnectNonInteractive(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	var stdout, stderr bytes.Buffer
	err := runConnect([]string{
		"--no-test", "--config", cfgPath,
		"--base-url", "https://api.example.com/v1",
		"--api-key", "sk-secret",
		"--model", "gpt-4o-mini",
	}, bytes.NewReader(nil), &stdout, &stderr)
	if err != nil {
		t.Fatalf("runConnect: %v", err)
	}

	got, err := config.LoadFile(cfgPath)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if !got.LLM.Ready() {
		t.Fatalf("llm should be ready: %+v", got.LLM)
	}
}

// 已有可用配置时默认不覆盖（回车 = 否），确认 y 才覆盖
func TestRunConnectOverwriteConfirm(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	seed := config.LLM{BaseURL: "https://old.example.com/v1", APIKey: "old-key", Model: "old-model"}
	if err := config.SaveLLM(cfgPath, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 场景 1：默认回车 → 不覆盖
	input := bytes.NewBufferString("https://new.example.com/v1\nsk-new\ngpt-4o-mini\n\n")
	var stdout, stderr bytes.Buffer
	if err := runConnect([]string{"--no-test", "--config", cfgPath}, input, &stdout, &stderr); err != nil {
		t.Fatalf("runConnect: %v", err)
	}
	got, _ := config.LoadFile(cfgPath)
	if got.LLM.Model != "old-model" {
		t.Fatalf("default should not overwrite, got model=%s", got.LLM.Model)
	}

	// 场景 2：明确 y → 覆盖
	input2 := bytes.NewBufferString("https://new.example.com/v1\nsk-new\ngpt-4o-mini\ny\n")
	if err := runConnect([]string{"--no-test", "--config", cfgPath}, input2, &stdout, &stderr); err != nil {
		t.Fatalf("runConnect: %v", err)
	}
	got2, _ := config.LoadFile(cfgPath)
	if got2.LLM.Model != "gpt-4o-mini" {
		t.Fatalf("confirm y should overwrite, got model=%s", got2.LLM.Model)
	}
}

// 连通测试失败时不得写盘（决策：错配不落地）
func TestRunConnectTestFailureNoWrite(t *testing.T) {
	// 立即断连的假端点：探测请求必然失败
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	var stdout, stderr bytes.Buffer
	err := runConnect([]string{
		"--config", cfgPath,
		"--base-url", srv.URL,
		"--api-key", "bad-key",
		"--model", "whatever",
	}, bytes.NewReader(nil), &stdout, &stderr)
	if err == nil {
		t.Fatal("want error when connectivity test fails")
	}
	if _, statErr := os.Stat(cfgPath); statErr == nil {
		t.Fatal("config must not be written when test fails")
	}
}

// 连通测试成功路径：真写盘
func TestRunConnectTestSuccessWrites(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	var stdout, stderr bytes.Buffer
	err := runConnect([]string{
		"--config", cfgPath,
		"--base-url", srv.URL,
		"--api-key", "test-key",
		"--model", "test-model",
	}, bytes.NewReader(nil), &stdout, &stderr)
	if err != nil {
		t.Fatalf("runConnect: %v", err)
	}
	if !strings.Contains(stdout.String(), "connectivity test ok") {
		t.Fatalf("stdout should report test ok:\n%s", stdout.String())
	}
	got, _ := config.LoadFile(cfgPath)
	if got.LLM.Model != "test-model" {
		t.Fatalf("config should be written, got %+v", got.LLM)
	}
}

// LLM 缺配置错误应指引 connect 入口（错误即向导的发现路径）
func TestValidateLLMHintsConnect(t *testing.T) {
	err := config.ValidateLLM(config.Config{})
	if err == nil {
		t.Fatal("want error for empty config")
	}
	if !strings.Contains(err.Error(), "aruing connect") {
		t.Fatalf("error should hint connect: %v", err)
	}
}
