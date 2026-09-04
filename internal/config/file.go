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
			MaxStdoutBytes:      fc.Tools.MaxStdoutBytes,
			Projection:          fc.Tools.Projection,
		},
		// 编排侧取证决策段整段带入（照 TUI 漏拷教训：全部段都在此拷贝）
		Agent: fc.Agent,
		// TUI 段同样从文件带入：此前漏拷导致 config 文件里的 tui.* 全部静默失效
		// （只有 env 覆盖路径生效）；theme/mode/theme_file 一并修复
		TUI: TUI{
			Theme:     strings.TrimSpace(fc.TUI.Theme),
			Mode:      strings.TrimSpace(fc.TUI.Mode),
			ThemeFile: strings.TrimSpace(fc.TUI.ThemeFile),
		},
		Debug: fc.Debug,
	}, nil
}
