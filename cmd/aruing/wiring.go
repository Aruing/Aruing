// 命令行入口的依赖组装模块
//
// 这一层负责把"用哪些角色实例组装 Orchestrator"集中收敛：
// - 在没有 LLM 配置时，所有角色使用 fake，假闭环继续可跑、可测（CI、make test 默认走这条路径）
// - 在 LLM 配置齐全时，把 parser / resolver / planner / verifier / reporter 换成 LLM 实现
// - 工具始终经 Dispatcher + 只读 Policy；kubectl 可用时可额外注册 k8s 工具
//
// 进程级参数只来自 config.Config（由 internal/config 从 env 加载），本文件不再直接读 env
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"aruing/internal/agent"
	"aruing/internal/config"
	"aruing/internal/core"
	"aruing/internal/llm"
	"aruing/internal/tools"
	"aruing/internal/tools/k8s"
)

// 描述组装编排器所需的角色集合
// 各角色在有 LLM 配置时可替换为真实现；字段类型放宽为 Orchestrator 构造所需的最小能力
type orchestratorRoles struct {
	parser interface {
		Parse(context.Context, core.Run) (core.Query, error)
	}
	resolver agent.ResolveDriver
	planner  interface {
		Plan(context.Context, agent.PlanState) (agent.Plan, error)
	}
	verifier interface {
		Verify(context.Context, []core.Hypothesis, []core.Task, []core.Evidence) ([]core.Verdict, error)
	}
	reporter interface {
		Report(context.Context, core.Run, []core.Verdict, []core.Evidence) (core.Report, error)
	}
	registry *tools.Registry
}

// 构建全 fake 角色集合 + 工具注册表，所有角色共享一份 ID 表
// 假角色之间共享身份约定，避免 wiring 层散落两套编号规则
// toolsCfg 仅用于可选注册 k8s（KubectlPath）
func buildFakeRoles(factory *core.Factory, toolsCfg config.Tools) (orchestratorRoles, error) {
	ids := make(map[string]string, 7)
	for _, prefix := range []string{"query", "node", "target", "h", "t", "v", "rep"} {
		id, err := factory.NewID(prefix)
		if err != nil {
			return orchestratorRoles{}, fmt.Errorf("create %s ID: %w", prefix, err)
		}
		ids[prefix] = id
	}
	now := factory.Now()

	parser := agent.NewFakeParser(core.Query{
		ID:   ids["query"],
		Goal: "定位 demo-api 无法访问的原因",
		Nodes: []core.Node{{
			ID:   ids["node"],
			Type: "resource",
			Text: "demo-api",
		}},
		CreatedAt: now,
	})
	resolver := agent.NewFakeResolver([]core.Target{{
		ID:     ids["target"],
		NodeID: ids["node"],
		Type:   "k8s.resource",
		Attrs: map[string]string{
			"k8s.kind":      "Deployment",
			"k8s.namespace": "default",
			"k8s.name":      "demo-api",
		},
		CreatedAt: now,
	}})
	// 任务 Refs 只引用本模板内的猜想（及 Parser 节点若需要），不引用预生成 target ID
	// Target 编号由编排在定位阶段发放，假规划器无法在组装时预知
	planner := agent.NewFakePlanner(agent.Plan{
		Hypotheses: []core.Hypothesis{{
			ID:              ids["h"],
			Statement:       "后端 Pod 没有正常运行",
			Reason:          "服务不可访问时需要先确认后端是否就绪",
			ExpectedSignals: []string{"Pod 未就绪或反复重启"},
			CreatedAt:       now,
		}},
		Tasks: []core.Task{{
			ID:        ids["t"],
			Refs:      []string{ids["h"]},
			ToolName:  "fake.list_pods",
			Arguments: json.RawMessage(`{"namespace":"default","selector":"app=demo-api"}`),
			Purpose:   "检查后端 Pod 状态",
		}},
	})
	verifier := agent.NewFakeVerifier([]core.Verdict{{
		ID:           ids["v"],
		HypothesisID: ids["h"],
		Result:       core.VerdictSupported,
		Reason:       "Pod 处于 CrashLoopBackOff",
		CreatedAt:    now,
	}})
	reporter := agent.NewFakeReporter(core.Report{
		ID:      ids["rep"],
		Title:   "demo-api 诊断报告",
		Summary: "后端 Pod 未正常运行",
		Conclusions: []core.Conclusion{{
			HypothesisID: ids["h"],
			Result:       core.VerdictSupported,
			Reason:       "Pod 处于 CrashLoopBackOff",
		}},
		Suggestions: []string{"检查应用启动日志和容器配置"},
		CreatedAt:   now,
	})

	registry := tools.NewRegistry()
	if err := registry.Register(tools.NewFakeListPodsTool()); err != nil {
		return orchestratorRoles{}, fmt.Errorf("register fake tool: %w", err)
	}
	// 可选注册真实 k8s：默认闭环仍只调用 fake.list_pods；定位循环会真正用到 k8s
	if err := maybeRegisterK8s(registry, toolsCfg.KubectlPath); err != nil {
		return orchestratorRoles{}, err
	}

	return orchestratorRoles{
		parser:   parser,
		resolver: resolver,
		planner:  planner,
		verifier: verifier,
		reporter: reporter,
		registry: registry,
	}, nil
}

