package config

import (
	"testing"
)

// LoadFrom 应按键填充字段并 trim；缺失键为空串
func TestLoadFrom(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"ARUING_LLM_BASE_URL":          "  https://example.com/v1  ",
		"ARUING_LLM_API_KEY":           " sk-test ",
		"ARUING_LLM_MODEL":             " gpt-test ",
		"ARUING_KUBECTL_PATH":          " /usr/bin/kubectl ",
		"ARUING_ALLOW_DIAGNOSTIC_EXEC": " true ",
		"ARUING_DEBUG":                 " 1 ",
		"IGNORED":                      "x",
	}
	cfg := LoadFrom(func(k string) string { return env[k] })

	if cfg.LLM.BaseURL != "https://example.com/v1" {
		t.Errorf("BaseURL = %q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "sk-test" {
		t.Errorf("APIKey = %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "gpt-test" {
		t.Errorf("Model = %q", cfg.LLM.Model)
	}
	if cfg.Tools.KubectlPath != "/usr/bin/kubectl" {
		t.Errorf("KubectlPath = %q", cfg.Tools.KubectlPath)
	}
	if !cfg.Tools.AllowDiagnosticExec {
		t.Error("AllowDiagnosticExec = false, want true")
	}
	if !cfg.Debug {
		t.Error("Debug = false, want true")
	}
	if !cfg.LLM.Ready() {
		t.Error("Ready = false, want true")
	}
}

// 任一 LLM 字段缺失时 Ready 为 false
func TestLLMReady(t *testing.T) {
	t.Parallel()

	full := LLM{BaseURL: "u", APIKey: "k", Model: "m"}
	if !full.Ready() {
		t.Fatal("full config should be ready")
	}
	for _, partial := range []LLM{
		{APIKey: "k", Model: "m"},
		{BaseURL: "u", Model: "m"},
		{BaseURL: "u", APIKey: "k"},
		{},
	} {
		if partial.Ready() {
			t.Errorf("Ready = true for partial %#v", partial)
		}
	}
}

// ToClientConfig 只映射三字段，不臆造超时
func TestToClientConfig(t *testing.T) {
	t.Parallel()

	c := LLM{BaseURL: "u", APIKey: "k", Model: "m"}.ToClientConfig()
	if c.BaseURL != "u" || c.APIKey != "k" || c.Model != "m" {
		t.Fatalf("mapped = %#v", c)
	}
	if c.Timeout != 0 || c.MaxRetries != 0 {
		t.Fatalf("want zero Timeout/MaxRetries, got %#v", c)
	}
}

// nil getenv 不得 panic，得到空配置；exec 开关默认关
func TestLoadFromNil(t *testing.T) {
	t.Parallel()

	cfg := LoadFrom(nil)
	if cfg.LLM.Ready() || cfg.Tools.KubectlPath != "" {
		t.Fatalf("want empty config, got %#v", cfg)
	}
	if cfg.Tools.AllowDiagnosticExec {
		t.Error("AllowDiagnosticExec should default to false")
	}
}

// 非布尔或空值的 exec 开关应解析为 false，避免误开高自由度动作
func TestAllowDiagnosticExecParsing(t *testing.T) {
	t.Parallel()

	load := func(raw string) Config {
		return LoadFrom(func(k string) string {
			if k == "ARUING_ALLOW_DIAGNOSTIC_EXEC" {
				return raw
			}
			return ""
		})
	}
	if load("").Tools.AllowDiagnosticExec || load("notabool").Tools.AllowDiagnosticExec {
		t.Error("empty/invalid should be false")
	}
	if !load("true").Tools.AllowDiagnosticExec || !load("1").Tools.AllowDiagnosticExec {
		t.Error("true/1 should be true")
	}
}
