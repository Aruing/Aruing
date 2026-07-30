package session

import (
	"strings"
	"testing"

	"aruing/internal/core"
)

func TestFormatDiagnosticReplyRich(t *testing.T) {
	got := formatDiagnosticReply(core.Report{
		Title:   "demo-api 不可用",
		Summary: "镜像拉取失败导致 CrashLoop",
		Conclusions: []core.Conclusion{
			{HypothesisID: "h_1", Result: core.VerdictSupported, Reason: "事件中有 ImagePullBackOff"},
			{HypothesisID: "h_2", Result: core.VerdictRefuted, Reason: "节点资源充足"},
		},
		Suggestions: []string{"检查镜像仓库凭证", "确认镜像 tag 存在"},
	})
	for _, want := range []string{
		"demo-api 不可用",
		"镜像拉取失败",
		"结论：",
		"ImagePullBackOff",
		"建议：",
		"镜像仓库凭证",
		"镜像 tag",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestFormatDiagnosticReplyMinimal(t *testing.T) {
	if got := formatDiagnosticReply(core.Report{}); got != "诊断已完成" {
		t.Fatalf("got %q", got)
	}
	if got := formatDiagnosticReply(core.Report{Summary: "仅摘要"}); got != "仅摘要" {
		t.Fatalf("got %q", got)
	}
}
