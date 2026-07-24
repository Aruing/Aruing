package main

import (
	"fmt"
	"strings"

	"aruing/internal/core"
)

// 把结构化报告渲染成 Markdown 文本
//
// 纯展示函数：不调模型、不查集群，只依赖 core.Report 字段
// 结论按 supported / refuted / insufficient 分组，证据编号原样列出保持可追溯
func renderMarkdown(report core.Report) string {
	var b strings.Builder

	b.WriteString("# " + nonEmpty(report.Title, "诊断报告") + "\n\n")

	b.WriteString("## 摘要\n\n")
	b.WriteString(nonEmpty(report.Summary, "（无摘要）") + "\n\n")

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

	b.WriteString("---\n")
	fmt.Fprintf(&b, "运行 `%s` · 报告 `%s` · 生成于 %s\n",
		report.RunID, report.ID, report.CreatedAt.Format("2006-01-02 15:04:05"))

	return b.String()
}

// 按 supported / refuted / insufficient 分段输出结论，无对应结论的段省略
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
		b.WriteString("### " + g.title + "\n\n")
		for _, c := range items {
			b.WriteString("- " + nonEmpty(c.Reason, "（无说明）") + "\n")
			if len(c.EvidenceIDs) > 0 {
				b.WriteString("  证据：" + joinIDs(c.EvidenceIDs) + "\n")
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
