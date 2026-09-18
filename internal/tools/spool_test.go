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

// 引用探测：只认 stdoutSpool 字段；未写/空文件名/非对象形态均返回 nil
func TestStdoutSpoolRef(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string // 命中时的 File，空串表示期望 nil
	}{
		{"带引用", `{"argv":["logs"],"exitCode":0,"stdoutSpool":{"sessionId":"s","file":"spool-1","totalBytes":10,"totalLines":2}}`, "spool-1"},
		{"无引用字段", `{"argv":["logs"],"exitCode":0}`, ""},
		{"引用字段为空对象", `{"stdoutSpool":{}}`, ""},
		{"文件名为空白", `{"stdoutSpool":{"sessionId":"s","file":"  "}}`, ""},
		{"非对象形态", `[]`, ""},
		{"空串", ``, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := StdoutSpoolRef([]byte(tc.raw))
			if tc.want == "" {
				if ref != nil {
					t.Fatalf("want nil, got %+v", ref)
				}
				return
			}
			if ref == nil || ref.File != tc.want {
				t.Fatalf("want file %q, got %+v", tc.want, ref)
			}
		})
	}
}
