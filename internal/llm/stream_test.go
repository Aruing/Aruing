package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 写一个完整协议数据行并立即刷新，供可控上游使用
func writeStreamChunk(w http.ResponseWriter, content, finish string) {
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"index": 0, "delta": map[string]string{"content": content}, "finish_reason": finish,
	}}})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	w.(http.Flusher).Flush()
}

// 上游必须等消费者放行才发尾块，证明不是完整响应后模拟打字
func TestStreamIncremental(t *testing.T) {
	release := make(chan struct{})
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Stream        bool
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if !request.Stream || !request.StreamOptions.IncludeUsage {
			t.Error("stream options missing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamChunk(w, "你好", "")
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		writeStreamChunk(w, "🙂 \n```go\n x\n```", "stop")
		_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7}}\n\ndata: [DONE]\n\n")
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var text strings.Builder
	labeled := NewLabelingClient(c, "reporter")
	summary, err := labeled.(Streamer).Stream(ctx, StreamRequest{}, func(delta string) error {
		if text.Len() == 0 {
			close(release)
		}
		text.WriteString(delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "你好🙂 \n```go\n x\n```" {
		t.Fatalf("text = %q", text.String())
	}
	if summary.Usage == nil || summary.Usage.CompletionTokens != 7 {
		t.Fatalf("summary = %+v", summary)
	}
	totals := c.(StreamUsageTracker).StreamUsageSnapshot()["reporter"]
	if totals.Completed != 1 || totals.Attempts != 1 || totals.PromptTokens != 11 {
		t.Fatalf("totals = %+v", totals)
	}
	if c.(UsageTracker).UsageSnapshot()["reporter"].Calls != 1 {
		t.Fatal("legacy usage missing")
	}
	if _, ok := NewLabelingClient(fakeClient{}, "plain").(Streamer); ok {
		t.Fatal("false streaming capability")
	}
}

// 明确结束原因和协议终结标记缺一不可，空正文也不能冒充成功
func TestStreamTermination(t *testing.T) {
	for _, tc := range []struct {
		name, content, finish, tail string
		want                        error
	}{
		{"stop", "ok", "stop", "data: [DONE]\n\n", nil},
		{"bare eof", "partial", "", "", ErrStreamIncomplete},
		{"finish without done", "partial", "stop", "", ErrStreamIncomplete},
		{"done without finish", "partial", "", "data: [DONE]\n\n", ErrStreamIncomplete},
		{"length", "partial", "length", "data: [DONE]\n\n", ErrStreamIncomplete},
		{"filter", "partial", "content_filter", "data: [DONE]\n\n", ErrStreamIncomplete},
		{"tools", "", "tool_calls", "data: [DONE]\n\n", ErrStreamIncomplete},
		{"empty", " \n", "stop", "data: [DONE]\n\n", ErrEmptyResponse},
		{"invalid chunk", "partial", "", "data: not json\n\n", ErrStreamIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				writeStreamChunk(w, tc.content, tc.finish)
				_, _ = io.WriteString(w, tc.tail)
			})
			_, err := c.(Streamer).Stream(t.Context(), StreamRequest{}, func(string) error { return nil })
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if calls.Load() != 1 {
				t.Fatal("read failure retried")
			}
			totals := c.(StreamUsageTracker).StreamUsageSnapshot()["unknown"]
			if totals.UsageMissing != 1 {
				t.Fatalf("missing usage = %+v", totals)
			}
		})
	}
}

// 消费方停止或取消必须关闭上游连接，不能重新发送已经展示的内容
func TestStreamCancel(t *testing.T) {
	for _, mode := range []string{"consumer", "cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			closed := make(chan struct{})
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				writeStreamChunk(w, "first", "")
				<-r.Context().Done()
				close(closed)
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sentinel := errors.New("consumer stopped")
			want := sentinel
			if mode == "cancel" {
				want = context.Canceled
			}
			if mode == "timeout" {
				c.(*client).timeout = 50 * time.Millisecond
				want = context.DeadlineExceeded
			}
			_, err := c.(Streamer).Stream(ctx, StreamRequest{}, func(string) error {
				if mode == "cancel" {
					cancel()
					return nil
				}
				if mode == "timeout" {
					return nil
				}
				return sentinel
			})
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("upstream not closed")
			}
		})
	}
}

