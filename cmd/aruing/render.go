package main

import (
	"fmt"
	"strings"

	"aruing/internal/core"
)

// 把结构化报告与调查证据渲染成标记文本
//
// 纯展示函数：不调模型、不查集群，只依赖报告与已登记证据
// 结论按已支持、已排除、证据不足分组，证据编号原样列出保持可追溯
// 末尾「证据明细」按调查时序列出每条证据的命令与摘要，便于回溯「查了什么、结果如何」
func renderMarkdown(report core.Report, evidence []core.Evidence) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s\n\n", nonEmpty(report.Title, "诊断报告"))
	fmt.Fprintf(&b, "## 摘要\n\n%s\n\n", nonEmpty(report.Summary, "（无摘要）"))

	if len(report.Conclusions) == 0 {
		b.WriteString("## 结论\n\n暂无结论\n\n")
	} else {
		renderConclusionGroups(&b, report.Conclusions)
	}

	if len(report.Suggestions) > 0 {
		b.WriteString("## 建议\n\n")
		for i, s := range report.Suggestions {
			fmt.Fprintf(&b, "%d. %s\n", i+1, s)
		}
		b.WriteString("\n")
	}

	if len(evidence) > 0 {
		renderEvidenceDetail(&b, evidence)
	}

	b.WriteString("---\n")
	fmt.Fprintf(&b, "运行 `%s` · 报告 `%s` · 生成于 %s\n",
		report.RunID, report.ID, report.CreatedAt.Format("2006-01-02 15:04:05"))

	return b.String()
}

// 渲染证据明细表：证据编号 / 命令视图（合成失败证据用 — 占位）/ 摘要
func renderEvidenceDetail(b *strings.Builder, evidence []core.Evidence) {
	b.WriteString("## 证据明细\n\n")
	b.WriteString("| 证据 | 命令 | 摘要 |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, e := range evidence {
		cmd := "—"
		if strings.TrimSpace(e.CommandView) != "" {
			cmd = "`" + e.CommandView + "`"
		}
		summary := nonEmpty(e.Summary, "—")
		if e.Error != "" {
			summary = "❌ " + summary
		}
		fmt.Fprintf(b, "| `%s` | %s | %s |\n", e.ID, cmd, summary)
	}
	b.WriteString("\n")
}

// 按已支持、已排除、证据不足分段输出结论，无对应结论的段省略
func renderConclusionGroups(b *strings.Builder, conclusions []core.Conclusion) {
	groups := []struct {
		result core.VerdictResult
		title  string
	}{
		{core.VerdictSupported, "已支持"},
		{core.VerdictRefuted, "已排除"},
		{core.VerdictInsufficient, "证据不足"},
	}
	first := true
	for _, g := range groups {
		items := pickConclusions(conclusions, g.result)
		if len(items) == 0 {
			continue
		}
		if first {
			b.WriteString("## 结论\n\n")
			first = false
		}
		fmt.Fprintf(b, "### %s\n\n", g.title)
		for _, c := range items {
			fmt.Fprintf(b, "- %s\n", nonEmpty(c.Reason, "（无说明）"))
			if len(c.EvidenceIDs) > 0 {
				fmt.Fprintf(b, "  证据：%s\n", joinIDs(c.EvidenceIDs))
			}
		}
		b.WriteString("\n")
	}
}

// 按结果筛选结论，保持原顺序
func pickConclusions(conclusions []core.Conclusion, result core.VerdictResult) []core.Conclusion {
	var out []core.Conclusion
	for _, c := range conclusions {
		if c.Result == result {
			out = append(out, c)
		}
	}
	return out
}

// 把证据编号列表渲染成反引号包裹的空格分隔串
func joinIDs(ids []string) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = "`" + id + "`"
	}
	return strings.Join(quoted, " ")
}

// 空串回退到占位文案
func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
