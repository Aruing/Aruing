// aruing update 自更新：检测 GitHub Releases 新版本，下载校验后原子替换自身
//
// 三道防线（beta22 决策 3）：源码构建（version=dev）明确报错不猜；npm 安装来源
// （路径含 node_modules）拒绝自替换、提示 npm update——npm 包完整性由 npm 管，
// 二进制不得自替换脱钩；已是最新静默退出
//
// 校验与替换由 go-selfupdate 承担：ChecksumValidator 与 GoReleaser 的
// checksums.txt 产物零配置对齐；UpdateSelf 含 Windows 运行中 exe 处理与失败回滚
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/creativeprojects/go-selfupdate"
)

// 自更新检测的目标仓库；与模块路径同源（github.com/Aruing/Aruing）
var updateRepository = selfupdate.NewRepositorySlug("Aruing", "Aruing")

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

	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
		// Prerelease 默认 false：正式用户不会被推 rc，与安装脚本 stable 优先语义一致
	})
	if err != nil {
		return fmt.Errorf("init updater: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
	defer cancel()

	fmt.Fprintf(stdout, "current: %s\n", version)
	release, found, err := updater.DetectLatest(ctx, updateRepository)
	if err != nil {
		return fmt.Errorf("detect latest release (network?): %w", err)
	}
	// found=false：仓库无匹配正式版 Release（如仅 rc 存在）→ 对用户而言就是最新
	if !found {
		fmt.Fprintln(stdout, "already up to date")
		return nil
	}
	fmt.Fprintf(stdout, "latest:  %s\n", release.Version())

	// 防线 3：已是最新（库内 semver 比较，rc 不会被推给正式用户）
	if !release.GreaterThan(version) {
		fmt.Fprintln(stdout, "already up to date")
		return nil
	}

	if *checkOnly {
		fmt.Fprintln(stdout, "update available: run \"aruing update\" to install")
		return nil
	}

	fmt.Fprintf(stdout, "==> updating to %s...\n", release.Version())
	if _, err := updater.UpdateSelf(ctx, version, updateRepository); err != nil {
		return fmt.Errorf("update failed (binary unchanged): %w", err)
	}
	fmt.Fprintf(stdout, "==> updated: %s → %s\n", version, release.Version())
	return nil
}

// 判断路径是否 npm 安装形态（node_modules 内，含全局 node 安装目录）
//
// npm 全局装的二进制真实路径形如 .../node_modules/aruing/... 或
// .../lib/node_modules/...；宽松匹配 node_modules 片段宁可误拒（提示 npm update
// 无害）也不误替换（npm 包状态脱钩）
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
