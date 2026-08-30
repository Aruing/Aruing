package config

import (
	"os"
	"path/filepath"
	"testing"
)

// env 路径解析取证决策段：方法与全套参数；非法数值归零（方法名校验留装配层）
func TestLoadFromAcquireEnv(t *testing.T) {
	env := map[string]string{
		"ARUING_AGENT_ACQUIRE_METHOD":     "b1-serial",
		"ARUING_AGENT_ACQUIRE_MAX_ROUNDS": "5",
		"ARUING_AGENT_ACQUIRE_SEED":       "42",
		"ARUING_AGENT_ACQUIRE_ALPHA":      "4",
		"ARUING_AGENT_ACQUIRE_P_STAR":     "0.95",
		"ARUING_AGENT_ACQUIRE_A":          "25",
		"ARUING_AGENT_ACQUIRE_TAU":        "0.02",
		"ARUING_AGENT_ACQUIRE_DELTA":      "0.01",
		"ARUING_AGENT_ACQUIRE_MASS_FLOOR": "0.1",
	}
	cfg := LoadFrom(func(k string) string { return env[k] })
	a := cfg.Agent.Acquire
	if a.Method != "b1-serial" || a.MaxRounds != 5 || a.Seed != 42 || a.Alpha != 4 || a.PStar != 0.95 ||
		a.A != 25 || a.Tau != 0.02 || a.Delta != 0.01 || a.MassFloor != 0.1 {
		t.Fatalf("取证决策配置解析错误：%+v", a)
	}

	// 种子非法数值归零（与方法名不同，种子无枚举域可校，静默归零与其他数值参数同口径）
	badSeed := LoadFrom(func(k string) string {
		if k == "ARUING_AGENT_ACQUIRE_SEED" {
			return "not-a-number"
		}
		return ""
	})
	if badSeed.Agent.Acquire.Seed != 0 {
		t.Fatalf("非法种子应归零，got %d", badSeed.Agent.Acquire.Seed)
	}

	// 非法数值归零：参数域校验由 acquire 包消费侧兜底，配置层不二次报错
	bad := LoadFrom(func(k string) string {
		if k == "ARUING_AGENT_ACQUIRE_ALPHA" {
			return "not-a-number"
		}
		return ""
	})
	if bad.Agent.Acquire.Alpha != 0 {
		t.Fatalf("非法 α 应归零，got %v", bad.Agent.Acquire.Alpha)
	}
}

// env 覆盖文件配置（文件 ours + env b1-serial → b1-serial）
func TestMergeEnvLookupAcquire(t *testing.T) {
	base := Config{Agent: Agent{Acquire: Acquire{Method: "ours", Alpha: 3}}}
	merged := MergeEnvLookup(base, func(k string) (string, bool) {
		if k == "ARUING_AGENT_ACQUIRE_METHOD" {
			return "b1-serial", true
		}
		return "", false
	})
	if merged.Agent.Acquire.Method != "b1-serial" || merged.Agent.Acquire.Alpha != 3 {
		t.Fatalf("env 覆盖错误：%+v", merged.Agent.Acquire)
	}
}

// 文件路径解析取证决策段（防 LoadFile 漏拷段：beta20 TUI 前科）
func TestLoadFileCarriesAcquireSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `llm:
  base_url: https://example.test
  api_key: k
  model: m
agent:
  acquire:
    method: b1-serial
    alpha: 4
    p_star: 0.95
    tau: 0.02
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	a := cfg.Agent.Acquire
	if a.Method != "b1-serial" || a.Alpha != 4 || a.PStar != 0.95 || a.Tau != 0.02 {
		t.Fatalf("文件取证决策段漏拷或解析错误：%+v", a)
	}
	if a.A != 0 || a.Delta != 0 || a.MassFloor != 0 {
		t.Fatalf("未配置字段应为零值：%+v", a)
	}
}
