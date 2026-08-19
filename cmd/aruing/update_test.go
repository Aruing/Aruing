package main

import (
	"bytes"
	"strings"
	"testing"
)

// 恢复包级 version 变量的工具；自更新路径深度依赖注入的版本串
func withVersion(t *testing.T, v string) {
	t.Helper()
	old := version
	t.Cleanup(func() { version = old })
	version = v
}

// 源码构建（dev）应明确拒绝自更新，不发起任何网络请求
func TestRunUpdateRejectsSourceBuild(t *testing.T) {
	withVersion(t, "dev")
	var stdout, stderr bytes.Buffer
	err := runUpdate(nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("want error for dev build")
	}
	if !strings.Contains(err.Error(), "source build") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// 非 semver 形态的注入值（空串、垃圾串）同样拒绝
func TestRunUpdateRejectsNonSemver(t *testing.T) {
	for _, v := range []string{"", "garbage", "0.1"} {
		withVersion(t, v)
		var stdout, stderr bytes.Buffer
		if err := runUpdate(nil, &stdout, &stderr); err == nil {
			t.Fatalf("want error for version %q", v)
		}
	}
}

// npm 安装路径（node_modules 内，含符号链接解析后）应拒绝自替换并提示 npm update
func TestRunUpdateRejectsNpmInstallPath(t *testing.T) {
	withVersion(t, "0.1.0-rc1")
	// 把当前测试二进制视为 npm 安装：isNpmInstallPath 是纯函数，直接验证判定逻辑
	// 加上一条 runUpdate 全流程（路径不命中时走到网络检测，此处只测纯函数面）
	cases := []struct {
		path string
		want bool
	}{
		{"/usr/lib/node_modules/aruing/bin/aruing", true},
		{"/Users/x/.npm-global/lib/node_modules/aruing/aruing", true},
		{"/Users/x/.aruing/bin/aruing", false},
		{"/usr/local/bin/aruing", false},
	}
	for _, tc := range cases {
		if got := isNpmInstallPath(tc.path); got != tc.want {
			t.Fatalf("isNpmInstallPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// isSemverish 边界：合法形态通过，git describe 变体通过，垃圾串拒绝
func TestIsSemverish(t *testing.T) {
	valid := []string{"0.1.0", "0.1.0-rc1", "1.2.3-29-gabcdef", "10.20.30"}
	invalid := []string{"", "dev", "0.1", "v0.1.0", "x.y.z", "0.1.0rc1"}
	for _, v := range valid {
		if !isSemverish(v) {
			t.Fatalf("isSemverish(%q) should be true", v)
		}
	}
	for _, v := range invalid {
		if isSemverish(v) {
			t.Fatalf("isSemverish(%q) should be false", v)
		}
	}
}

// --check 对真实 GitHub API：仓库已有 Release（rc 会被 prerelease 过滤），
// 网络可达时应正常返回而不更新；短模式（-short）跳过
func TestRunUpdateCheckRealAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("network test skipped in short mode")
	}
	withVersion(t, "0.0.1")
	var stdout, stderr bytes.Buffer
	err := runUpdate([]string{"--check"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runUpdate --check: %v", err)
	}
	// 仓库当前只有 rc（prerelease 过滤默认关）→ 正确行为是 already up to date；
	// v0.1.0 正式发布后此断言自然变为 latest 路径，两者都是成功形态
	out := stdout.String()
	if !strings.Contains(out, "current: 0.0.1") {
		t.Fatalf("missing current line:\n%s", out)
	}
	if !strings.Contains(out, "latest:") && !strings.Contains(out, "already up to date") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}
