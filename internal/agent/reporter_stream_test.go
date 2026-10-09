package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Aruing/Aruing/internal/llm"
)

// 流式运输不能绕过报告的判决与证据引用校验，断流不能产生正式报告
func TestReporterStream(t *testing.T) {
	for _, mode := range []string{"valid", "invalid evidence", "interrupted", "interrupted recovers", "empty recovers"} {
		t.Run(mode, func(t *testing.T) {
			body := `{"title":"诊断报告","summary":"观察到异常","conclusions":[{"hypothesis_id":"h_1","result":"supported","reason":"证据支持","evidence_ids":["e_1"]}],"suggestions":[]}`
			if mode == "invalid evidence" {
				body = strings.Replace(body, "e_1", "unknown", 1)
			}
			var calls atomic.Int32
			client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
				attempt := calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				if mode == "empty recovers" && attempt == 1 {
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				for _, part := range []string{body[:len(body)/2], body[len(body)/2:]} {
					raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": part}}}})
					_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
					w.(http.Flusher).Flush()
				}
				if mode != "interrupted" && (mode != "interrupted recovers" || attempt != 1) {
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}
			})
			collector, err := llm.NewCollectingClient(llm.NewLabelingClient(client, "reporter"))
			if err != nil {
				t.Fatal(err)
			}
			reporter, err := NewLLMReporter(collector, newTestFactory(t))
			if err != nil {
				t.Fatal(err)
			}
			report, err := reporter.Report(t.Context(), testReportRun(), testReportVerdicts(), testReportEvidence())
			wantCalls := int32(1)
			wantFailed := 0
			switch mode {
			case "interrupted":
				wantCalls = 3
				wantFailed = 3
			case "invalid evidence":
				wantCalls = int32(maxReportAttempts)
			case "interrupted recovers", "empty recovers":
				wantCalls = 2
				wantFailed = 1
			}
			if calls.Load() != wantCalls {
				t.Fatalf("calls=%d, want %d", calls.Load(), wantCalls)
			}
			totals := client.(llm.StreamUsageTracker).StreamUsageSnapshot()["reporter"]
			if totals.Attempts != int(wantCalls) || totals.Failed != wantFailed || totals.Completed != int(wantCalls)-wantFailed {
				t.Fatalf("totals=%+v", totals)
			}
			switch mode {
			case "valid", "interrupted recovers", "empty recovers":
				if err != nil {
					t.Fatal(err)
				}
				if report.ID == "" || report.RunID != "run_1" || report.Conclusions[0].EvidenceIDs[0] != "e_1" {
					t.Fatalf("report = %+v", report)
				}
			case "invalid evidence":
				if !errors.Is(err, ErrLLMOutputInconsistent) || report.ID != "" {
					t.Fatalf("report=%+v error=%v", report, err)
				}
			case "interrupted":
				if !errors.Is(err, llm.ErrStreamIncomplete) || report.ID != "" {
					t.Fatalf("report=%+v error=%v", report, err)
				}
			}
		})
	}
}
