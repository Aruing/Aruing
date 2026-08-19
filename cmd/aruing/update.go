// aruing update 自更新：检测 GitHub Releases 新版本，校验和验证后原子替换自身
//
// 三道防线（beta22 决策 3）：源码构建（version=dev）明确报错不猜；npm 安装来源
// （路径含 node_modules）拒绝自替换、提示 npm update——npm 包完整性由 npm 管，
// 二进制不得自替换脱钩；已是最新静默退出
//
// 分工（2026-08-19 决策：换 minio/selfupdate，go-selfupdate 因传递依赖
// openpgp 触发 GO-2026-5932 且上游无修复计划）：检测/下载/校验复用与
// scripts/install.sh 同构的策略（stable 直链免 API 限流 + checksums 单行比对，
// 该策略已在安装脚本真机验证）；原子替换/Windows 运行中 exe/失败回滚交给
// minio/selfupdate.Apply
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/minio/selfupdate"
)

// stable 直链基地址：CDN 永久别名，始终指向最新正式版，零 API 调用免限流
// （与 scripts/install.sh 同策略；仓库 slug 已写死在 URL 里）
const updateStableBase = "https://github.com/Aruing/Aruing/releases/latest/download"

// 检测与下载的整体超时；慢网给足下载 12MB 产物的余量
const updateTimeout = 5 * time.Minute

// 解析 update 子命令参数并执行自更新
//
// --check 只检测不更新（打印 current/latest，供脚本消费）；默认执行更新
func runUpdate(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	checkOnly := fs.Bool("check", false, "check for a newer release without updating")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: aruing update [flags]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Update aruing to the latest release (from GitHub Releases).")
		fmt.Fprintln(stderr, "Binaries installed via npm should use: npm update -g aruing")
		fmt.Fprintln(stderr, "")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 防线 1：源码构建无法自更新——诚实报错，不与 Release 版本瞎比
	if version == "dev" || !isSemverish(version) {
		return fmt.Errorf("cannot self-update a source build (version %q); install a release via the install script or npm first", version)
	}

	// 防线 2：npm 安装来源——二进制位于 node_modules 下（解析符号链接后判定）
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	if resolved, linkErr := filepath.EvalSymlinks(exePath); linkErr == nil {
		exePath = resolved
	}
	if isNpmInstallPath(exePath) {
		return fmt.Errorf("this aruing binary is managed by npm (%s);\nrun: npm update -g aruing", exePath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
	defer cancel()

	fmt.Fprintf(stdout, "current: %s\n", version)

	// stable 直链探测：与 install.sh 同策略。HEAD 探测产物在不在，避免下载体
	// 只为判存在；产物名规则与 .goreleaser.yaml 命名契约一致
	asset := updateAssetName()
	probe := http.Client{Timeout: 30 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, updateStableBase+"/"+asset, nil)
	resp, err := probe.Do(req)
	if err != nil {
		return fmt.Errorf("probe latest release (network?): %w", err)
	}
	defer resp.Body.Close()

	// 防线 3：stable 不存在（仓库尚无正式版）或已是最新——静默退出
	// 比较依据：直链的 CDN 命中会带出不可预测的最终 URL，改用 sha256 对照：
	// 本地二进制哈希与远端产物哈希一致即已是最新（内容级比较，最诚实）
	if resp.StatusCode == http.StatusNotFound {
		fmt.Fprintln(stdout, "already up to date (no stable release)")
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe latest release: unexpected status %d", resp.StatusCode)
	}

	if *checkOnly {
		// 只检测：拉取产物哈希与本地比对（不落盘不替换）
		same, err := sameAsRemote(ctx, exePath, asset)
		if err != nil {
			return err
		}
		if same {
			fmt.Fprintln(stdout, "already up to date")
		} else {
			fmt.Fprintf(stdout, "update available: run \"aruing update\" to install\n")
		}
		return nil
	}

	// 执行更新：下载 + checksums 校验 + 原子替换（Apply 含失败回滚提示）
	if err := applyUpdate(ctx, exePath, asset, stdout); err != nil {
		return err
	}
	return nil
}

// 按当前平台拼产物名（与 .goreleaser.yaml 的 {{ .ProjectName }}_{{ .Os }}_{{ .Arch }} 契约一致）
func updateAssetName() string {
	arch := runtimeGOARCH
	ext := "tar.gz"
	if runtimeGOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("aruing_%s_%s.%s", runtimeGOOS, arch, ext)
}

// 下载产物与 checksums，逐字节校验后流式喂给 selfupdate.Apply 替换自身
//
// 不落盘中转：Apply 接受 io.Reader，产物体直接从 HTTP 响应流入；
// 校验失败发生在 Apply 之前，二进制不会被触碰
func applyUpdate(ctx context.Context, exePath, asset string, stdout io.Writer) error {
	client := http.Client{}
	// checksums 先行：拿期望哈希（单行提取，与 install.sh 同逻辑）
	sumBody, err := fetchBody(ctx, client, updateStableBase+"/checksums.txt")
	if err != nil {
		return fmt.Errorf("fetch checksums: %w", err)
	}
	expected := checksumFor(string(sumBody), asset)
	if expected == "" {
		return fmt.Errorf("no checksum entry for %s", asset)
	}

	assetResp, err := fetchStream(ctx, client, updateStableBase+"/"+asset)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	defer assetResp.Body.Close()

	// 读全量：12MB 量级内存可控；checksums 校验的是压缩包哈希，先验包完整性
	archive, err := io.ReadAll(assetResp.Body)
	if err != nil {
		return fmt.Errorf("read %s: %w", asset, err)
	}
	sum := sha256.Sum256(archive)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), expected) {
		return fmt.Errorf("checksum mismatch for %s — refusing to update", asset)
	}
	fmt.Fprintf(stdout, "checksum ok, extracting...\n")

	// Apply 期望裸可执行流，压缩包必须先解出二进制（pr-agent 评审修复：
	// 直接喂压缩包会“成功”写出坏二进制）
	binary, err := extractBinary(archive)
	if err != nil {
		return err
	}

	if err := selfupdate.Apply(bytes.NewReader(binary), selfupdate.Options{TargetPath: exePath}); err != nil {
		if rollErr := selfupdate.RollbackError(err); rollErr != nil {
			return fmt.Errorf("update failed and rollback also failed: %v (manual recovery needed: reinstall via install script)", rollErr)
		}
		return fmt.Errorf("update failed (binary unchanged): %w", err)
	}
	fmt.Fprintf(stdout, "==> updated, restart your shell if needed\n")
	return nil
}

