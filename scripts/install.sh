#!/bin/sh
# aruing 一行安装脚本（macOS / Linux）
# 用法：curl -fsSL https://raw.githubusercontent.com/Aruing/Aruing/main/scripts/install.sh | sh
# 流程：平台检测 → 列表 API 解析最新版本 → 下载产物与 checksums → sha256 校验 →
#       解包到 ~/.aruing/bin（免 sudo）→ PATH 检测与提示
# 设计事实（rc1 实测）：releases/latest 不含 pre-release，必须用列表 API；
#                       checksums 按产物规范名匹配，下载不得改名

set -eu

REPO="Aruing/Aruing"
INSTALL_DIR="${HOME}/.aruing/bin"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

err() { echo "aruing install: $*" >&2; exit 1; }

# 平台检测：只映射已发布的组合（darwin amd64/arm64、linux amd64/arm64），其余明确报错不猜
os="$(uname -s)"
arch="$(uname -m)"
case "$os:$arch" in
  Darwin:x86_64) asset_os=aruing_darwin_amd64 ;;
  Darwin:arm64) asset_os=aruing_darwin_arm64 ;;
  Linux:x86_64) asset_os=aruing_linux_amd64 ;;
  Linux:aarch64) asset_os=aruing_linux_arm64 ;;
  *) err "unsupported platform: $os $arch (supported: darwin amd64/arm64, linux amd64/arm64; windows 见 install.ps1)" ;;
esac

command -v curl >/dev/null 2>&1 || err "curl is required (macOS: built-in; debian/ubuntu: apt install curl)"

asset="${asset_os}.tar.gz"
cd "$TMP_DIR"

# 版本解析双层策略（免限流优先）：
# 1) stable 直链：releases/latest/download/<产物> 是 CDN 永久别名，始终指向最新
#    正式版（不含 pre-release），完全不经过 api.github.com——匿名 API 按出口 IP
#    60 次/小时限流（公司 NAT/VPN/云容器池极易撞墙），直链对正常用户结构性免疫
# 2) API 列表发现（回退）：仅当 stable 尚不存在（404，首发前的 rc 窗口）走到；
#    此时取列表第一个非 draft 项（含 pre-release）。撞限流时给出 Releases 页面
#    手工下载兜底，而不是让用户干等限流窗口
stable_base="https://github.com/${REPO}/releases/latest/download"
if curl -fsI -o /dev/null "${stable_base}/${asset}" 2>/dev/null; then
  echo "==> downloading ${asset} (latest stable)"
  curl -fsSL -o "$asset" "${stable_base}/${asset}" || err "download failed: ${asset}"
  curl -fsSL -o checksums.txt "${stable_base}/checksums.txt" || err "download failed: checksums.txt"
else
  release_json="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases?per_page=10")" \
    || err "no stable release yet, and GitHub API failed (network, or anonymous rate limit — open https://github.com/${REPO}/releases to download manually)"
  tag="$(printf '%s' "$release_json" | grep -o '"tag_name": *"[^"]*"' | head -1 | sed 's/.*"tag_name": *"//; s/"$//')"
  [ -n "$tag" ] || err "no published release found"
  base_url="https://github.com/${REPO}/releases/download/${tag}"
  echo "==> downloading ${asset} (${tag})"
  curl -fsSL -o "$asset" "${base_url}/${asset}" || err "download failed: ${asset}"
  curl -fsSL -o checksums.txt "${base_url}/checksums.txt" || err "download failed: checksums.txt"
fi

# 校验和验证：从 checksums.txt 提取本产物单行单独比对
# 规避 GNU --ignore-missing / busybox / macOS 三方行为差异（只算本产物则完全一致）
# 失败必须中止；半成品在 TMP_DIR，trap 兜底清理
expected="$(grep " ${asset}$" checksums.txt | head -1 | awk '{print $1}')"
[ -n "$expected" ] || err "no checksum entry for ${asset}"
if command -v sha256sum >/dev/null 2>&1; then
  echo "$asset 校验中..."
  actual="$(sha256sum "$asset" | awk '{print $1}')"
else
  # macOS 无 sha256sum，用 shasum（同样只算本产物）
  command -v shasum >/dev/null 2>&1 || err "sha256sum or shasum is required"
  echo "$asset 校验中..."
  actual="$(shasum -a 256 "$asset" | awk '{print $1}')"
fi
[ "$actual" = "$expected" ] || err "checksum mismatch for ${asset}"

mkdir -p "$INSTALL_DIR"
tar xzf "$asset" -C "$INSTALL_DIR"
chmod +x "$INSTALL_DIR/aruing"

echo "==> installed: ${INSTALL_DIR}/aruing"
"$INSTALL_DIR/aruing" version || true

# PATH 检测：已在则静默；不在则打印可复制的 rc 写入命令（按登录 shell 给形态，不自动写入）
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    echo ""
    echo "==> ${INSTALL_DIR} is not in your PATH. Add it with:"
    case "${SHELL:-}" in
      */fish) echo "       fish_add_path ~/.aruing/bin" ;;
      */zsh) echo "       echo 'export PATH=\"\$HOME/.aruing/bin:\$PATH\"' >> ~/.zshrc" ;;
      */bash) echo "       echo 'export PATH=\"\$HOME/.aruing/bin:\$PATH\"' >> ~/.bashrc" ;;
      *) echo "       export PATH=\"\$HOME/.aruing/bin:\$PATH\"  (add to your shell profile)" ;;
    esac
    echo "       then restart your shell."
    ;;
esac
