// 命令行入口的依赖组装模块
//
// 产品路径：LLM 五角色 + 真实 k8s（kubectl 可选）；不再组装 Fake 诊断闭环
// run / chat 均要求 LLM 齐全；Tower 与 Orchestrator 共用同一 Dispatcher
//
// 进程级参数只来自 config.Config，本文件不再直接读 env
package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"aruing/internal/agent"
	"aruing/internal/config"
	"aruing/internal/core"
	"aruing/internal/llm"
	"aruing/internal/session"
	"aruing/internal/store"
	"aruing/internal/tools"
	"aruing/internal/tools/k8s"
)

// 生产环境调查阶段规划轮数上限
// 一轮≈1 次 Planner + N 次工具 + 1 次 Verifier；3 轮可走完 Pod→logs→describe 这类证据链
const productionInvestigateMaxRounds = 3

// 组装编排器所需的角色集合（不含工具图）
type orchestratorRoles struct {
	parser interface {
		Parse(context.Context, core.Run) (core.Query, error)
	}
	resolver agent.ResolveDriver
	planner  interface {
		Plan(context.Context, agent.PlanState) (agent.Plan, error)
	}
	verifier interface {
		Verify(context.Context, core.Query, []core.Hypothesis, []core.Task, []core.Evidence) ([]core.Verdict, error)
	}
	reporter interface {
		Report(context.Context, core.Run, []core.Verdict, []core.Evidence) (core.Report, error)
	}
}

// 工具注册表与调度器；Tower 与 Orchestrator 必须共用同一 Dispatcher
type tooling struct {
	registry     *tools.Registry
	dispatcher   *tools.Dispatcher
	reconEnabled bool
}

// 组装工具注册表与 Dispatcher；kubectl 可用时注册 k8s
func buildTooling(toolsCfg config.Tools) (tooling, error) {
	registry := tools.NewRegistry()
	k8sRegistered, err := maybeRegisterK8s(registry, toolsCfg.KubectlPath)
	if err != nil {
		return tooling{}, err
	}

	var policy tools.Policy = tools.NewReadonlyPolicy()
	if toolsCfg.AllowDiagnosticExec {
		policy = tools.NewDiagnosticPolicy()
	}
	return tooling{
		registry:     registry,
		dispatcher:   tools.NewDispatcher(registry, policy),
		reconEnabled: k8sRegistered,
	}, nil
}

// 当 kubectl 可用时注册后端级 k8s 工具；不可用则跳过
// 路径：显式 KubectlPath > PATH 中的 kubectl
func maybeRegisterK8s(registry *tools.Registry, kubectlPath string) (bool, error) {
	path := kubectlPath
	if path == "" {
		looked, err := exec.LookPath("kubectl")
		if err != nil {
			return false, nil
		}
		path = looked
	}
	tool, err := k8s.New(k8s.Config{
		KubectlPath:    path,
		DefaultTimeout: 30 * time.Second,
		MaxTimeout:     2 * time.Minute,
	})
	if err != nil {
		return false, nil
	}
	if err := registry.Register(tool); err != nil {
		return false, fmt.Errorf("register k8s tool: %w", err)
	}
	return true, nil
}

// 组装 Orchestrator：必须 LLM 齐全，无 Fake 回退
func newOrchestrator(factory *core.Factory, cfg config.Config, progress io.Writer) (*agent.Orchestrator, error) {
	if factory == nil {
		return nil, fmt.Errorf("orchestrator requires a factory")
	}
	if err := config.ValidateLLM(cfg); err != nil {
		return nil, err
	}

	toolsGraph, err := buildTooling(cfg.Tools)
	if err != nil {
		return nil, err
	}

	client, err := llm.NewClient(cfg.LLM.ToClientConfig())
	if err != nil {
		return nil, fmt.Errorf("build llm client: %w", err)
	}
	roles, err := buildLLMRoles(client, factory, toolsGraph.registry.Specs())
	if err != nil {
		return nil, err
	}

	orch := agent.NewOrchestrator(
		roles.parser,
		roles.resolver,
		roles.planner,
		toolsGraph.dispatcher,
		roles.verifier,
		roles.reporter,
		factory,
	)
	configureOrchestrator(orch, toolsGraph.reconEnabled, progress)
	return orch, nil
}

// 组装 chat 用的 Session 栈：MemoryStore + MemoryRunLedger + TowerResponder
// 无 LLM 时硬失败
func newSessionStack(factory *core.Factory, cfg config.Config, progress io.Writer) (*session.Service, error) {
	if err := config.ValidateLLM(cfg); err != nil {
		return nil, err
	}
	if factory == nil {
		return nil, fmt.Errorf("session stack requires a factory")
	}

	toolsGraph, err := buildTooling(cfg.Tools)
	if err != nil {
		return nil, err
	}

	client, err := llm.NewClient(cfg.LLM.ToClientConfig())
	if err != nil {
		return nil, fmt.Errorf("build llm client: %w", err)
	}
	roles, err := buildLLMRoles(client, factory, toolsGraph.registry.Specs())
	if err != nil {
		return nil, err
	}

	orch := agent.NewOrchestrator(
		roles.parser,
		roles.resolver,
		roles.planner,
		toolsGraph.dispatcher,
		roles.verifier,
		roles.reporter,
		factory,
	)
	configureOrchestrator(orch, toolsGraph.reconEnabled, progress)

	ledger := store.NewMemoryRunLedger()
	tower, err := agent.NewTowerResponder(
		client,
		factory,
		orch,
		ledger,
		toolsGraph.dispatcher,
		toolsGraph.registry.Specs(),
	)
	if err != nil {
		return nil, fmt.Errorf("build tower: %w", err)
	}
	if cfg.Debug {
		tower.SetProgress(progress)
	}

	return session.NewService(store.NewMemoryStore(), factory, tower), nil
}

// 用 LLM 客户端组装五角色；specs 与 Dispatcher 同源
func buildLLMRoles(client llm.Client, factory *core.Factory, specs []tools.ToolSpec) (orchestratorRoles, error) {
	parser, err := agent.NewLLMParser(client, factory)
	if err != nil {
		return orchestratorRoles{}, fmt.Errorf("build llm parser: %w", err)
	}
	resolver, err := agent.NewLLMResolver(client, specs)
	if err != nil {
		return orchestratorRoles{}, fmt.Errorf("build llm resolver: %w", err)
	}
	planner, err := agent.NewLLMPlanner(client, factory, specs)
	if err != nil {
		return orchestratorRoles{}, fmt.Errorf("build llm planner: %w", err)
	}
	verifier, err := agent.NewLLMVerifier(client, factory)
	if err != nil {
		return orchestratorRoles{}, fmt.Errorf("build llm verifier: %w", err)
	}
	reporter, err := agent.NewLLMReporter(client, factory)
	if err != nil {
		return orchestratorRoles{}, fmt.Errorf("build llm reporter: %w", err)
	}
	return orchestratorRoles{
		parser:   parser,
		resolver: resolver,
		planner:  planner,
		verifier: verifier,
		reporter: reporter,
	}, nil
}

// 统一生产预算、侦察开关与进度输出
func configureOrchestrator(orch *agent.Orchestrator, reconEnabled bool, progress io.Writer) {
	orch.SetInvestigateMaxRounds(productionInvestigateMaxRounds)
	orch.SetReconEnabled(reconEnabled)
	orch.SetProgress(progress)
}
