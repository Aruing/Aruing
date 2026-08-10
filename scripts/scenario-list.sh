#!/usr/bin/env bash
# scripts/scenario-list.sh
#
# 列出已知场景（scenarios/ 下含 manifests/ 的目录）及其 kind 集群状态。
# 不强制 kind 已安装：缺 kind 时仅列出目录，集群状态显示为「?」。
#
# 用法: scripts/scenario-list.sh   或   make scenario-list

set -euo pipefail

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../scenarios/lib/common.sh"

have_kind=0
command -v kind >/dev/null 2>&1 && have_kind=1

echo "known scenarios (directories with manifests/ under scenarios/):"
found=0
while IFS= read -r n; do
	found=1
	status="?"
	if [ "$have_kind" -eq 1 ]; then
		if scn_kind_exists "$(scn_cluster_name "$n")"; then
			status="up"
		else
			status="down"
		fi
	fi
	printf '  %-22s [%s]\n' "$n" "$status"
done < <(scn_known_scenarios)

if [ "$found" -eq 0 ]; then
	echo "  (none)"
fi
