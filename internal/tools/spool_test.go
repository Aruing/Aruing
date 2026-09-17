package tools

import (
	"context"
	"testing"
)

// ctx 会话标注往返；未标注返回空串
func TestSpoolScope(t *testing.T) {
	ctx := context.Background()
	if got := SpoolScopeFrom(ctx); got != "" {
		t.Fatalf("unscoped ctx = %q, want empty", got)
	}
	scoped := WithSpoolScope(ctx, "sess_123")
	if got := SpoolScopeFrom(scoped); got != "sess_123" {
		t.Fatalf("scoped ctx = %q, want sess_123", got)
	}
	// 空白编号视为未标注，工具层据此不开 spill
	if got := SpoolScopeFrom(WithSpoolScope(ctx, "  ")); got != "" {
		t.Fatalf("blank scope = %q, want empty", got)
	}
	// 原始 ctx 不受标注影响
	if got := SpoolScopeFrom(ctx); got != "" {
		t.Fatalf("parent ctx polluted: %q", got)
	}
}
