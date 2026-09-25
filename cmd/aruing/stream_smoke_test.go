package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Aruing/Aruing/internal/config"
	"github.com/Aruing/Aruing/internal/llm"
)

// 显式开启才调用真实供应商；只记录块数和结束状态，不记录凭证或正文
func TestStreamSmoke(t *testing.T) {
	if os.Getenv("ARUING_STREAM_SMOKE") != "1" {
		t.Skip("set ARUING_STREAM_SMOKE=1 to call configured provider")
	}
	cfg, _, err := config.LoadResolvedWith("", config.ResolveOptions{Cwd: "../.."})
	if err != nil {
		t.Fatal("load model configuration failed")
	}
	if err = config.ValidateLLM(cfg); err != nil {
		t.Skip("model configuration unavailable")
	}
	client, err := llm.NewClient(cfg.LLM.ToClientConfig())
	if err != nil {
		t.Fatal("construct model client failed")
	}
	stream := client.(llm.Streamer)
	t.Run("complete", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		chunks := 0
		summary, streamErr := stream.Stream(ctx, llm.StreamRequest{Request: llm.Request{
			System: "请用中文简要回答，不调用工具。", User: "分三点介绍 Go 的 context，每点一句话。", Label: "stream-smoke",
		}}, func(string) error { chunks++; return nil })
		if streamErr != nil {
			t.Fatalf("provider stream failed (%T)", streamErr)
		}
		if chunks == 0 || summary.FinishReason != "stop" {
			t.Fatalf("chunks=%d finish=%s", chunks, summary.FinishReason)
		}
		t.Logf("chunks=%d finish=%s usage_present=%t", chunks, summary.FinishReason, summary.Usage != nil)
	})
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		chunks := 0
		_, streamErr := stream.Stream(ctx, llm.StreamRequest{Request: llm.Request{
			System: "请用中文回答。", User: "详细介绍 Go 的 context 用法。", Label: "stream-smoke",
		}}, func(string) error { chunks++; cancel(); return nil })
		if chunks == 0 || !errors.Is(streamErr, context.Canceled) {
			t.Fatalf("chunks=%d canceled=%t", chunks, errors.Is(streamErr, context.Canceled))
		}
		t.Logf("canceled after %d chunk", chunks)
	})
}
