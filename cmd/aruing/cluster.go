// 启动 banner 的 k8s 连接信息探测：kubectl 路径来源 + 当前 context。
// 本文件只做可观测性探测（本地查询、不连集群、不产证据），不接入诊断路径（守 §4）。
package main

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"aruing/internal/config"
)

// kubectl 来源标签
const (
	sourceConfig  = "config"  // 用户在 config.Tools.KubectlPath 显式配置
	sourcePATH    = "PATH"    // 通过 exec.LookPath 在系统 PATH 中查到
	sourceMissing = "missing" // 既未配置也未在 PATH 找到，k8s 工具不会注册
)

// current-context 查询结果占位标记
const (
	contextNone    = "<none>" // kubectl 在但无 current-context（空配置 / 查询失败 / 超时）
	contextMissing = "<n/a>"  // kubectl 不可用，查询无意义
)

// current-context 查询超时：本地 kubeconfig 解析通常极快，2s 足够且不拖累启动
const contextQueryTimeout = 2 * time.Second

// 启动 banner 可展示的 k8s 连接信息
type clusterInfo struct {
	// 解析后的 kubectl 路径；空表示未找到
	kubectlPath string
	// kubectl 来源：sourceConfig | sourcePATH | sourceMissing
	kubectlSource string
	// current-context 名，或 contextNone / contextMissing 占位
	context string
	// current-context 查询失败原因；verbose 时展示，nil 表示无失败
	contextErr error
}

// 执行 `kubectl config current-context` 并返回原始输出
// 抽成可注入类型便于测试：成功 / 失败 / 超时 / 空各路径无需真实 kubectl
type ctxRunner func(ctx context.Context, kubectlPath string) (string, error)

// 解析 kubectl 路径并探测当前 context，供 main 调用点一步到位
func resolveCluster(ctx context.Context, toolsCfg config.Tools, runCtx ctxRunner) clusterInfo {
	path, src := resolveKubectlPath(toolsCfg.KubectlPath)
	return resolveClusterInfo(ctx, path, src, runCtx)
}

// 在已知 kubectl 路径与来源上探测当前 context（拆出便于分别测试）
// kubectl 不可用（sourceMissing）时不查 context（标 contextMissing）
// 可用但查询失败 / 空 / 超时标 contextNone，并保留原因供 verbose 展示
func resolveClusterInfo(ctx context.Context, kubectlPath, kubectlSource string, runCtx ctxRunner) clusterInfo {
	info := clusterInfo{
		kubectlPath:   kubectlPath,
		kubectlSource: kubectlSource,
	}
	if kubectlSource == sourceMissing {
		info.context = contextMissing
		return info
	}
	out, err := runCtx(ctx, kubectlPath)
	if err != nil {
		info.context = contextNone
		info.contextErr = err
		return info
	}
	if name := strings.TrimSpace(out); name != "" {
		info.context = name
		return info
	}
	info.context = contextNone
	return info
}

// 解析 kubectl 路径与来源，分支与 wiring.maybeRegisterK8s 一致
// （config.KubectlPath 优先，否则 exec.LookPath），避免两处不一致
func resolveKubectlPath(configured string) (path, source string) {
	if configured != "" {
		return configured, sourceConfig
	}
	looked, err := exec.LookPath("kubectl")
	if err != nil {
		return "", sourceMissing
	}
	return looked, sourcePATH
}

// 产品默认的 current-context 查询器
// 调用 `kubectl config current-context`，带超时；本地 kubeconfig 查询、不连集群、不产证据
// 明确不是诊断取证：不经 Dispatcher、结果不进 Evidence / Run
func defaultKubectlContext(ctx context.Context, kubectlPath string) (string, error) {
	qctx, cancel := context.WithTimeout(ctx, contextQueryTimeout)
	defer cancel()
	out, err := exec.CommandContext(qctx, kubectlPath, "config", "current-context").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
