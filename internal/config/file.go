package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// 从给定路径读取配置文件；允许省略任意键，省略项保持零值
func LoadFile(path string) (Config, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Config{}, fmt.Errorf("config path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return Config{
		LLM: LLM{
			BaseURL: strings.TrimSpace(fc.LLM.BaseURL),
			APIKey:  strings.TrimSpace(fc.LLM.APIKey),
			Model:   strings.TrimSpace(fc.LLM.Model),
		},
		Tools: Tools{
			KubectlPath:         strings.TrimSpace(fc.Tools.KubectlPath),
			AllowDiagnosticExec: fc.Tools.AllowDiagnosticExec,
		},
		Debug: fc.Debug,
	}, nil
}