// 建流前的限流可重试，重试统计与正文完成统计分别计数
func TestStreamRetry(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "retry", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamChunk(w, "ok", "stop")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	_, err := c.(Streamer).Stream(t.Context(), StreamRequest{}, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	totals := c.(StreamUsageTracker).StreamUsageSnapshot()["unknown"]
	if totals.Attempts != 2 || totals.Failed != 1 || totals.Completed != 1 {
		t.Fatalf("totals = %+v", totals)
	}
}

// 解析失败不会部分修改已有对象，包括其嵌套映射
func TestCollectingJSON(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		want          error
	}{
		{"valid", `{"values":{"new":2},"count":3}`, nil},
		{"type error", `{"values":{"new":2},"count":"bad"}`, ErrJSONParse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if request["response_format"] == nil {
					t.Error("missing json mode")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				writeStreamChunk(w, tc.content, "stop")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			})
			collector, err := NewCollectingClient(c)
			if err != nil {
				t.Fatal(err)
			}
			out := struct {
				Values map[string]int
				Count  int
			}{Values: map[string]int{"old": 1}, Count: 9}
			err = collector.GenerateJSON(t.Context(), Request{}, &out)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v", err)
			}
			if tc.want != nil {
				if out.Count != 9 || len(out.Values) != 1 || out.Values["old"] != 1 {
					t.Fatalf("mutated target: %+v", out)
				}
			} else if out.Count != 3 || out.Values["new"] != 2 {
				t.Fatalf("result = %+v", out)
			}
		})
	}
	if _, err := NewCollectingClient(fakeClient{}); err == nil {
		t.Fatal("non-stream client accepted")
	}
}

// 在任意字节处拆分协议行（含多字节字符），并合并多事件，正文不得丢失或重复
func TestStreamFragments(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		wire := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"你好🙂\"}}]}\r\n\r\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\" tail\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
		for i := 0; i < len(wire); i += 7 {
			_, _ = io.WriteString(w, wire[i:min(i+7, len(wire))])
			w.(http.Flusher).Flush()
		}
	})
	collector, err := NewCollectingClient(c)
	if err != nil {
		t.Fatal(err)
	}
	response, err := collector.Generate(t.Context(), Request{})
	if err != nil || response.Content != "你好🙂 tail" {
		t.Fatalf("response=%+v error=%v", response, err)
	}
}

// 用量包是累计快照，重复包不重复计费；失败后的已知用量仍保留
func TestStreamUsageFailure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamChunk(w, "ok", "stop")
		for range 2 {
			_, _ = io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":3}}\n\n")
		}
	})
	_, err := c.(Streamer).Stream(t.Context(), StreamRequest{Request: Request{Label: "custom"}}, func(string) error { return nil })
	if !errors.Is(err, ErrStreamIncomplete) {
		t.Fatal(err)
	}
	tracker := c.(StreamUsageTracker)
	snapshot := tracker.StreamUsageSnapshot()
	totals := snapshot["custom"]
	if totals.Failed != 1 || totals.UsageMissing != 0 || totals.PromptTokens != 5 || totals.CompletionTokens != 3 {
		t.Fatalf("totals = %+v", totals)
	}
	snapshot["custom"] = StreamTotals{}
	if tracker.StreamUsageSnapshot()["custom"].Failed != 1 {
		t.Fatal("snapshot aliases internal state")
	}
	if c.(UsageTracker).UsageSnapshot()["custom"].Calls != 0 {
		t.Fatal("failed call counted as success")
	}
}
