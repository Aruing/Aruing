// 收集重试只提交最后一次完整结果，失败片段和取消不得泄漏到下一次请求
package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

// 通过函数控制收集阶段的故障，不依赖真实网络与时钟
type collectingStreamStub struct {
	Client
	// 每次请求的增量及终止结果
	run func(context.Context, StreamRequest, func(string) error) (StreamSummary, error)
}

// 将一次同步请求交给测试场景
func (s collectingStreamStub) Stream(ctx context.Context, req StreamRequest, emit func(string) error) (StreamSummary, error) {
	return s.run(ctx, req, emit)
}

// 断流和空正文可以恢复，重试次数有界，其他错误不重放
func TestCollectingRetry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		partial string
		recover bool
		want    error
		calls   int
	}{
		{"incomplete recovers", fmt.Errorf("read: %w", ErrStreamIncomplete), `{"stale":1}`, true, nil, 2},
		{"empty recovers", ErrEmptyResponse, "", true, nil, 2},
		{"whitespace recovers", nil, " \n", true, nil, 2},
		{"incomplete exhausted", ErrStreamIncomplete, `{"stale":1}`, false, ErrStreamIncomplete, 3},
		{"empty exhausted", ErrEmptyResponse, "", false, ErrEmptyResponse, 3},
		{"invalid json", nil, `{"count":`, false, ErrJSONParse, 1},
		{"limit", ErrStreamLimit, "partial", false, ErrStreamLimit, 1},
		{"canceled", errors.Join(ErrStreamIncomplete, context.Canceled), "partial", false, context.Canceled, 1},
		{"deadline", errors.Join(ErrStreamIncomplete, context.DeadlineExceeded), "partial", false, context.DeadlineExceeded, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				collector, err := NewCollectingClient(collectingStreamStub{run: func(_ context.Context, _ StreamRequest, emit func(string) error) (StreamSummary, error) {
					calls++
					if tc.recover && calls > 1 {
						return StreamSummary{}, emit(`{"fresh":2}`)
					}
					if err := emit(tc.partial); err != nil {
						return StreamSummary{}, err
					}
					return StreamSummary{}, tc.failure
				}})
				if err != nil {
					t.Fatal(err)
				}
				out := map[string]int{"old": 9}
				err = collector.GenerateJSON(t.Context(), Request{}, &out)
				if !errors.Is(err, tc.want) || calls != tc.calls {
					t.Fatalf("error=%v calls=%d, want %v/%d", err, calls, tc.want, tc.calls)
				}
				if tc.want == nil {
					if len(out) != 1 || out["fresh"] != 2 {
						t.Fatalf("failed attempt leaked: %v", out)
					}
				} else if len(out) != 1 || out["old"] != 9 {
					t.Fatalf("target mutated: %v", out)
				}
			})
		})
	}
}

// 退避期间的截止时间必须终止等待，不发起下一次请求
func TestCollectingRetryDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		collector, err := NewCollectingClient(collectingStreamStub{run: func(context.Context, StreamRequest, func(string) error) (StreamSummary, error) {
			calls++
			return StreamSummary{}, ErrStreamIncomplete
		}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		response, err := collector.Generate(ctx, Request{})
		if !errors.Is(err, context.DeadlineExceeded) || calls != 1 || response.Content != "" {
			t.Fatalf("response=%+v error=%v calls=%d", response, err, calls)
		}
	})
}
