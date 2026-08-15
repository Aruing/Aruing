#!/usr/bin/env bash
# scripts/smoke-all.sh [scenario ...]
#
# 全量真集群 smoke：遍历（或指定）场景，up → 逐 case → down，单场景失败不中断，末尾汇总表。
# 供 `make smoke-all` / `make self-check` 使用。
#
# 语义（beta18 第 3 项 step 1，plan: 2026-8-15-cases-protocol.md）：
# - 全部场景都跑（严格校验）；指定参数则只跑指定场景
# - **fresh-up**：up 前若 kind 集群已存在，先 down 再 up——重启后状态严格等于 manifests，
#   不留上轮 case apply/ 的残留（维护者裁决 2026-8-15）
# - 无 cases/ 的场景 = 默认 case：只发 prompts.md 第 1 条（行为与旧版一致，回归）
# - 有 cases/ 的场景：顶层不再单独跑；case 目录按名排序，逐 case：可选 apply/ →
#   逐条 prompt 同 session 续聊（支持挂起-回复链）
# - apply/ 不回滚（工具只读不互相弄脏；纪律见 aruing-cluster-smoke）
# - 对话内容是否符合 expect.md 由 agent / 维护者对照 log 判定（本脚本只管执行与留痕）
#
# 用法: scripts/smoke-all.sh           或   make smoke-all
#       scripts/smoke-all.sh <name>    （单场景，供「只跑受影响场景」）

set -uo pipefail

. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../scenarios/lib/common.sh"
# common.sh 自带 set -euo pipefail；本脚本要单场景失败不中断，
# 必须显式关掉 errexit（set -uo 不含 +e 时 -e 会残留，pr-agent #81 指出）；
# 失败一律由显式 if 捕获（run_step）。
set +e -uo pipefail

# ---------- 本脚本内自实现（不进 common.sh：仅 smoke-all 使用） ----------

# scn_strip_prompt <line> → 去「」包裹：以「开头则取到首个」为止（允许尾部备注）
scn_strip_prompt() {
	local line="$1"
	case "$line" in
		「*) line="${line#「}"; line="${line%%」*}" ;;
	esac
	printf '%s\n' "$line"
}

# scn_first_prompt <name> → prompts.md 第 1 条有序列表提示词；无则空
scn_first_prompt() {
	local f="$ARUING_SCN_DIR/$1/prompts.md" line
	[ -f "$f" ] || return 0
	line="$(awk '/^[[:space:]]*[0-9]+\.[[:space:]]/ { sub(/^[[:space:]]*[0-9]+\.[[:space:]]*/, ""); print; exit }' "$f")"
	scn_strip_prompt "$line"
}

# scn_case_prompts <case-dir> → 全部有序列表提示词（每行一条）
scn_case_prompts() {
	local f="$1/prompts.md"
	[ -f "$f" ] || return 0
	awk '/^[[:space:]]*[0-9]+\.[[:space:]]/ { sub(/^[[:space:]]*[0-9]+\.[[:space:]]*/, ""); print }' "$f" | while IFS= read -r l; do scn_strip_prompt "$l"; done
}

# run_step <label> <logfile> <cmd...> → 执行并落 log；打印 ok/FAIL；返回命令退出码
run_step() {
	local label="$1" log="$2"; shift 2
	if "$@" >>"$log" 2>&1; then
		echo "$label=ok"
		return 0
	else
		echo "$label=FAIL (log: $log)"
		return 1
	fi
}

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

# LLM 配置：ARUING_CONFIG 或 playground/config.yaml（chat 须 LLM，无配置必失败）
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

# ---------- 场景枚举（可选参数过滤） ----------

