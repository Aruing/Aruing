package main

import (
	"strings"
	"testing"
	"time"

	"aruing/internal/core"
)

// 完整报告应渲染出标题、摘要、三段结论、证据编号和建议
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

	md := renderMarkdown(report)

	for _, want := range []string{
		"# demo-api 诊断报告",
		"## 摘要",
		"后端 Pod 未正常运行",
		"### 已支持",
		"Pod 处于 CrashLoopBackOff",
		"`e_1` `e_2`",
		"### 已排除",
		"网络策略正常",
		"### 证据不足",
		"## 建议",
		"1. 检查启动日志",
		"2. 检查资源配置",
		"运行 `run_1`",
		"报告 `rep_1`",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in output:\n%s", want, md)
		}
	}
}

// 无结论时应输出占位，不渲染空段标题
func TestRenderMarkdownNoConclusions(t *testing.T) {
	t.Parallel()

	md := renderMarkdown(core.Report{
		ID:        "rep_2",
		RunID:     "run_2",
		Title:     "空报告",
		Summary:   "无内容",
		CreatedAt: time.Now(),
	})

	if !strings.Contains(md, "暂无结论") {
		t.Errorf("want placeholder, got:\n%s", md)
	}
	if strings.Contains(md, "### 已支持") {
		t.Errorf("empty group should be omitted:\n%s", md)
	}
}

// 只有 supported 结论时，其他段应省略
func TestRenderMarkdownPartialGroups(t *testing.T) {
	t.Parallel()

	md := renderMarkdown(core.Report{
		ID:      "rep_3",
		RunID:   "run_3",
		Title:   "T",
		Summary: "S",
		Conclusions: []core.Conclusion{
			{Result: core.VerdictSupported, Reason: "ok", EvidenceIDs: nil},
		},
		CreatedAt: time.Now(),
	})

	if !strings.Contains(md, "### 已支持") {
		t.Errorf("supported group missing:\n%s", md)
	}
	if strings.Contains(md, "### 已排除") || strings.Contains(md, "### 证据不足") {
		t.Errorf("empty groups should be omitted:\n%s", md)
	}
}

// 无建议时应省略建议段
func TestRenderMarkdownNoSuggestions(t *testing.T) {
	t.Parallel()

	md := renderMarkdown(core.Report{
		ID:        "rep_4",
		RunID:     "run_4",
		Title:     "T",
		Summary:   "S",
		CreatedAt: time.Now(),
	})

	if strings.Contains(md, "## 建议") {
		t.Errorf("suggestions section should be omitted when empty:\n%s", md)
	}
}

// 空标题与空摘要应回退到占位文案
func TestRenderMarkdownEmptyFields(t *testing.T) {
	t.Parallel()

	md := renderMarkdown(core.Report{
		ID:        "rep_5",
		RunID:     "run_5",
		CreatedAt: time.Now(),
	})

	if !strings.Contains(md, "# 诊断报告") {
		t.Errorf("empty title should fall back:\n%s", md)
	}
	if !strings.Contains(md, "（无摘要）") {
		t.Errorf("empty summary should fall back:\n%s", md)
	}
}
