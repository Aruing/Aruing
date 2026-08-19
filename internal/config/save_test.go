package config

import (
	"os"
	"path/filepath"
	"strings"
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

// 未知顶层段（未来新增或自定义键）应原样保留，仅 llm 被替换
func TestSaveLLMPreservesUnknownSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	seed := `llm:
  base_url: https://old/v1
  api_key: k
  model: m
future_feature:
  channels:
    - name: primary
tools:
  kubectl_path: /bin/kubectl
`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SaveLLM(path, LLM{BaseURL: "https://new/v1", APIKey: "k2", Model: "m2"}); err != nil {
		t.Fatalf("SaveLLM: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "future_feature") || !strings.Contains(string(data), "channels") {
		t.Fatalf("unknown sections must be preserved:\n%s", data)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if got.LLM.Model != "m2" || got.Tools.KubectlPath != "/bin/kubectl" {
		t.Fatalf("llm replaced and tools preserved: %+v", got)
	}
}

// 已存在文件权限宽松时，写入后应收紧到 0600（含密钥文件不应组/全局可读）
func TestSaveLLMTightensPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("llm: {}\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SaveLLM(path, LLM{BaseURL: "u", APIKey: "secret", Model: "m"}); err != nil {
		t.Fatalf("SaveLLM: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("want 0600 after save, got %o", fi.Mode().Perm())
	}
}
