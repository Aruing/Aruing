// 命令行入口的依赖组装模块
//
// 这一层负责把"用哪些角色实例组装 Orchestrator"集中收敛：
// - 在没有 LLM 配置时，所有角色使用 fake，假闭环继续可跑、可测（CI、make test 默认走这条路径）
// - 在 LLM 配置齐全时，把 parser 换成 LLMParser，其它角色仍走 fake，按计划逐个替换
// - 工具始终经 Dispatcher + 只读 Policy；kubectl 可用时可额外注册 k8s 工具（本阶段假 Planner 仍只调 fake.list_pods）
//
// 不在本文件引 internal/config（计划放在工作单元 #8），当下只读 env，配置收敛留给后续阶段
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"aruing/internal/agent"
	"aruing/internal/core"
	"aruing/internal/llm"
	"aruing/internal/tools"
	"aruing/internal/tools/k8s"
)

// 描述组装编排器所需的角色集合
// fake 与 real 角色都满足同一接口，调用方按是否提供 LLM 配置自由替换 parser
type orchestratorRoles struct {
	parser   *agent.FakeParser
	resolver *agent.FakeResolver
	planner  *agent.FakePlanner
	verifier *agent.FakeVerifier
	reporter *agent.FakeReporter
	registry *tools.Registry
}

// 构建全 fake 角色集合 + 工具注册表，所有角色共享一份 ID 表
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
			Refs:      []string{ids["target"], ids["h"]},
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
	// 可选注册真实 k8s：默认闭环仍只调用 fake.list_pods；#4b 定位循环会真正用到 k8s
	if err := maybeRegisterK8s(registry); err != nil {
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
// 路径：ARUING_KUBECTL_PATH > PATH 中的 kubectl；不强制 CI 安装 kubectl
func maybeRegisterK8s(registry *tools.Registry) error {
	path := strings.TrimSpace(os.Getenv("ARUING_KUBECTL_PATH"))
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
		// 配置不合法时不拖垮假闭环：记录为可跳过，调用方仍可用 fake
		return nil
	}
	if err := registry.Register(tool); err != nil {
		return fmt.Errorf("register k8s tool: %w", err)
	}
	return nil
}

// 根据 LLM 配置是否齐全决定 parser 角色：
// 配置齐全则用 LLMParser 替换 fake parser，其它角色继续走 fake
// 配置不全直接退化到全 fake，保证 make test、CI 在无凭据时仍可运行
// 工具执行一律经只读 Policy，禁止写类 kubectl 进入 Evidence 链
func newOrchestrator(factory *core.Factory, llmCfg llm.Config) (*agent.Orchestrator, error) {
	roles, err := buildFakeRoles(factory)
	if err != nil {
		return nil, err
	}

	dispatcher := tools.NewDispatcher(roles.registry, tools.NewReadonlyPolicy())

	if strings.TrimSpace(llmCfg.BaseURL) == "" ||
		strings.TrimSpace(llmCfg.APIKey) == "" ||
		strings.TrimSpace(llmCfg.Model) == "" {
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

	client, err := llm.NewClient(llmCfg)
	if err != nil {
		return nil, fmt.Errorf("build llm client: %w", err)
	}
	parser, err := agent.NewLLMParser(client, factory)
	if err != nil {
		return nil, fmt.Errorf("build llm parser: %w", err)
	}

	return agent.NewOrchestrator(
		parser,
		roles.resolver,
		roles.planner,
		dispatcher,
		roles.verifier,
		roles.reporter,
		factory,
	), nil
}
