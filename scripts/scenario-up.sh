#!/usr/bin/env bash
# scripts/scenario-up.sh
#
# 起一个命名的 kind 场景集群并应用其故障清单。
# 已存在则复用并重新 apply（不删除他进程的集群）。
#
# 用法: scripts/scenario-up.sh <name>   或   make lab-up NAME=<name>

set -euo pipefail

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../scenarios/lib/common.sh"

scn_check_tools
name="$(scn_resolve_name "${1:-}")"
cluster="$(scn_cluster_name "$name")"
kubeconfig="$(scn_kubeconfig "$name")"
manifests="$ARUING_SCN_DIR/$name/manifests"

mkdir -p "$ARUING_SCN_KUBE_DIR"

if scn_kind_exists "$cluster"; then
	echo "scenario: kind cluster '$cluster' already exists; reusing and re-applying manifests."
else
	echo "scenario: creating kind cluster '$cluster' (首次会拉节点镜像，可能较慢)..."
	kind create cluster --name "$cluster" >/dev/null
fi

echo "scenario: exporting kubeconfig → $kubeconfig"
kind get kubeconfig --name "$cluster" > "$kubeconfig"

export KUBECONFIG="$kubeconfig"
echo "scenario: applying manifests from $manifests"
kubectl apply -f "$manifests"

# 等待用户工作负载落到稳态。要求：存在非加售命名空间的 Pod，且没有任何 Pod 处于 Pending/ContainerCreating。
# 注意：ImagePullBackOff / ErrImagePull / Running / CrashLoopBackOff 都算「已落稳」，正是我们要的坏态。
# 排除 kind 自带加售命名空间（kube-system / local-path-storage），否则其 Pod 早 Running 会让循环在工作负载建出前早退。
# 初始 sleep 让控制器有时间根据 Deployment 创建 Pod。
echo "scenario: waiting for workload pods to settle (poll up to ~120s)..."
sleep 5
i=0
while [ "$i" -lt 40 ]; do
	user_pods="$(kubectl get po -A --no-headers 2>/dev/null | grep -vE '^[[:space:]]*(kube-system|local-path-storage)[[:space:]]' || true)"
	if [ -n "$user_pods" ] && ! printf '%s\n' "$user_pods" | grep -Eq 'Pending|ContainerCreating'; then
		break
	fi
	sleep 3
	i=$((i + 1))
done

echo
echo "scenario: cluster summary (kubectl get po -A):"
kubectl get po -A
echo
echo "scenario: ready."
echo "  验收（以 chat 为主）:"
echo "    export KUBECONFIG=$kubeconfig"
echo "    ./bin/aruing chat --config playground/config.yaml \"<提示词>\""
echo "  提示词:   $ARUING_SCN_DIR/$name/prompts.md"
echo "  验收标准: $ARUING_SCN_DIR/$name/expect.md"
echo "  拆集群:   make lab-down NAME=$name"