// --check 路径：本地二进制哈希与远端产物哈希比对
func sameAsRemote(ctx context.Context, exePath, asset string) (bool, error) {
	client := http.Client{}
	sumBody, err := fetchBody(ctx, client, updateStableBase+"/checksums.txt")
	if err != nil {
		return false, fmt.Errorf("fetch checksums: %w", err)
	}
	expected := checksumFor(string(sumBody), asset)
	if expected == "" {
		return false, fmt.Errorf("no checksum entry for %s", asset)
	}
	// checksums 记录的是压缩包哈希；--check 的比较对象是解包后的远端二进制
	// 与本地二进制（裸二进制 vs 压缩包哈希永远不等，会误报恒有更新）
	archive, err := fetchBody(ctx, client, updateStableBase+"/"+asset)
	if err != nil {
		return false, fmt.Errorf("download %s: %w", asset, err)
	}
	sum := sha256.Sum256(archive)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), expected) {
		return false, fmt.Errorf("checksum mismatch for %s", asset)
	}
	remote, err := extractBinary(archive)
	if err != nil {
		return false, err
	}
	local, err := fileSHA256(exePath)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(hashBytes(remote), local), nil
}

// 从 tar.gz / zip 压缩包解出 aruing 可执行文件的裸字节（内存中完成，不落盘）
//
// 包内文件名与 .goreleaser.yaml 打包契约一致：aruing（或 windows 下 aruing.exe）
// 伴随 LICENSE / README 等附件，按基名精确匹配目标；zip 与 tar.gz 按魔数识别
func extractBinary(archive []byte) ([]byte, error) {
	want := "aruing"
	if runtimeGOOS == "windows" {
		want = "aruing.exe"
	}
	if len(archive) > 4 && bytes.Equal(archive[:4], []byte("PK\x03\x04")) {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("open zip: %w", err)
		}
		for _, f := range zr.File {
			if path.Base(f.Name) != want || f.FileInfo().IsDir() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open %s in zip: %w", f.Name, err)
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
		return nil, fmt.Errorf("%s not found in release zip", want)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open tar.gz: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != want {
			continue
		}
		return io.ReadAll(tr)
	}
	return nil, fmt.Errorf("%s not found in release tar.gz", want)
}

// sha256 十六进制（比较用）
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// 从 checksums.txt 提取本产物的期望哈希（单行匹配，规避三方 sha256sum 行为差异）
func checksumFor(checksums, asset string) string {
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == asset {
			return fields[0]
		}
	}
	return ""
}

// 拉取完整响应体（checksums 这类小文件）
func fetchBody(ctx context.Context, client http.Client, url string) ([]byte, error) {
	resp, err := fetchStream(ctx, client, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// 建立流式请求并校验状态码
func fetchStream(ctx context.Context, client http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return resp, nil
}

// 本地文件 SHA256（十六进制小写）
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// 判断路径是否 npm 安装形态（node_modules 内，含全局 node 安装目录）
//
// npm 全局装的二进制真实路径形如 .../node_modules/aruing/...；宽松匹配
// node_modules 片段宁可误拒（提示 npm update 无害）也不误替换（npm 包状态脱钩）
func isNpmInstallPath(path string) bool {
	normalized := filepath.ToSlash(strings.ToLower(path))
	return strings.Contains(normalized, "node_modules")
}

// 粗判注入的版本串是否形如 semver（x.y.z 开头）
//
// 只用于 dev 兜底提示，不做完整校验；ldflags 注入的 git describe 形如
// 0.1.0-rc1 / 0.1.0-29-gabcdef 均以 x.y.z 开头
func isSemverish(v string) bool {
	if v == "" {
		return false
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 3 {
		return false
	}
	for _, p := range parts[:2] {
		if p == "" || !allDigits(p) {
			return false
		}
	}
	// 第三段允许 0-rc1 这类后缀
	head := strings.SplitN(parts[2], "-", 2)[0]
	return head != "" && allDigits(head)
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// 运行时平台经变量暴露，测试可覆写平台产物名拼接
var (
	runtimeGOOS   = runtime.GOOS
	runtimeGOARCH = runtime.GOARCH
)
