package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/session"
)

// 索引卡不带 raw；编号（地址）完整保留；文本字段钳到 c_max
func TestBuildMemoryCards(t *testing.T) {
	longSummary := strings.Repeat("细节", 2_000) + " 结尾编号 e_999"
	records := []session.DiagnosticRecord{{
		RunID:    "run_1",
		Question: "demo-api 挂了",
		Report: core.Report{
			Title:   "镜像拉取失败",
			Summary: longSummary,
			Conclusions: []core.Conclusion{{
				Result:      core.VerdictSupported,
				Reason:      "事件含 Failed to pull image " + strings.Repeat("x", 4_000),
				EvidenceIDs: []string{"e_1", "e_2"},
			}},
			Suggestions: []string{"检查镜像仓库凭证"},
		},
		Evidence: []core.Evidence{{
			ID:          "e_1",
			ToolName:    "k8s",
			Summary:     "events: ImagePullBackOff",
			CommandView: "kubectl describe pod demo-api",
			Raw:         json.RawMessage(`{"stdout":"giant output"}`),
		}},
	}}

	cards := buildMemoryCards(records)
	if len(cards) != 1 {
		t.Fatalf("cards len: %d", len(cards))
	}
	c := cards[0]
	if c.RunID != "run_1" || c.Title != "镜像拉取失败" {
		t.Fatalf("card head: %+v", c)
	}
	// 文本字段钳制：摘要与结论理由超 c_max 被截断，且截断带标记（C1 保地址）
	if estimateTokens(c.Summary) > cardMaxTokens+40 {
		t.Fatalf("summary not clamped: %d tokens", estimateTokens(c.Summary))
	}
	if !strings.Contains(c.Summary, "truncated") || !strings.Contains(c.Summary, "e_999") {
		t.Fatalf("clamped summary must keep truncation marker and addrs: %q", c.Summary)
	}
	if !strings.Contains(c.Conclusions[0].Reason, "truncated") {
		t.Fatalf("conclusion reason must be clamped: %q", c.Conclusions[0].Reason)
	}
	// 地址不钳：证据编号原样保留
	if got := c.Conclusions[0].EvidenceIDs; len(got) != 2 || got[0] != "e_1" || got[1] != "e_2" {
		t.Fatalf("evidence ids must survive: %v", got)
	}
	// 卡面不带 raw（裁决口径）
	if len(c.Evidence) != 1 || len(c.Evidence[0].Raw) != 0 || c.Evidence[0].RawTruncated {
		t.Fatalf("cards must not carry raw: %+v", c.Evidence[0])
	}
	if c.Evidence[0].ID != "e_1" || c.Evidence[0].Summary != "events: ImagePullBackOff" {
		t.Fatalf("evidence card fields: %+v", c.Evidence[0])
	}
}

// 空记录返回空切片（非 nil），载荷序列化为 []
func TestBuildMemoryCardsEmpty(t *testing.T) {
	if cards := buildMemoryCards(nil); cards == nil || len(cards) != 0 {
		t.Fatalf("want non-nil empty: %v", cards)
	}
}
