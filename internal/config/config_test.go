package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 按环境变量键填充配置并去掉首尾空白；缺失键为空串
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

// 任一大模型字段缺失时就绪为否
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

// 映射客户端时只带出三字段，不臆造超时与重试
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

// 注入空的环境读取函数不得崩溃，得到空配置且诊断执行默认关闭
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

// 非布尔或空值的执行开关应解析为否，避免误开高自由度动作
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

// 三件套齐全通过；缺项时错误文案列出缺失键
func TestValidateLLM(t *testing.T) {
	t.Parallel()

	if err := ValidateLLM(Config{LLM: LLM{BaseURL: "u", APIKey: "k", Model: "m"}}); err != nil {
		t.Fatalf("full: %v", err)
	}
	err := ValidateLLM(Config{})
	if err == nil {
		t.Fatal("want error for empty")
	}
	if !strings.Contains(err.Error(), "ARUING_LLM_BASE_URL") {
		t.Fatalf("want missing list, got %v", err)
	}
	err = ValidateLLM(Config{LLM: LLM{Model: "m"}})
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("partial model only: %v", err)
	}
}

// 完整配置文件与仅含部分字段的文件均可解析
func TestLoadFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
llm:
  base_url: https://api.example/v1
  api_key: sk-file
  model: file-model
tools:
  kubectl_path: /bin/kubectl
  allow_diagnostic_exec: true
debug: true
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "file-model" || !cfg.Tools.AllowDiagnosticExec || !cfg.Debug {
		t.Fatalf("got %#v", cfg)
	}

	minimal := filepath.Join(dir, "min.yaml")
	if writeErr := os.WriteFile(minimal, []byte("llm:\n  model: only\n"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	cfg, err = LoadFile(minimal)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "only" || cfg.LLM.BaseURL != "" || cfg.Debug {
		t.Fatalf("defaults: %#v", cfg)
	}
}

// 环境变量覆盖文件值；布尔键只要存在即覆盖，含显式假
func TestMergeEnvLookup(t *testing.T) {
	t.Parallel()

	base := Config{
		LLM:   LLM{BaseURL: "file-url", APIKey: "file-key", Model: "file-model"},
		Tools: Tools{KubectlPath: "/file/kubectl", AllowDiagnosticExec: true},
		Debug: true,
	}
	env := map[string]string{
		"ARUING_LLM_MODEL":             "env-model",
		"ARUING_ALLOW_DIAGNOSTIC_EXEC": "false",
		"ARUING_DEBUG":                 "0",
	}
	cfg := MergeEnvLookup(base, func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	})
	if cfg.LLM.BaseURL != "file-url" || cfg.LLM.Model != "env-model" {
		t.Fatalf("string merge: %#v", cfg.LLM)
	}
	if cfg.Tools.AllowDiagnosticExec || cfg.Debug {
		t.Fatalf("bool env false should win: %#v", cfg)
	}
}

type fakeFileInfo struct{ name string }

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 1 }
func (f fakeFileInfo) Mode() fs.FileMode  { return 0o644 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

// 自动搜索优先演练目录；全无时未命中；显式路径缺失报错；配置环境变量优先于自动链
func TestResolveConfigPath(t *testing.T) {
	t.Parallel()

	exists := map[string]bool{}
	opt := ResolveOptions{
		Cwd:           "/proj",
		UserConfigDir: "/user",
		SystemPath:    "/etc/aruing/config.yaml",
		LookupEnv:     func(string) (string, bool) { return "", false },
		Stat: func(p string) (fs.FileInfo, error) {
			if exists[p] {
				return fakeFileInfo{name: filepath.Base(p)}, nil
			}
			return nil, os.ErrNotExist
		},
	}

	_, ok, err := ResolveConfigPath("", opt)
	if err != nil || ok {
		t.Fatalf("none: ok=%v err=%v", ok, err)
	}

	exists["/proj/playground/config.yaml"] = true
	exists["/user/aruing/config.yaml"] = true
	path, ok, err := ResolveConfigPath("", opt)
	if err != nil || !ok || path != "/proj/playground/config.yaml" {
		t.Fatalf("playground first: path=%q ok=%v err=%v", path, ok, err)
	}

	delete(exists, "/proj/playground/config.yaml")
	path, ok, err = ResolveConfigPath("", opt)
	if err != nil || !ok || path != "/user/aruing/config.yaml" {
		t.Fatalf("user: path=%q ok=%v err=%v", path, ok, err)
	}

	_, _, err = ResolveConfigPath("/missing.yaml", opt)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("explicit missing: %v", err)
	}

	exists["/forced.yaml"] = true
	opt.LookupEnv = func(k string) (string, bool) {
		if k == "ARUING_CONFIG" {
			return "/forced.yaml", true
		}
		return "", false
	}
	path, ok, err = ResolveConfigPath("", opt)
	if err != nil || !ok || path != "/forced.yaml" {
		t.Fatalf("ARUING_CONFIG: path=%q ok=%v err=%v", path, ok, err)
	}
}

// 文件与环境合并后通过校验；无大模型配置时返回校验错误
func TestLoadResolvedWith(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
llm:
  base_url: https://from-file/v1
  api_key: file-key
  model: file-model
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, used, err := LoadResolvedWith(path, ResolveOptions{
		LookupEnv: func(k string) (string, bool) {
			if k == "ARUING_LLM_MODEL" {
				return "env-model", true
			}
			return "", false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if used != path {
		t.Fatalf("used=%q", used)
	}
	if cfg.LLM.Model != "env-model" || cfg.LLM.BaseURL != "https://from-file/v1" {
		t.Fatalf("%#v", cfg.LLM)
	}

	_, _, err = LoadResolvedWith("", ResolveOptions{
		Cwd:           dir,
		UserConfigDir: dir,
		SystemPath:    "",
		LookupEnv:     func(string) (string, bool) { return "", false },
	})
	if err == nil {
		t.Fatal("want ValidateLLM error without LLM")
	}
}
