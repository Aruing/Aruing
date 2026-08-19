package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"strings"

	"gopkg.in/yaml.v3"
)

// 解析配置路径时可注入的依赖，仅用于单测替换真实文件系统与环境
type ResolveOptions struct {
	// 工作目录；空则取进程当前目录
	Cwd string
	// 用户配置根目录；空则取系统用户配置目录
	UserConfigDir string
	// 系统级配置文件路径；空则非视窗系统默认为系统配置路径，视窗系统跳过
	SystemPath string
	// 查询环境变量；空则使用进程环境
	LookupEnv func(string) (string, bool)
	// 判断路径是否存在；空则使用真实文件系统
	Stat func(string) (fs.FileInfo, error)
}

// 按优先级解析应加载的配置文件路径
//
// 显式路径非空时必须存在，否则报错
// 否则若设置了配置路径环境变量且非空，该路径必须存在
// 否则按演示目录、用户目录、系统路径依次取第一个存在的文件
// 全部不存在时存在标志为假且错误为空，表示仅用环境变量
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

// 解析配置路径后可选读文件，再叠环境变量并校验大模型三件套
//
// 返回的路径为实际使用的文件；无文件时为空串，调用方可展示为仅环境变量
func LoadResolved(explicit string) (Config, string, error) {
	return LoadResolvedWith(explicit, ResolveOptions{})
}

// 与已解析加载相同，可注入路径与环境依赖以便单测
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

// 返回用户级配置文件的规范落点（connect 向导的写入目标）
//
// 与 ResolveConfigPath 搜索链中的用户级候选一致：$XDG_CONFIG_HOME 或
// os.UserConfigDir 下的 aruing/config.yaml；无法解析用户目录时返回错误
// （此时 connect 应提示改用 --config 显式路径）
func UserConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(dir, "aruing", "config.yaml"), nil
}

// 把大模型三件套写入配置文件；已存在时仅覆盖 llm 段，其余段原样保留
//
// 这是 aruing connect 的落盘函数：读旧文件（可不存在）→ 覆盖 llm → 序列化写回。
// 远期多渠道（channels + 优先级降级）演进时升级文件 schema，此函数仍是唯一写入口
func SaveLLM(path string, llmCfg LLM) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("config path is empty")
	}
	var fc fileConfig
	data, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("read existing config %s: %w", path, readErr)
	}
	if readErr == nil {
		if err := yaml.Unmarshal(data, &fc); err != nil {
			return fmt.Errorf("parse existing config %s: %w", path, err)
		}
	}
	fc.LLM = llmCfg
	out, err := yaml.Marshal(fc)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create config dir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}
