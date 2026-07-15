// 假闭环组装模块负责为命令行入口连接当前所有确定性实现
//
// 这里集中创建假问题、目标、计划、判断和报告模板，并注册假工具
// 真实模块接入后应逐个替换对应依赖，不把组装细节移动到命令解析函数
package main

import (
	"encoding/json"
	"fmt"

	"aruing/internal/agent"
	"aruing/internal/core"
	"aruing/internal/tools"
)

// 使用统一元数据工厂组装可运行的完整假闭环
// 返回值只用于当前命令行最小闭环，不代表真实模块的最终配置方式
func newFakeOrchestrator(factory *core.Factory) (*agent.Orchestrator, error) {
	// 假模板仍使用真实格式编号，避免命令行阶段形成第二套身份约定
	ids := make(map[string]string, 7)
	for _, prefix := range []string{"query", "node", "target", "h", "t", "v", "rep"} {
		id, err := factory.NewID(prefix)
		if err != nil {
			return nil, fmt.Errorf("create %s ID: %w", prefix, err)
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

	// 工具注册失败属于启动配置错误，应在处理用户问题前直接返回
	registry := tools.NewRegistry()
	if err := registry.Register(tools.NewFakeListPodsTool()); err != nil {
		return nil, fmt.Errorf("register fake tool: %w", err)
	}

	return agent.NewOrchestrator(
		parser,
		resolver,
		planner,
		tools.NewDispatcher(registry),
		verifier,
		reporter,
		factory,
	), nil
}
