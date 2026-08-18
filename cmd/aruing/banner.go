// 启动 banner 的字段抽象与渲染。设计为可扩展：加内置项只需在 collectBannerFields
// 新增一行；后续可由配置注入用户自定义字段，无需改渲染逻辑。
package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/Aruing/Aruing/internal/config"
)

// config banner 的一行键值对，统一渲染为 `config: <key>=<value>`
type bannerField struct {
	// 渲染到 `config: <key>=...` 的键名
	key string
	// 键对应的展示值
	value string
}

// 按字段顺序渲染 `config: <key>=<value>` 到标准错误
// 只负责格式化、不取值，便于复用与单测
func writeConfigBanner(stderr io.Writer, fields []bannerField) {
	for _, f := range fields {
		fmt.Fprintf(stderr, "config: %s=%s\n", f.key, f.value)
	}
}

// 收集全部 banner 字段写入标准错误
// config 字段后按需追加 note（k8s 工具未注册提示）与 debug（verbose 时 context 失败原因）
func writeStartupBanner(stderr io.Writer, usedPath string, cfg config.Config, ci clusterInfo) {
	writeConfigBanner(stderr, collectBannerFields(usedPath, cfg, ci))
	if ci.kubectlSource == sourceMissing {
		fmt.Fprintln(stderr, "note: k8s tool not registered, cluster diagnosis unavailable")
	}
	if cfg.Debug && ci.contextErr != nil {
		fmt.Fprintf(stderr, "debug: current-context query failed: %v\n", ci.contextErr)
	}
}

// 按固定顺序收集 config banner 字段：path / llm_model / kubectl / context
func collectBannerFields(usedPath string, cfg config.Config, ci clusterInfo) []bannerField {
	return []bannerField{
		{key: "path", value: configPathValue(usedPath)},
		{key: "llm_model", value: fmt.Sprintf("%s ready=%t", modelValue(cfg.LLM.Model), cfg.LLM.Ready())},
		{key: "kubectl", value: kubectlFieldValue(ci)},
		{key: "context", value: ci.context},
	}
}

// 空白裁剪配置路径；无文件时标 env-only
func configPathValue(usedPath string) string {
	if p := strings.TrimSpace(usedPath); p != "" {
		return p
	}
	return "env-only"
}

// 模型名空值占位，避免渲染空串让人误以为配置丢失
func modelValue(model string) string {
	if m := strings.TrimSpace(model); m != "" {
		return m
	}
	return "<empty>"
}

// 渲染 kubectl 路径与来源；未找到时路径占位 <missing>
func kubectlFieldValue(ci clusterInfo) string {
	path := ci.kubectlPath
	if path == "" {
		path = "<missing>"
	}
	return fmt.Sprintf("%s source=%s", path, ci.kubectlSource)
}
