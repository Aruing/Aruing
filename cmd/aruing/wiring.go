// 命令行入口的依赖组装模块
//
// 这一层负责把"用哪些角色实例组装 Orchestrator / Session"集中收敛：
// - run：无 LLM 配置时全 fake（CI、make test）；有 LLM 时五角色换真实现
// - chat：必须 LLM 齐全，组装 TowerResponder + session.Service；与 Orchestrator 共用 Dispatcher
// - 工具始终经 Dispatcher + 只读/诊断 Policy；kubectl 可用时可额外注册 k8s 工具
//
// 进程级参数只来自 config.Config（由 internal/config 从 env 加载），本文件不再直接读 env
package main

import (
	"context"
	"encoding/json"
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
// fake 路径首轮即出 supported 退出，不受此值影响
const productionInvestigateMaxRounds = 3

// chat 要求配置 LLM 时的明确错误文案（含 env 名，便于用户对照 .env.example）
const errChatRequiresLLM = "chat requires LLM configuration: set ARUING_LLM_BASE_URL, ARUING_LLM_API_KEY, and ARUING_LLM_MODEL"

// 组装编排器所需的角色集合（不含工具图）
// 各角色在有 LLM 配置时可替换为真实现；字段类型放宽为 Orchestrator 构造所需的最小能力
type orchestratorRoles struct {
	// 问题解析角色，产出 Query 线索
	parser interface {
		Parse(context.Context, core.Run) (core.Query, error)
	}
	// 定位驱动，提议 call_tool / submit_targets / fail
	resolver agent.ResolveDriver
	// 规划角色，产出猜想与取证任务
	planner interface {
		Plan(context.Context, agent.PlanState) (agent.Plan, error)
	}
	// 验证角色，基于证据产出 Verdict
	verifier interface {
		Verify(context.Context, core.Query, []core.Hypothesis, []core.Task, []core.Evidence) ([]core.Verdict, error)
	}
	// 报告角色，整理 Report
	reporter interface {
		Report(context.Context, core.Run, []core.Verdict, []core.Evidence) (core.Report, error)
	}
}

// 工具注册表与调度器；Tower 与 Orchestrator 必须共用同一 Dispatcher
type tooling struct {
	// 工具注册表，Dispatcher 与 LLM 角色 specs 同源
	registry *tools.Registry
	// 经 Policy 授权的调度器
	dispatcher *tools.Dispatcher
	// 是否注册了真实 k8s 工具；用于开启集群侦察
	reconEnabled bool
}

// 构建全 fake 角色集合，所有角色共享一份 ID 表
// 假角色之间共享身份约定，避免 wiring 层散落两套编号规则
func buildFakeRoles(factory *core.Factory) (orchestratorRoles, error) {
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

	return orchestratorRoles{
		parser:   parser,
		resolver: resolver,
		planner:  planner,
		verifier: verifier,
		reporter: reporter,
	}, nil
}

// 组装工具注册表与 Dispatcher；fake.list_pods 始终注册，kubectl 可用时可选 k8s
// Tower 与 Orchestrator 应共用本函数返回的同一 dispatcher 实例
func buildTooling(toolsCfg config.Tools) (tooling, error) {
	registry := tools.NewRegistry()
	if err := registry.Register(tools.NewFakeListPodsTool()); err != nil {
		return tooling{}, fmt.Errorf("register fake tool: %w", err)
	}
	k8sRegistered, err := maybeRegisterK8s(registry, toolsCfg.KubectlPath)
	if err != nil {
		return tooling{}, err
	}

	policy := tools.NewReadonlyPolicy()
	if toolsCfg.AllowDiagnosticExec {
		policy = tools.NewDiagnosticPolicy()
	}
	return tooling{
		registry:     registry,
		dispatcher:   tools.NewDispatcher(registry, policy),
		reconEnabled: k8sRegistered,
	}, nil
}

// 当 kubectl 可用时注册后端级 k8s 工具；不可用则跳过，保证无集群环境仍可跑假闭环
// 返回是否成功注册，用于决定是否开启集群侦察
// 路径：显式 KubectlPath > PATH 中的 kubectl；不强制 CI 安装 kubectl
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
		// 配置不合法时不拖垮假闭环：跳过注册，调用方仍可用 fake
		return false, nil
	}
	if err := registry.Register(tool); err != nil {
		return false, fmt.Errorf("register k8s tool: %w", err)
	}
	return true, nil
}

// 根据配置是否齐全决定各角色：
// LLM 三件套齐全则用 LLM 实现替换对应 fake；否则全 fake
// 保证 make test、CI 在无凭据时仍可运行
// 工具执行一律经 Policy，禁止写类 kubectl 进入 Evidence 链
// LLMResolver / LLMPlanner 的工具规格取自 Registry.Specs，与 Dispatcher 白名单同源
func newOrchestrator(factory *core.Factory, cfg config.Config, progress io.Writer) (*agent.Orchestrator, error) {
	roles, err := buildFakeRoles(factory)
	if err != nil {
		return nil, err
	}
	toolsGraph, err := buildTooling(cfg.Tools)
	if err != nil {
		return nil, err
	}

	if !cfg.LLM.Ready() {
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

	client, err := llm.NewClient(cfg.LLM.ToClientConfig())
	if err != nil {
		return nil, fmt.Errorf("build llm client: %w", err)
	}
	roles, err = buildLLMRoles(client, factory, toolsGraph.registry.Specs())
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

// 组装 chat 用的 Session 栈：MemoryStore + TowerResponder + Orchestrator 共用 Dispatcher
// 无 LLM 时硬失败，不用 FakeTower 冒充产品路径
func newSessionStack(factory *core.Factory, cfg config.Config, progress io.Writer) (*session.Service, error) {
	if !cfg.LLM.Ready() {
		return nil, fmt.Errorf("%s", errChatRequiresLLM)
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

	tower, err := agent.NewTowerResponder(
		client,
		factory,
		orch,
		toolsGraph.dispatcher,
		toolsGraph.registry.Specs(),
	)
	if err != nil {
		return nil, fmt.Errorf("build tower: %w", err)
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
