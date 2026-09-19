package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Aruing/Aruing/internal/core"
	"github.com/Aruing/Aruing/internal/llm"
	"github.com/Aruing/Aruing/internal/session"
	"github.com/Aruing/Aruing/internal/store"
)

// 识别最终回复请求并保留请求体，供同包 Tower 测试复用
func isTowerReplyRequest(t *testing.T, r *http.Request) bool {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	for _, message := range req.Messages {
		if message.Role == "system" && strings.HasPrefix(message.Content, "# Tower Reply") {
			return true
		}
	}
	return false
}

// 只实现阻塞客户端，用于证明可选流式能力不会被静默伪装
type nonStreamingTowerClient struct{}

func (nonStreamingTowerClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, nil
}

func (nonStreamingTowerClient) GenerateJSON(context.Context, llm.Request, any) error {
	return nil
}

// 结构化动作先完整校验；只有 reply 路径才调用第二次纯文本流，内部 JSON 不进入 emit
func TestTowerRespondStreamReply(t *testing.T) {
	decision := `{"action":"reply","content":"草稿答案"}`
	var requests int
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if isTowerReplyRequest(t, r) {
			w.Header().Set("Content-Type", "text/event-stream")
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": "最终答案"}, "finish_reason": "stop"}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+strconv.Quote(decision)+`},"finish_reason":"stop"}]}`)
	})
	tower, err := NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, store.NewMemoryRunLedger(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var chunks []string
	out, err := tower.RespondStream(t.Context(), session.RespondInput{SessionID: "s", UserText: "问候"}, func(delta string) error { chunks = append(chunks, delta); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "最终答案" || strings.Join(chunks, "") != out.Content {
		t.Fatalf("out=%q chunks=%q", out.Content, chunks)
	}
	if requests != 2 {
		t.Fatalf("requests=%d, want 2", requests)
	}
}

// 底层没有 Streamer 时明确失败，不能返回整块正文却不调用消费者
func TestTowerRespondStreamUnsupported(t *testing.T) {
	tower, err := NewTowerResponder(nonStreamingTowerClient{}, newTestFactory(t), &fakeRunExecutor{}, store.NewMemoryRunLedger(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	emitted := false
	_, err = tower.RespondStream(t.Context(), session.RespondInput{SessionID: "s", UserText: "问候"}, func(string) error {
		emitted = true
		return nil
	})
	if !errors.Is(err, ErrTowerStreamingUnsupported) || emitted {
		t.Fatalf("error=%v emitted=%v", err, emitted)
	}
}

// 流式消费者失败后不回退到阻塞接口或重新执行工具
func TestTowerRespondStreamConsumerFailure(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isTowerReplyRequest(t, r) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"action\":\"reply\",\"content\":\"draft\"}"},"finish_reason":"stop"}]}`)
	})
	tower, err := NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, store.NewMemoryRunLedger(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("stop")
	_, err = tower.RespondStream(t.Context(), session.RespondInput{SessionID: "s", UserText: "问候"}, func(string) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v", err)
	}
}

// 网络断流保留已展示片段但整轮明确失败，不返回可落库的完整结果
func TestTowerRespondStreamInterrupted(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, r *http.Request) {
		if isTowerReplyRequest(t, r) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
			return
		}
		writeChatCompletion(w, `{"action":"reply","content":"draft"}`)
	})
	tower, err := NewTowerResponder(client, newTestFactory(t), &fakeRunExecutor{}, store.NewMemoryRunLedger(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	out, err := tower.RespondStream(t.Context(), session.RespondInput{SessionID: "s", UserText: "问候"}, func(delta string) error {
		content.WriteString(delta)
		return nil
	})
	if !errors.Is(err, llm.ErrStreamIncomplete) || out.Content != "" || content.String() != "partial" {
		t.Fatalf("out=%+v error=%v emitted=%q", out, err, content.String())
	}
}

// 正式诊断结果保持完整提交，不把中间动作或报告片段发送到 reply 消费者
func TestTowerRespondStreamEscalateDoesNotEmit(t *testing.T) {
	client := newMockLLMClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeChatCompletion(w, `{"action":"escalate","question":"检查服务"}`)
	})
	executor := &fakeRunExecutor{report: core.Report{Title: "诊断报告", Summary: "服务异常"}}
	tower, err := NewTowerResponder(client, newTestFactory(t), executor, store.NewMemoryRunLedger(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	emitted := false
	out, err := tower.RespondStream(t.Context(), session.RespondInput{SessionID: "s", UserText: "服务异常"}, func(string) error {
		emitted = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if emitted || out.Mode != session.ModeDiagnostic || out.Report == nil {
		t.Fatalf("emitted=%v out=%+v", emitted, out)
	}
}
