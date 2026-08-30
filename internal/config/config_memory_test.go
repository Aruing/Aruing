package config

import "testing"

// 记忆组装段：env 读取与文件同字段；非法值静默归零交默认兜底（口径与 acquire 一致）
func TestLoadFromMemoryEnv(t *testing.T) {
	cfg := LoadFrom(func(k string) string {
		switch k {
		case "ARUING_AGENT_MEMORY_METHOD":
			return "d1-last-n"
		case "ARUING_AGENT_MEMORY_LAST_N":
			return "42"
		default:
			return ""
		}
	})
	if cfg.Agent.Memory.Method != "d1-last-n" {
		t.Fatalf("method: %q", cfg.Agent.Memory.Method)
	}
	if cfg.Agent.Memory.LastN != 42 {
		t.Fatalf("last_n: %d", cfg.Agent.Memory.LastN)
	}
}

// env 查找风格覆盖：仅非空覆盖，非法数值不覆盖
func TestMergeEnvLookupMemory(t *testing.T) {
	base := Config{}
	base.Agent.Memory.Method = "ours"
	base.Agent.Memory.LastN = 10

	out := MergeEnvLookup(base, func(k string) (string, bool) {
		switch k {
		case "ARUING_AGENT_MEMORY_METHOD":
			return "", true // 空串不覆盖
		case "ARUING_AGENT_MEMORY_LAST_N":
			return "bad", true // 非法数值归零不覆盖
		default:
			return "", false
		}
	})
	if out.Agent.Memory.Method != "ours" || out.Agent.Memory.LastN != 10 {
		t.Fatalf("merge must keep base on empty/invalid: %+v", out.Agent.Memory)
	}

	out = MergeEnvLookup(base, func(k string) (string, bool) {
		if k == "ARUING_AGENT_MEMORY_METHOD" {
			return "d2-flat-summary", true
		}
		return "", false
	})
	if out.Agent.Memory.Method != "d2-flat-summary" {
		t.Fatalf("non-empty env must override: %q", out.Agent.Memory.Method)
	}

	// 钉板（pr-agent #130 R1 证伪）：显式 env 0 不重置文件非零值——
	// 非正整数 env 归零等同未设置（parseIntEnv 口径，与 acquire.max_rounds /
	// tools.projection.budget 同族），默认值兑底归消费方 assembleLastN；
	// 「回默认」的正路是文件里写 0 或删行，而非 env 置 0
	out = MergeEnvLookup(base, func(k string) (string, bool) {
		if k == "ARUING_AGENT_MEMORY_LAST_N" {
			return "0", true
		}
		return "", false
	})
	if out.Agent.Memory.LastN != 10 {
		t.Fatalf("explicit env 0 must not reset file value (non-positive = unset): %d", out.Agent.Memory.LastN)
	}
}
