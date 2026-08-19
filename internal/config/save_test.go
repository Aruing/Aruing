package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 向盘落 LLM 三件套后应能读回，且 llm 段内容一致
func TestSaveLLMRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	in := LLM{BaseURL: "https://api.example.com/v1", APIKey: "sk-test", Model: "gpt-4o-mini"}

	if err := SaveLLM(path, in); err != nil {
		t.Fatalf("SaveLLM: %v", err)
	}

	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got.LLM != in {
		t.Fatalf("llm round trip mismatch: got %+v want %+v", got.LLM, in)
	}
}

// 已有配置文件时应保留 tools/tui 等其他段，仅覆盖 llm 段
func TestSaveLLMPreservesOtherSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	seed := `llm:
  base_url: https://old.example.com/v1
  api_key: old-key
  model: old-model
tools:
  kubectl_path: /usr/local/bin/kubectl
tui:
  theme: light
debug: true
`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	newLLM := LLM{BaseURL: "https://new.example.com/v1", APIKey: "new-key", Model: "new-model"}
	if err := SaveLLM(path, newLLM); err != nil {
		t.Fatalf("SaveLLM: %v", err)
	}

	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got.LLM != newLLM {
		t.Fatalf("llm should be overwritten: got %+v", got.LLM)
	}
	if got.Tools.KubectlPath != "/usr/local/bin/kubectl" {
		t.Fatalf("tools.kubectl_path should be preserved, got %q", got.Tools.KubectlPath)
	}
	if got.TUI.Theme != "light" {
		t.Fatalf("tui.theme should be preserved, got %q", got.TUI.Theme)
	}
	if !got.Debug {
		t.Fatal("debug should be preserved")
	}
}

// 目录不存在时应自动创建（connect 首次运行时用户级目录尚未建立）
func TestSaveLLMCreatesMissingDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "aruing", "config.yaml")
	if err := SaveLLM(path, LLM{BaseURL: "u", APIKey: "k", Model: "m"}); err != nil {
		t.Fatalf("SaveLLM: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file should exist: %v", err)
	}
}

// 空 path 应明确报错而非写到意外位置
func TestSaveLLMEmptyPath(t *testing.T) {
	if err := SaveLLM("", LLM{}); err == nil {
		t.Fatal("want error for empty path")
	}
}

// 已有配置文件损坏（非法 YAML）时拒绝覆盖，避免静默丢配置
func TestSaveLLMRejectsCorruptExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("llm: [unclosed"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SaveLLM(path, LLM{BaseURL: "u", APIKey: "k", Model: "m"}); err == nil {
		t.Fatal("want error for corrupt existing file")
	}
}

// 用户级配置路径应落在 os.UserConfigDir 下的 aruing 子目录
func TestUserConfigPath(t *testing.T) {
	p, err := UserConfigPath()
	if err != nil {
		t.Fatalf("UserConfigPath: %v", err)
	}
	if filepath.Base(filepath.Dir(p)) != "aruing" || filepath.Base(p) != "config.yaml" {
		t.Fatalf("unexpected user config path: %s", p)
	}
}
