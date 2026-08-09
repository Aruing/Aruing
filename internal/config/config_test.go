package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// ValidateLLM：全齐 OK；缺项列出
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

// LoadFile + 省略字段
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
	if err := os.WriteFile(minimal, []byte("llm:\n  model: only\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadFile(minimal)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "only" || cfg.LLM.BaseURL != "" || cfg.Debug {
		t.Fatalf("defaults: %#v", cfg)
	}
}

// env 覆盖文件；bool 键存在即生效
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

// Resolve：playground 优先；全无 ok=false；显式缺失 error；ARUING_CONFIG 优先于自动链
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

// LoadResolved：文件+env 合并后校验
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
