#!/usr/bin/env bash
# bigtable-fleet 实例表生成器：确定性产出 manifests/10-fleet.yaml（kind: List，单行 flow-style）
#
# 表构造与 bench rich-value fleet 生成器常数对齐（笔记 plan/0.1.4/map-reduce/
# 2026-9-24-map-reduce-smoke.md D2），真场景 = 步骤 3 已实证打崩单遍投影的表的集群实化：
#   - N=3000 行；故障载体 K=N/100=30：每 100 行第 97 位（i%100==97），
#     STATUS=CrashLoopBackOff / READY=0/1 / RESTARTS=7 / 镜像坏 tag（30 个共享同一 tag = 根因）
#   - Pending 无害扰动 2%（i%50==23）；非常规重启每 20 行 1 行轮转 1..20（i%20==11）；
#     稀有节点每 25 行 1 行轮转 node-4..20（i%25==9），主流节点 node-1..3 轮转
#   - 命名：填充 app-<i:04d>，故障位 app-<i:04d>z——字典序恰落槽位（kubectl 按名排序，
#     散布确定化，覆盖 ~3%–99.9% 每 ~3.3%）
#
# 零随机：同参数输出逐字节一致。评审读本脚本与 D2 对照，不读生成产物
# （产物 ~3000 行机械 YAML，PR 规模纪律明示生成物不计入行数）

set -euo pipefail

n="${1:-3000}"
root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/manifests/10-fleet.yaml"

printf 'apiVersion: v1\nkind: List\nitems:\n' >"$out"

for ((i = 0; i < n; i++)); do
	if ((i % 100 == 97)); then
		# 故障载体：多载体中权值根因（CrashLoopBackOff×K），共享坏 tag
		name="app-$(printf '%04d' "$i")z"
		image="registry.example/fleet/app:v1_faulty"
		ready='"0/1"'
		state="CrashLoopBackOff"
		restarts='"7"'
		node="node-$((1 + i % 3))"
	else
		name="app-$(printf '%04d' "$i")"
		image="registry.example/fleet/app:v1"
		ready='"1/1"'
		state="Running"
		restarts='"0"'
		node="node-$((1 + i % 3))"
		# 无害扰动与论域扩张：槽位经模数互斥设计，不与故障位重叠
		if ((i % 50 == 23)); then
			state="Pending"
			ready='"0/1"'
		fi
		if ((i % 20 == 11)); then
			restarts="\"$((i / 20 % 20 + 1))\""
		fi
		if ((i % 25 == 9)); then
			node="node-$((4 + i / 25 % 17))"
		fi
	fi
	printf -- '- {apiVersion: demo.aruing.io/v1, kind: AppFleet, metadata: {name: %s, namespace: fleet}, spec: {image: %s, node: %s}, status: {ready: %s, state: %s, restarts: %s}}\n' \
		"$name" "$image" "$node" "$ready" "$state" "$restarts" >>"$out"
done

echo "generated: $out ($(grep -c '^- ' "$out") items)"