declare -a names
while IFS= read -r n; do
	if [[ $# -gt 0 ]]; then
		want=0
		for a in "$@"; do [[ "$a" == "$n" ]] && want=1; done
		[[ $want -eq 1 ]] && names+=("$n")
	else
		names+=("$n")
	fi
done < <(scn_known_scenarios)

if [[ ${#names[@]} -eq 0 ]]; then
	if [[ $# -gt 0 ]]; then
		echo "smoke-all: no matching scenarios among: $* (known: make lab-list)"
	else
		echo "smoke-all: no known scenarios (directories with manifests/ under scenarios/)"
	fi
	exit 2
fi

echo "smoke-all: scenarios → ${names[*]}"
echo

# ---------- 执行 ----------

logs_dir="$ARUING_SCN_DIR/.smoke"
mkdir -p "$logs_dir"

declare -a rows
overall=0

# 场景内逐 case 执行（有 cases/ 时调用）。返回 0 = 该场景全部 case 通过。
run_cases() { # <scenario>
	local scn="$1"
	local cases_dir="$ARUING_SCN_DIR/$scn/cases" all_ok=0
	local cdir cbase log apply_dir f n_apply apply_fail
	local sid msg total chat_ok

	while IFS= read -r cdir; do
		cbase="$(basename "$cdir")"
		echo "-- case: $cbase"
		log="$logs_dir/$scn-$cbase.log"
		: >"$log"

		# 可选 apply/：case 开始前追加故障清单（kubectl apply -f，逐文件）
		apply_dir="$cdir/apply"
		n_apply=0 apply_fail=0
		if [[ -d "$apply_dir" ]]; then
			while IFS= read -r f; do
				n_apply=$((n_apply + 1))
				run_step "apply[$(basename "$f")]" "$log" \
					env KUBECONFIG="$PWD/scenarios/.kube/$scn.yaml" kubectl apply -f "$f" || apply_fail=1
			done < <(find "$apply_dir" -maxdepth 1 -type f \( -name '*.yaml' -o -name '*.yml' \) | sort)
		fi
		[[ $apply_fail -eq 0 ]] || all_ok=1

		# 逐条 prompt 同 session 续聊：单进程 stdin 行模式（MemoryStore 进程内，
		# 跨进程 --session 不共享；非 tty stdin 下 chat 逐行同会话跑 Turn）
		total=0 chat_ok=0
		prompts_tmp="$(mktemp)"
		while IFS= read -r msg; do
			[[ -z "$msg" ]] && continue
			total=$((total + 1))
			echo "-- chat prompt: $msg" >>"$log"
			printf '%s\n' "$msg" >>"$prompts_tmp"
		done < <(scn_case_prompts "$cdir")
		chat_ok=0
		if [[ $total -gt 0 ]]; then
			echo "exit" >>"$prompts_tmp"
			run_step chat "$log" env KUBECONFIG="$PWD/scenarios/.kube/$scn.yaml" \
				"$PWD/bin/aruing" chat <"$prompts_tmp" && chat_ok=1 || all_ok=1
		fi
		rm -f "$prompts_tmp"

		apply_state="ok"
		[[ $apply_fail -eq 0 ]] || apply_state="FAIL"
		[[ $n_apply -eq 0 && $apply_fail -eq 0 ]] && apply_state="none"
		chat_state="FAIL"
		[[ $chat_ok -eq 1 ]] && chat_state="ok($total prompts)"
		[[ $total -eq 0 ]] && { chat_state="SKIP"; all_ok=1; }
		rows+=("$scn/$cbase apply=$apply_state($n_apply) chat=$chat_state")
		[[ "$chat_state" == "SKIP" ]] && echo "chat=SKIP (no prompts in case prompts.md)"
		echo
	done < <(find "$cases_dir" -mindepth 1 -maxdepth 1 -type d | sort)

	# cases/ 存在但零 case 目录：顶层默认 case 已被跳过，等于什么都没验收 → 判失败（pr-agent #83）
	if ! find "$cases_dir" -mindepth 1 -maxdepth 1 -type d | grep -q .; then
		echo "case=FAIL (cases/ exists but has no case directories)"
		rows+=("$scn cases=EMPTY chat=SKIP")
		return 1
	fi

	return $all_ok
}

for name in "${names[@]}"; do
	echo "== scenario: $name =="

	# 有 cases/ → 顶层不再作为默认 case 跑（基底只起集群）
	has_cases=0
	[[ -d "$ARUING_SCN_DIR/$name/cases" ]] && has_cases=1

	up_ok=0 down_ok=0 scn_ok=0
	log="$logs_dir/$name.log"
	: >"$log"

	# fresh-up：已存在的集群先拆（保证重启后严格符合 manifests；残留 case apply 一并清除）。
	# 拆除失败不得静默复用脏集群（pr-agent #83）：仍存在则该场景判 up=FAIL 跳过。
	cluster="$(scn_cluster_name "$name")"
	if scn_kind_exists "$cluster"; then
		echo "fresh-up: removing pre-existing cluster '$cluster'"
		bash "$PWD/scripts/scenario-down.sh" "$name" >>"$log" 2>&1 || true
		if scn_kind_exists "$cluster"; then
			echo "up=FAIL (fresh-up teardown failed; cluster '$cluster' still exists — refusing to reuse stale state)"
			if [[ $has_cases -eq 1 ]]; then
				rows+=("$name up=FAIL(fresh-up) all-cases=SKIP down=SKIP")
			else
				rows+=("$name up=FAIL(fresh-up) chat=SKIP down=SKIP")
			fi
			overall=1
			echo
			continue
		fi
	fi
	run_step up "$log" bash "$PWD/scripts/scenario-up.sh" "$name" && up_ok=1

	if [[ $up_ok -eq 1 ]]; then
		if [[ $has_cases -eq 1 ]]; then
			run_cases "$name" || scn_ok=1
		else
			# 默认 case：只发第 1 条提示词（与旧版行为一致，回归）
			msg="$(scn_first_prompt "$name")"
			chat_state="FAIL"
			if [[ -z "$msg" ]]; then
				echo "chat=SKIP (no prompt in prompts.md)"
				scn_ok=1
				chat_state="SKIP"
			elif run_step chat "$log" env KUBECONFIG="$PWD/scenarios/.kube/$name.yaml" \
				"$PWD/bin/aruing" chat "$msg"; then
				chat_state="ok"
			else
				scn_ok=1
			fi
		fi
		run_step down "$log" bash "$PWD/scripts/scenario-down.sh" "$name" && down_ok=1
		if [[ $has_cases -eq 1 ]]; then
			rows+=("$name up=ok down=$([[ $down_ok -eq 1 ]] && echo ok || echo FAIL) (cases above)")
		else
			rows+=("$name up=ok chat=$chat_state down=$([[ $down_ok -eq 1 ]] && echo ok || echo FAIL)")
		fi
	else
		echo "chat=SKIP (up failed)"
		if [[ $has_cases -eq 1 ]]; then
			rows+=("$name up=FAIL all-cases=SKIP down=SKIP")
		else
			rows+=("$name up=FAIL chat=SKIP down=SKIP")
		fi
	fi

	[[ $up_ok -eq 1 ]] || overall=1
	[[ $down_ok -eq 1 ]] || overall=1
	[[ $scn_ok -eq 0 ]] || overall=1
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
