// 命令行入口的依赖组装模块
//
// 这一层负责把"用哪些角色实例组装 Orchestrator"集中收敛：
// - 在没有 LLM 配置时，所有角色使用 fake，假闭环继续可跑、可测（CI、make test 默认走这条路径）
// - 在 LLM 配置齐全时，把 parser 换成 LLMParser，其它角色仍走 fake，按计划逐个替换
//
// 不在本文件引 internal/config（计划放在工作单元 #8），当下只读 env，配置收敛留给后续阶段
package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"aruing/internal/agent"
	"aruing/internal/core"
	"aruing/internal/llm"
	"aruing/internal/tools"
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

	return orchestratorRoles{
		parser:   parser,
		resolver: resolver,
		planner:  planner,
		verifier: verifier,
		reporter: reporter,
		registry: registry,
	}, nil
}

// 根据 LLM 配置是否齐全决定 parser 角色：
// 配置齐全则用 LLMParser 替换 fake parser，其它角色继续走 fake
// 配置不全直接退化到全 fake，保证 make test、CI 在无凭据时仍可运行
func newOrchestrator(factory *core.Factory, llmCfg llm.Config) (*agent.Orchestrator, error) {
	roles, err := buildFakeRoles(factory)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(llmCfg.BaseURL) == "" ||
		strings.TrimSpace(llmCfg.APIKey) == "" ||
		strings.TrimSpace(llmCfg.Model) == "" {
		return agent.NewOrchestrator(
			roles.parser,
			roles.resolver,
			roles.planner,
			tools.NewDispatcher(roles.registry),
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
		tools.NewDispatcher(roles.registry),
		roles.verifier,
		roles.reporter,
		factory,
	), nil
}
