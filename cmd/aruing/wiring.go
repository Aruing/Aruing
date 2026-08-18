// 命令行入口的依赖组装模块
//
// 产品路径：大模型五角色加真实集群工具（集群命令可选）；不再组装假诊断闭环
// 单次运行与多轮对话均要求大模型齐全；基线塔与编排器共用同一调度器
//
// 进程级参数只来自配置结构，本文件不再直接读环境变量
package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/Aruing/Aruing/internal/agent"
	"github.com/Aruing/Aruing/internal/config"
	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/llm"
	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
	"github.com/Aruing/Aruing/internal/tools"
	"github.com/Aruing/Aruing/internal/tools/k8s"
)

// 生产环境调查阶段规划轮数上限
// 一轮约一次规划、若干工具调用与一次验证；三轮可走完常见证据链
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

// 工具注册表与调度器；基线塔与编排器必须共用同一调度器
// obsIndex 供基线轮内 evidenceId / evidence.read；编排器路径可不读
type tooling struct {
	registry     *tools.Registry
	dispatcher   *tools.Dispatcher
	obsIndex     *tools.ObservationIndex
	reconEnabled bool
}

// 组装工具注册表与调度器；集群命令可用时注册集群工具与 evidence.read
func buildTooling(toolsCfg config.Tools) (tooling, error) {
	registry := tools.NewRegistry()
	k8sRegistered, err := maybeRegisterK8s(registry, toolsCfg)
	if err != nil {
		return tooling{}, err
	}

	obsIndex := tools.NewObservationIndex()
	evidenceRead, err := tools.NewEvidenceReadTool(obsIndex, registry)
	if err != nil {
		return tooling{}, fmt.Errorf("create evidence.read tool: %w", err)
	}
	if err := registry.Register(evidenceRead); err != nil {
		return tooling{}, fmt.Errorf("register evidence.read tool: %w", err)
	}

	var policy = tools.NewReadonlyPolicy()
	if toolsCfg.AllowDiagnosticExec {
		policy = tools.NewDiagnosticPolicy()
	}
	return tooling{
		registry:     registry,
		dispatcher:   tools.NewDispatcher(registry, policy),
		obsIndex:     obsIndex,
		reconEnabled: k8sRegistered,
	}, nil
}

// 当集群命令可用时注册后端级集群工具；不可用则跳过
// 路径优先使用显式配置，否则在系统路径中查找默认集群命令
// MaxStdoutBytes 零值时由 k8s 包默认（1MiB）
func maybeRegisterK8s(registry *tools.Registry, toolsCfg config.Tools) (bool, error) {
	path := toolsCfg.KubectlPath
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
		MaxStdoutBytes: toolsCfg.MaxStdoutBytes,
	})
	if err != nil {
		return false, nil
	}
	if err := registry.Register(tool); err != nil {
		return false, fmt.Errorf("register k8s tool: %w", err)
	}
	return true, nil
}

// 组装编排器：必须大模型齐全，无假实现回退
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

// 组装多轮对话会话栈：内存存储、诊断账本与基线塔
// 无大模型配置时硬失败
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
		toolsGraph.obsIndex,
	)
	if err != nil {
		return nil, fmt.Errorf("build tower: %w", err)
	}
	if cfg.Debug {
		tower.SetProgress(progress)
	}

	return session.NewService(store.NewMemoryStore(), factory, tower), nil
}

// 用大模型客户端组装五角色；工具规格与调度器同源
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
