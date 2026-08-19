package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Aruing/Aruing/internal/core"
)

// 完整报告应渲染出标题、摘要、结论分组、证据编号和建议
func TestRenderMarkdownFull(t *testing.T) {
	t.Parallel()

	report := core.Report{
		ID:      "rep_1",
		RunID:   "run_1",
		Title:   "demo-api 诊断报告",
		Summary: "后端 Pod 未正常运行",
		Conclusions: []core.Conclusion{
			{Result: core.VerdictSupported, Reason: "Pod 处于 CrashLoopBackOff", EvidenceIDs: []string{"e_1", "e_2"}},
			{Result: core.VerdictRefuted, Reason: "网络策略正常", EvidenceIDs: []string{"e_3"}},
			{Result: core.VerdictInsufficient, Reason: "日志信息不足", EvidenceIDs: []string{"e_4"}},
		},
		Suggestions: []string{"检查启动日志", "检查资源配置"},
		CreatedAt:   time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC),
	}

	md := renderMarkdown(report, nil)

	for _, want := range []string{
		"# demo-api 诊断报告",
		"后端 Pod 未正常运行",
		"### 已支持",
		"Pod 处于 CrashLoopBackOff",
		"`e_1` `e_2`",
		"### 已排除",
		"### 证据不足",
		"## 建议",
		"1. 检查启动日志",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in output:\n%s", want, md)
		}
	}
}

// 空结论/空建议/空标题应回退占位，且不渲染空段
func TestRenderMarkdownEmpty(t *testing.T) {
	t.Parallel()

	md := renderMarkdown(core.Report{
		ID:        "rep_empty",
		RunID:     "run_empty",
		CreatedAt: time.Now(),
	}, nil)

	if !strings.Contains(md, "# 诊断报告") || !strings.Contains(md, "（无摘要）") {
		t.Errorf("empty title/summary should fall back:\n%s", md)
	}
	if !strings.Contains(md, "暂无结论") {
		t.Errorf("want conclusions placeholder:\n%s", md)
	}
	if strings.Contains(md, "### 已支持") || strings.Contains(md, "## 建议") {
		t.Errorf("empty groups/suggestions should be omitted:\n%s", md)
	}
}

// 证据明细应按调查时序列出命令与摘要
func TestRenderMarkdownEvidenceDetail(t *testing.T) {
	t.Parallel()

	report := core.Report{
		ID:        "rep_e",
		RunID:     "run_e",
		Title:     "T",
		Summary:   "S",
		CreatedAt: time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC),
	}
	evidence := []core.Evidence{
		{ID: "e_a", ToolName: "k8s", CommandView: "kubectl get ingress -n swanlab -o json", Summary: "kubectl 执行完成，exitCode=0"},
		{ID: "e_b", ToolName: "k8s", Summary: "工具执行失败", Error: "denied by policy"},
	}

	md := renderMarkdown(report, evidence)

	for _, want := range []string{
		"## 证据明细",
		"`e_a`",
		"`kubectl get ingress -n swanlab -o json`",
		"`e_b`",
		"❌ 工具执行失败",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in output:\n%s", want, md)
		}
	}
}
