package main

import (
	"context"
	"errors"
	"testing"
)

// kubectl 配置路径优先；为空时落到 PATH 查找（PATH/missing 结果依赖宿主环境，不在此断言）
func TestResolveKubectlPathConfigured(t *testing.T) {
	path, src := resolveKubectlPath("/custom/kubectl")
	if path != "/custom/kubectl" || src != sourceConfig {
		t.Fatalf("got path=%q src=%q", path, src)
	}
}

// kubectl 缺失时不查 context，直接标 <n/a>
func TestResolveClusterInfoMissing(t *testing.T) {
	called := false
	ci := resolveClusterInfo(context.Background(), "", sourceMissing, func(context.Context, string) (string, error) {
		called = true
		return "", nil
	})
	if called {
		t.Fatal("must not query context when kubectl missing")
	}
	if ci.context != contextMissing {
		t.Fatalf("context=%q want %q", ci.context, contextMissing)
	}
}

// 查询成功时取 current-context 并去空白
func TestResolveClusterInfoSuccess(t *testing.T) {
	ci := resolveClusterInfo(context.Background(), "/bin/kubectl", sourcePATH, func(_ context.Context, p string) (string, error) {
		if p != "/bin/kubectl" {
			t.Fatalf("unexpected kubectl path %q", p)
		}
		return "kind-demo\n", nil
	})
	if ci.context != "kind-demo" {
		t.Fatalf("context=%q want kind-demo", ci.context)
	}
	if ci.contextErr != nil {
		t.Fatalf("unexpected err: %v", ci.contextErr)
	}
}

// 查询失败时标 <none> 并保留原因供 verbose 展示
func TestResolveClusterInfoFailure(t *testing.T) {
	wantErr := errors.New("boom")
	ci := resolveClusterInfo(context.Background(), "/bin/kubectl", sourcePATH, func(context.Context, string) (string, error) {
		return "", wantErr
	})
	if ci.context != contextNone {
		t.Fatalf("context=%q want %q", ci.context, contextNone)
	}
	if !errors.Is(ci.contextErr, wantErr) {
		t.Fatalf("contextErr=%v want %v", ci.contextErr, wantErr)
	}
}

// 查询返回空白时标 <none>（不当作有效 context）
func TestResolveClusterInfoEmpty(t *testing.T) {
	ci := resolveClusterInfo(context.Background(), "/bin/kubectl", sourcePATH, func(context.Context, string) (string, error) {
		return "  \n", nil
	})
	if ci.context != contextNone {
		t.Fatalf("context=%q want %q", ci.context, contextNone)
	}
}
