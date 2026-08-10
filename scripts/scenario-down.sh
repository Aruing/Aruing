#!/usr/bin/env bash
# scripts/scenario-down.sh
#
# 删除命名的 kind 场景集群及其临时 kubeconfig。
#
# 用法: scripts/scenario-down.sh <name>   或   make scenario-down NAME=<name>

set -euo pipefail

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../scenarios/lib/common.sh"

scn_check_tools
name="$(scn_resolve_name "${1:-}")"
cluster="$(scn_cluster_name "$name")"
kubeconfig="$(scn_kubeconfig "$name")"

echo "scenario: deleting kind cluster '$cluster'"
kind delete cluster --name "$cluster"
rm -f "$kubeconfig"
echo "scenario: removed kubeconfig $kubeconfig"
echo "scenario: done."
