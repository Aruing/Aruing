# scenarios/lib/common.sh
#
# 场景台架共享助手。被 scripts/scenario-*.sh 以 `source` 引入。
# 不引入新依赖；仅依赖 kind / kubectl / docker（在调用点校验存在）。
#
# 命名约定（唯一事实源）：
#   集群名    = aruing-sc-<scenario>
#   kubeconfig = scenarios/.kube/<scenario>.yaml
#   manifests  = scenarios/<scenario>/manifests/
# scenario.yaml 只给人读，脚本不解析。

set -euo pipefail

# 仓库根：本文件位于 scenarios/lib/，向上两级
_ARUING_SCN_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ARUING_REPO_ROOT="$(cd "$_ARUING_SCN_LIB_DIR/../.." && pwd)"
ARUING_SCN_DIR="$ARUING_REPO_ROOT/scenarios"
ARUING_SCN_KUBE_DIR="$ARUING_SCN_DIR/.kube"

ARUING_SCN_CLUSTER_PREFIX="aruing-sc"

# 失败退出
scn_die() {
	echo "scenario: $*" >&2
	exit 1
}

# 校验三个必需二进制；缺一即清晰报错
scn_check_tools() {
	local missing=()
	local b
	for b in kind kubectl docker; do
		command -v "$b" >/dev/null 2>&1 || missing+=("$b")
	done
	if [ "${#missing[@]}" -gt 0 ]; then
		scn_die "missing required binaries: ${missing[*]}. Install them (e.g. 'brew install kind') and ensure they are in PATH."
	fi
}

# scn_resolve_name <arg> → 校验并打印场景名；确认场景目录与 manifests/ 存在
scn_resolve_name() {
	local name="${1:-}"
	[ -n "$name" ] || scn_die "scenario name required. Usage: make lab-up NAME=<id> (known: make lab-list)."
	local dir="$ARUING_SCN_DIR/$name"
	[ -d "$dir/manifests" ] || scn_die "scenario '$name' not found (no manifests/ under $dir). Known: run 'make lab-list'."
	echo "$name"
}

scn_cluster_name() { echo "$ARUING_SCN_CLUSTER_PREFIX-$1"; }
scn_kubeconfig()   { echo "$ARUING_SCN_KUBE_DIR/$1.yaml"; }

# scn_known_scenarios → 列出 scenarios/ 下含 manifests/ 的目录名（每行一个）
scn_known_scenarios() {
	local d name
	for d in "$ARUING_SCN_DIR"/*/; do
		[ -d "$d/manifests" ] || continue
		name="$(basename "$d")"
		echo "$name"
	done
}

# scn_kind_exists <cluster> → 0 若 kind 中已存在该集群
scn_kind_exists() {
	local cluster="$1"
	kind get clusters 2>/dev/null | grep -Fxq "$cluster"
}
