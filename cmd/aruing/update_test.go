package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"io"
	"net/http"
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

// extractBinary 端到端：真实 rc1 tar.gz 产物解包出可执行字节（网络下载，
// E2E 门控同前）；解出的字节应能算哈希且与压缩包不同（证明真的解了包）
func TestExtractBinaryRealArchive(t *testing.T) {
	if os.Getenv("ARUING_UPDATE_E2E") != "1" {
		t.Skip("set ARUING_UPDATE_E2E=1 to run the live archive test")
	}
	resp, err := http.Get("https://github.com/Aruing/Aruing/releases/download/v0.1.0-rc1/aruing_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	archive, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	bin, err := extractBinary(archive)
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if len(bin) < 1_000_000 {
		t.Fatalf("extracted binary too small: %d bytes", len(bin))
	}
	if sha256.Sum256(bin) == sha256.Sum256(archive) {
		t.Fatal("extracted bytes must differ from archive (not extracted)")
	}
}

// 内存构造 zip / tar.gz 双格式验证解包（离线、确定性）
func TestExtractBinaryFormats(t *testing.T) {
	oldOS := runtimeGOOS
	t.Cleanup(func() { runtimeGOOS = oldOS })

	runtimeGOOS = "darwin"
	tgz := buildTestTarGz(t, "aruing", []byte("BINARY-DARWIN"))
	got, err := extractBinary(tgz)
	if err != nil || string(got) != "BINARY-DARWIN" {
		t.Fatalf("tar.gz extract = %q, %v", got, err)
	}

	runtimeGOOS = "windows"
	z := buildTestZip(t, map[string][]byte{"aruing.exe": []byte("BINARY-WIN"), "README.md": []byte("doc")})
	got, err = extractBinary(z)
	if err != nil || string(got) != "BINARY-WIN" {
		t.Fatalf("zip extract = %q, %v", got, err)
	}

	// 目标缺失时报错而非空字节
	runtimeGOOS = "darwin"
	if _, err := extractBinary(buildTestTarGz(t, "other", []byte("x"))); err == nil {
		t.Fatal("want error when binary not in archive")
	}
}

func buildTestTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "somedir/" + name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func buildTestZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	zw.Close()
	return buf.Bytes()
}

// tagFromURL：releases/download 路径形态提取；无关路径返回空
func TestTagFromURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Aruing/Aruing/releases/download/v0.1.0/aruing_darwin_arm64.tar.gz", "v0.1.0"},
		{"/releases/download/v10.20.30-rc1/x.zip", "v10.20.30-rc1"},
		{"/some/other/path", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := tagFromURL(tc.in); got != tc.want {
			t.Fatalf("tagFromURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// 版本方向判断（防降级核心）：
// stable 远端 > 本地 stable → 更新；
// 远端 == 本地三段号且本地是 rc → 更新（rc 升正式）；
// 远端 < 本地（本地更新 rc 或更高 stable）→ 不更新；
// 解析失败 → 不更新（宁可不动）
func TestNewerVersion(t *testing.T) {
	cases := []struct {
		remote, local string
		want          bool
	}{
		{"v0.1.0", "0.0.9", true},
		{"v0.2.0", "0.1.9", true},
		{"v1.0.0", "0.9.9", true},
		{"v0.1.1", "0.1.0", true},
		{"v0.1.0", "0.1.0", false},     // 相等 stable
		{"v0.1.0", "0.1.0-rc1", true},  // rc 升同号正式
		{"v0.1.0", "0.2.0-rc1", false}, // 本地更新的 rc，不得降级
		{"v0.1.0", "0.2.0", false},     // 本地更高 stable
		{"v0.1.0-rc1", "0.1.0", false}, // 远端是 rc 场景防御（stable 直链不应出现）
		{"garbage", "0.1.0", false},    // 远端解析失败
		{"v0.1.0", "garbage", false},   // 本地解析失败
	}
	for _, tc := range cases {
		if got := newerVersion(tc.remote, tc.local); got != tc.want {
			t.Fatalf("newerVersion(%q, %q) = %v, want %v", tc.remote, tc.local, got, tc.want)
		}
	}
}
