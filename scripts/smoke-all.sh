#!/usr/bin/env bash
# scripts/smoke-all.sh
#
# 全量真集群 smoke：遍历所有已知场景，up → chat(prompts.md 第 1 条提示词) → down，
# 单场景失败不中断，最后打汇总表。供 `make smoke-all` / `make self-check` 使用。
#
# 语义（beta18 第 4 项定稿）：
# - 全部场景都跑，严格校验；exit code 汇总（任一环节失败 → 非零）
# - 集群已存在 → 复用并重新 apply（scenario-up.sh 既有语义，不拆别人的集群）
# - 前置体检：Docker / kind / kubectl / bin/aruing / LLM 配置，缺哪个报哪个
# - 对话内容是否符合 expect.md 由 agent / 维护者对照 log 判定（本脚本只管执行与留痕）
#
# 用法: scripts/smoke-all.sh   或   make smoke-all

set -uo pipefail

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../scenarios/lib/common.sh"
# common.sh 自带 set -euo pipefail；本脚本要单场景失败不中断，改回宽松语义，
# 失败一律由显式 if 捕获（run_step）。
set -uo pipefail

# 本脚本内自实现（不进 common.sh：仅 smoke-all 使用）：
# scn_first_prompt <name> → 打印 prompts.md 第 1 条有序列表提示词；
# 去序号；若以「开头，取到首个」为止（允许尾部备注如「…」（不带 ns））；无则空。
# 「」处理用 bash 字面量字符串操作（字节精确），不用 awk 多字节正则（BSD awk 不可靠）。
scn_first_prompt() {
	local f="$ARUING_SCN_DIR/$1/prompts.md" line
	[ -f "$f" ] || return 0
	line="$(awk '/^[[:space:]]*[0-9]+\.[[:space:]]/ { sub(/^[[:space:]]*[0-9]+\.[[:space:]]*/, ""); print; exit }' "$f")"
	case "$line" in
		「*) line="${line#「}"; line="${line%%」*}" ;;
	esac
	printf '%s\n' "$line"
}

logs_dir="$ARUING_SCN_DIR/.smoke"
mkdir -p "$logs_dir"

# ---------- 前置体检 ----------

missing=0

for tool in docker kind kubectl; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "preflight: missing tool: $tool"
		missing=1
	fi
done

if [[ ! -x "$PWD/bin/aruing" ]]; then
	echo "preflight: missing ./bin/aruing (run: make build)"
	missing=1
fi

# LLM 配置：playground/config.yaml 或 ARUING_CONFIG（chat 须 LLM，无配置必失败）
config=""
if [[ -n "${ARUING_CONFIG:-}" ]]; then
	config="$ARUING_CONFIG"
elif [[ -f "$PWD/playground/config.yaml" ]]; then
	config="$PWD/playground/config.yaml"
fi
if [[ -z "$config" || ! -f "$config" ]]; then
	echo "preflight: missing LLM config (playground/config.yaml or ARUING_CONFIG)"
	missing=1
else
	echo "preflight: LLM config → $config"
fi

if [[ "$missing" -ne 0 ]]; then
	echo "preflight: FAILED — fix the missing items above, then retry."
	exit 2
fi
echo "preflight: OK"

# ---------- 遍历场景 ----------

declare -a names
while IFS= read -r n; do names+=("$n"); done < <(scn_known_scenarios)

if [[ ${#names[@]} -eq 0 ]]; then
	echo "smoke-all: no known scenarios (directories with manifests/ under scenarios/)"
	exit 2
fi

echo "smoke-all: scenarios → ${names[*]}"
echo

declare -a rows
overall=0

run_step() { # <label> <logfile> <cmd...>
	local label="$1" log="$2"; shift 2
	if "$@" >>"$log" 2>&1; then
		echo "$label=ok"
		return 0
	else
		echo "$label=FAIL (log: $log)"
		return 1
	fi
}

for name in "${names[@]}"; do
	echo "== scenario: $name =="
	log="$logs_dir/$name.log"
	: >"$log"

	up_ok=0 chat_ok=0 down_ok=0
	run_step up "$log" bash "$PWD/scripts/scenario-up.sh" "$name" && up_ok=1

	if [[ $up_ok -eq 1 ]]; then
		kubeconfig="$PWD/scenarios/.kube/$name.yaml"
		msg="$(scn_first_prompt "$name")"
		if [[ -z "$msg" ]]; then
			echo "chat=SKIP (no prompt in prompts.md)"
			chat_ok=2 # skip 视为不通过：严格校验要求每场景都有可执行提示词
		else
			echo "-- chat prompt: $msg" >>"$log"
			run_step chat "$log" env KUBECONFIG="$kubeconfig" "$PWD/bin/aruing" chat "$msg" && chat_ok=1
		fi
		run_step down "$log" bash "$PWD/scripts/scenario-down.sh" "$name" && down_ok=1
	else
		echo "chat=SKIP (up failed)"
		echo "down=SKIP (up failed)"
	fi

	row="$name up=$([[ $up_ok -eq 1 ]] && echo ok || echo FAIL) chat="
	case $chat_ok in
		1) row+="ok" ;;
		2) row+="SKIP" ;;
		*) row+="FAIL" ;;
	esac
	row+=" down=$([[ $down_ok -eq 1 ]] && echo ok || echo FAIL)"
	rows+=("$row")

	[[ $up_ok -eq 1 && $chat_ok -eq 1 && $down_ok -eq 1 ]] || overall=1
	echo
done

# ---------- 汇总 ----------

echo "==================== smoke-all summary ===================="
for row in "${rows[@]}"; do
	echo "$row"
done
echo "==========================================================="
if [[ $overall -eq 0 ]]; then
	echo "smoke-all: ALL OK (content vs expect.md → 人工/agent 对照 $logs_dir/*.log 判定)"
else
	echo "smoke-all: FAILURES above — see $logs_dir/*.log"
fi
exit $overall
