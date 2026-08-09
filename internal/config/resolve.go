package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// 解析配置路径时可注入的依赖（单测用）
type ResolveOptions struct {
	// 工作目录；空则 os.Getwd
	Cwd string
	// 用户配置根；空则 os.UserConfigDir
	UserConfigDir string
	// 系统级路径；空则非 Windows 用 /etc/aruing/config.yaml，Windows 为空（跳过）
	SystemPath string
	// 环境查询；空则 os.LookupEnv
	LookupEnv func(string) (string, bool)
	// 文件存在性；空则 os.Stat
	Stat func(string) (fs.FileInfo, error)
}

// 解析应加载的配置文件路径
//
// explicit 非空：必须存在，否则 error
// 否则若 ARUING_CONFIG 非空：必须存在
// 否则按 playground → user → system 找第一个存在的文件
// ok=false 表示无文件（纯 env），err=nil
func ResolveConfigPath(explicit string, opt ResolveOptions) (path string, ok bool, err error) {
	lookup := opt.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	stat := opt.Stat
	if stat == nil {
		stat = os.Stat
	}

	if p := strings.TrimSpace(explicit); p != "" {
		if _, err := stat(p); err != nil {
			if os.IsNotExist(err) {
				return "", false, fmt.Errorf("config file not found: %s", p)
			}
			return "", false, fmt.Errorf("stat config %s: %w", p, err)
		}
		return p, true, nil
	}

	if v, set := lookup("ARUING_CONFIG"); set {
		if p := strings.TrimSpace(v); p != "" {
			if _, err := stat(p); err != nil {
				if os.IsNotExist(err) {
					return "", false, fmt.Errorf("config file not found: %s (ARUING_CONFIG)", p)
				}
				return "", false, fmt.Errorf("stat config %s: %w", p, err)
			}
			return p, true, nil
		}
	}

	cwd := opt.Cwd
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", false, fmt.Errorf("get working directory: %w", err)
		}
	}

	userDir := opt.UserConfigDir
	if userDir == "" {
		var err error
		userDir, err = os.UserConfigDir()
		if err != nil {
			userDir = ""
		}
	}

	systemPath := opt.SystemPath
	if systemPath == "" && runtime.GOOS != "windows" {
		systemPath = "/etc/aruing/config.yaml"
	}

	candidates := []string{
		filepath.Join(cwd, "playground", "config.yaml"),
	}
	if userDir != "" {
		candidates = append(candidates, filepath.Join(userDir, "aruing", "config.yaml"))
	}
	if systemPath != "" {
		candidates = append(candidates, systemPath)
	}

	for _, p := range candidates {
		fi, err := stat(p)
		if err != nil {
			continue
		}
		if fi != nil && fi.IsDir() {
			continue
		}
		return p, true, nil
	}
	return "", false, nil
}

// 解析路径 → 可选 LoadFile → MergeEnv → ValidateLLM
//
// path 为实际使用的文件路径；无文件时 ""（调用方打印 "(none; env only)"）
func LoadResolved(explicit string) (Config, string, error) {
	return LoadResolvedWith(explicit, ResolveOptions{})
}

// LoadResolvedWith 与 LoadResolved 相同，可注入 ResolveOptions
func LoadResolvedWith(explicit string, opt ResolveOptions) (Config, string, error) {
	lookup := opt.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}

	path, found, err := ResolveConfigPath(explicit, opt)
	if err != nil {
		return Config{}, "", err
	}

	var cfg Config
	if found {
		cfg, err = LoadFile(path)
		if err != nil {
			return Config{}, path, err
		}
	}

	cfg = MergeEnvLookup(cfg, lookup)
	if err := ValidateLLM(cfg); err != nil {
		return cfg, path, err
	}
	return cfg, path, nil
}
