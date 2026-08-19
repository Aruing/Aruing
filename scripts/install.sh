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

# 版本解析：列表 API 第一个非 draft 项即最新（含 pre-release，决策：永远装最新）
# 返回体为 JSON 数组；tag_name 字段机械提取，避免依赖 jq
release_json="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases?per_page=10")" \
  || err "failed to query GitHub releases API (network issue, or anonymous rate limit — retry later)"
tag="$(printf '%s' "$release_json" | grep -o '"tag_name": *"[^"]*"' | head -1 | sed 's/.*"tag_name": *"//; s/"$//')"
[ -n "$tag" ] || err "no published release found"

asset="${asset_os}.tar.gz"
base_url="https://github.com/${REPO}/releases/download/${tag}"
echo "==> downloading ${asset} (${tag})"

cd "$TMP_DIR"
curl -fsSL -o "$asset" "${base_url}/${asset}" || err "download failed: ${asset}"
curl -fsSL -o checksums.txt "${base_url}/checksums.txt" || err "download failed: checksums.txt"

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