// 当 kubectl 可用时注册后端级 k8s 工具；不可用则跳过，保证无集群环境仍可跑假闭环
// 路径：显式 KubectlPath > PATH 中的 kubectl；不强制 CI 安装 kubectl
func maybeRegisterK8s(registry *tools.Registry, kubectlPath string) error {
	path := kubectlPath
	if path == "" {
		looked, err := exec.LookPath("kubectl")
		if err != nil {
			return nil
		}
		path = looked
	}
	tool, err := k8s.New(k8s.Config{
		KubectlPath:    path,
		DefaultTimeout: 30 * time.Second,
		MaxTimeout:     2 * time.Minute,
	})
	if err != nil {
		// 配置不合法时不拖垮假闭环：跳过注册，调用方仍可用 fake
		return nil
	}
	if err := registry.Register(tool); err != nil {
		return fmt.Errorf("register k8s tool: %w", err)
	}
	return nil
}

// 根据配置是否齐全决定各角色：
// LLM 三件套齐全则用 LLM 实现替换对应 fake；否则全 fake
// 保证 make test、CI 在无凭据时仍可运行
// 工具执行一律经只读 Policy，禁止写类 kubectl 进入 Evidence 链
// LLMResolver / LLMPlanner 的工具规格取自 Registry.Specs，与 Dispatcher 白名单同源
func newOrchestrator(factory *core.Factory, cfg config.Config) (*agent.Orchestrator, error) {
	roles, err := buildFakeRoles(factory, cfg.Tools)
	if err != nil {
		return nil, err
	}

	dispatcher := tools.NewDispatcher(roles.registry, tools.NewReadonlyPolicy())

	if !cfg.LLM.Ready() {
		return agent.NewOrchestrator(
			roles.parser,
			roles.resolver,
			roles.planner,
			dispatcher,
			roles.verifier,
			roles.reporter,
			factory,
		), nil
	}

	client, err := llm.NewClient(cfg.LLM.ToClientConfig())
	if err != nil {
		return nil, fmt.Errorf("build llm client: %w", err)
	}
	parser, err := agent.NewLLMParser(client, factory)
	if err != nil {
		return nil, fmt.Errorf("build llm parser: %w", err)
	}
	specs := roles.registry.Specs()
	resolver, err := agent.NewLLMResolver(client, specs)
	if err != nil {
		return nil, fmt.Errorf("build llm resolver: %w", err)
	}
	planner, err := agent.NewLLMPlanner(client, factory, specs)
	if err != nil {
		return nil, fmt.Errorf("build llm planner: %w", err)
	}
	verifier, err := agent.NewLLMVerifier(client, factory)
	if err != nil {
		return nil, fmt.Errorf("build llm verifier: %w", err)
	}
	reporter, err := agent.NewLLMReporter(client, factory)
	if err != nil {
		return nil, fmt.Errorf("build llm reporter: %w", err)
	}

	return agent.NewOrchestrator(
		parser,
		resolver,
		planner,
		dispatcher,
		verifier,
		reporter,
		factory,
	), nil
}
