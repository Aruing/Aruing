package main

import (
	"bytes"
	"os"
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

// npm 安装路径判定：node_modules 内（大小写/斜杠形态归一）拒绝，其余放行
func TestIsNpmInstallPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/usr/lib/node_modules/aruing/bin/aruing", true},
		{"/Users/x/.npm-global/lib/node_modules/aruing/aruing", true},
		{`C:\Users\x\AppData\Roaming\npm\node_modules\aruing\aruing.exe`, true},
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

// checksums 单行提取：多行文件中按产物规范名取期望哈希；缺失返回空
func TestChecksumFor(t *testing.T) {
	sums := "aaa111  aruing_darwin_amd64.tar.gz\n" +
		"bbb222  aruing_darwin_arm64.tar.gz\n" +
		"ccc333  aruing_windows_amd64.zip\n"
	if got := checksumFor(sums, "aruing_darwin_arm64.tar.gz"); got != "bbb222" {
		t.Fatalf("checksumFor = %q, want bbb222", got)
	}
	if got := checksumFor(sums, "aruing_linux_amd64.tar.gz"); got != "" {
		t.Fatalf("missing asset should give empty, got %q", got)
	}
	// 二进制产物行的 * 前缀（sha256sum 二进制标记）应被容忍
	if got := checksumFor("ddd444 *aruing_windows_amd64.zip\n", "aruing_windows_amd64.zip"); got != "ddd444" {
		t.Fatalf("* prefix form = %q, want ddd444", got)
	}
}

// 平台产物名拼接：与 .goreleaser.yaml 命名契约一致
func TestUpdateAssetName(t *testing.T) {
	oldOS, oldArch := runtimeGOOS, runtimeGOARCH
	t.Cleanup(func() { runtimeGOOS, runtimeGOARCH = oldOS, oldArch })

	runtimeGOOS, runtimeGOARCH = "darwin", "arm64"
	if got := updateAssetName(); got != "aruing_darwin_arm64.tar.gz" {
		t.Fatalf("darwin/arm64 = %q", got)
	}
	runtimeGOOS, runtimeGOARCH = "windows", "amd64"
	if got := updateAssetName(); got != "aruing_windows_amd64.zip" {
		t.Fatalf("windows/amd64 = %q", got)
	}
}

// 真网络端到端（--check）：默认跳过，显式 ARUING_UPDATE_E2E=1 才跑
// 门控原因：CI 离线/限流/仓库状态变化会让真 API 用例 flaky（pr-agent 评审采纳）
func TestRunUpdateCheckRealNetwork(t *testing.T) {
	if os.Getenv("ARUING_UPDATE_E2E") != "1" {
		t.Skip("set ARUING_UPDATE_E2E=1 to run the live selfupdate check")
	}
	withVersion(t, "0.0.1")
	var stdout, stderr bytes.Buffer
	err := runUpdate([]string{"--check"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runUpdate --check: %v", err)
	}
	// 仓库正式版发布前后两种成功形态：有 stable 直链（比对）或暂无正式版
	out := stdout.String()
	if !strings.Contains(out, "current: 0.0.1") {
		t.Fatalf("missing current line:\n%s", out)
	}
}
