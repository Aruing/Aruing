#!/usr/bin/env bash
# bench-run —— 0.1.5 评测基准统一跑批入口（benchmark 步骤 1）
#
# 一条命令跑完整基准：逐场景 fresh-up 起/拆 kind 集群 → 单轮诊断维（eval-sweep）
# → 长会话维（probe-sweep，须场景有 probe.yaml）→ 聚合逐场景/逐方法汇总报告。
# 单元循环、断点续跑守卫、判分全部复用两个 sweep 的原生语义（env 子集是其官方
# 口径）；本脚本只做集群生命周期编排与聚合，零 Go 改动。
#
# 每场景独立子 OUT（diag/<scn>/ probe/<scn>/）：两个 sweep 的汇总 CSV 与 manifest
# 是批级单文件，共用一个 OUT 会被后一次调用覆盖——子目录隔离后聚合时再合并。
#
# 用法：
#   make bench-run DRYRUN=1                          # 干跑：打印计划与单元命令，零集群零 LLM
#   make bench-run                                    # 真跑（须 Docker/kind/kubectl + LLM 配置；先 DRYRUN 核对成本）
#   scripts/bench-run.sh SCENARIOS="crashloop-bad-image" DIMS=probe OUT=/tmp/b1
#   scripts/bench-run.sh AGG_ONLY=1 OUT=<既有批目录>   # 只重聚合（补 summary，不碰集群不跑单元）
#
# 输出（OUT 默认 eval/results/bench/<UTC 时间戳>）：
#   manifest.json            顶层批档案（git/model/kind·kubectl 版本/矩阵参数）
#   diag/<scn>/ probe/<scn>/ 两个 sweep 原生布局（records/、judge-*.json、CSV）
#   summary.md / summary.csv 聚合报告（人读 / 机器可联结）
#   failures.txt             场景级失败清单（up/down/sweep/记录缺失，全量记录不静默）
#   bench-run.stdout.log     生命周期输出（fresh-up/up/down）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
. "$ROOT/scenarios/lib/common.sh" # scn_die / scn_known_scenarios / scn_kind_exists / scn_cluster_name / scn_resolve_name

DIMS="${DIMS:-both}" # both | diag | probe
OUT="${OUT:-$ROOT/eval/results/bench/$(date -u +%Y%m%d-%H%M%S)}"
CONFIG="${CONFIG:-playground/config.yaml}"
ARUING="${ARUING:-go run ./cmd/aruing}"
DRYRUN="${DRYRUN:-0}"
FORCE="${FORCE:-0}"
AGG_ONLY="${AGG_ONLY:-0}"
# 生命周期脚本可覆盖（stub 测试注入用）；默认真脚本
UP_SCRIPT="${UP_SCRIPT:-$ROOT/scripts/scenario-up.sh}"
DOWN_SCRIPT="${DOWN_SCRIPT:-$ROOT/scripts/scenario-down.sh}"

case "$DIMS" in
both | diag | probe) ;;
*) scn_die "DIMS must be one of: both | diag | probe (got: $DIMS)" ;;
esac
want_diag() { [ "$DIMS" = "both" ] || [ "$DIMS" = "diag" ]; }
want_probe() { [ "$DIMS" = "both" ] || [ "$DIMS" = "probe" ]; }

# 场景清单：默认全部已知；用户给定则逐个校验（manifests/ 存在）
if [ -n "${SCENARIOS:-}" ]; then
    for s in $SCENARIOS; do scn_resolve_name "$s" >/dev/null; done
else
    SCENARIOS="$(scn_known_scenarios | tr '\n' ' ')"
    [ -n "$SCENARIOS" ] || scn_die "no known scenarios (directories with manifests/ under scenarios/)"
fi

overall=0

fail() { # 场景级失败：stderr 必达；清单写入尽力而为（目录缺失/不可写不得反过来中止批，#18）
    printf '  FAIL %s\n' "$*" >&2
    { printf '  FAIL %s\n' "$*" >>"$OUT/failures.txt"; } 2>/dev/null || true
    overall=1
}

preflight() {
    local missing=0 b
    for b in docker kind kubectl python3; do # python3：manifest 写入与聚合的硬依赖
        command -v "$b" >/dev/null 2>&1 || { echo "preflight: missing tool: $b"; missing=1; }
    done
    if [ ! -f "$CONFIG" ]; then
        echo "preflight: missing LLM config: $CONFIG"
        missing=1
    fi
    [ "$missing" -eq 0 ] || scn_die "preflight FAILED — fix the missing items above, then retry."
    echo "preflight: OK (config → $CONFIG)"
}

# run_step <label> <cmd...> → 执行生命周期命令，输出落批日志；打印 ok/FAIL
run_step() {
    local label="$1"
    shift
    if "$@" >>"$OUT/bench-run.stdout.log" 2>&1; then
        echo "  $label=ok"
        return 0
    fi
    echo "  $label=FAIL (log: $OUT/bench-run.stdout.log)" >&2
    return 1
}

# run_sweep <dim> <script> <outdir> <scn> → 以单场景子集调用 sweep（参数透传，
# 未设参数传空串 → sweep 侧 ${VAR:-default} 回落自身默认）；非零退出记失败不中断
run_sweep() {
    local dim="$1" script="$2" outdir="$3" scn="$4"
    echo "---- $dim ← $(basename "$script") (OUT=$outdir)"
    if ! SCENARIOS="$scn" OUT="$outdir" DRYRUN="$DRYRUN" FORCE="$FORCE" \
        METHODS="${METHODS:-}" KS="${KS:-}" ROUNDS="${ROUNDS:-}" REPS="${REPS:-}" \
        CONFIG="$CONFIG" ARUING="$ARUING" \
        bash "$script"; then
        fail "sweep-fail scenario=$scn dim=$dim ($(basename "$script") 非零退出，详见 $outdir/run.stderr.log)"
    fi
}

write_manifest() {
    local kind_v kubectl_v model_v
    kind_v="$(kind version 2>/dev/null | head -1 | tr -d '\r')"
    kubectl_v="$(kubectl version --client 2>/dev/null | head -1 | tr -d '\r')"
    model_v="$(grep -E '^[[:space:]]*model:' "$CONFIG" 2>/dev/null | head -1 | sed 's/^[[:space:]]*model:[[:space:]]*//' | tr -d '"')"
    python3 - "$OUT" \
        "$(git -C "$ROOT" describe --always --dirty 2>/dev/null || echo unknown)" \
        "$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)" \
        "${model_v:-unknown}" "${kind_v:-unknown}" "${kubectl_v:-unknown}" \
        "$CONFIG" "$DIMS" "$SCENARIOS" \
        "${METHODS:-(sweep default)}" "${KS:-(sweep default)}" \
        "${ROUNDS:-(sweep default)}" "${REPS:-(sweep default)}" \
        "$ARUING" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" <<'PY'
import json, sys

out = sys.argv[1]
man = {
    "tool": "bench-run",
    "git": sys.argv[2],
    "commit": sys.argv[3],
    "model": sys.argv[4],
    "kind": sys.argv[5],
    "kubectl": sys.argv[6],
    "config": sys.argv[7],
    "dims": sys.argv[8],
    "scenarios": sys.argv[9],
    "methods": sys.argv[10],
    "ks": sys.argv[11],
    "rounds": sys.argv[12],
    "reps": sys.argv[13],
    "aruing": sys.argv[14],
    "started": sys.argv[15],
}
with open(out + "/manifest.json", "w", encoding="utf-8") as f:
    json.dump(man, f, ensure_ascii=False, indent=2)
PY
}

aggregate() { # 聚合两维 CSV → summary.md（人读）+ summary.csv（机器可联结）；无记录也写失败清单
    python3 - "$OUT" <<'PY'
import csv, glob, json, os, sys

out = sys.argv[1]

man = {}
man_path = os.path.join(out, "manifest.json")
if os.path.exists(man_path):
    with open(man_path, encoding="utf-8") as f:
        man = json.load(f)


def read_rows(pattern):
    rows = []
    for p in sorted(glob.glob(os.path.join(out, pattern))):
        with open(p, newline="", encoding="utf-8") as f:
            for r in csv.DictReader(f):
                rows.append(r)
    return rows


def num(x, default=0.0):
    try:
        return float(x)
    except (TypeError, ValueError):
        return default


def istrue(x):
    return str(x).strip().lower() == "true"


diag_rows = read_rows(os.path.join("diag", "*", "eval-sweep.csv"))
probe_rows = read_rows(os.path.join("probe", "*", "probe-summary.csv"))

L = ["# aruing benchmark 汇总"]
if man:
    L += [
        "",
        f"- 时间：{man.get('started', 'unknown')} · 工具：bench-run",
        f"- 代码：{man.get('git', 'unknown')} ({str(man.get('commit', ''))[:12]})",
        f"- 模型：{man.get('model', 'unknown')} · {man.get('kind', '?')} · {man.get('kubectl', '?')}",
        f"- 矩阵：维度 {man.get('dims', '?')} · 场景 [{man.get('scenarios', '')}] · 方法 [{man.get('methods', '')}]"
        f" · K [{man.get('ks', '')}] · 轮数 [{man.get('rounds', '')}] · 重复 {man.get('reps', '')}",
    ]

# 单轮诊断：按 (场景, 方法) 聚合
groups = {}
for r in diag_rows:
    groups.setdefault((r.get("scenario", ""), r.get("method", "")), []).append(r)
if groups:
    L += [
        "",
        "## 单轮诊断（diag）",
        "",
        "| 场景 | 方法 | 根因命中 | 引用违规 | 完成 | 平均轮数 | tokens in/out |",
        "| --- | --- | --- | --- | --- | --- | --- |",
    ]
    for (scn, m), rs in sorted(groups.items()):
        n = len(rs)
        hits = sum(1 for r in rs if istrue(r.get("root_cause_hit")))
        cit = int(sum(num(r.get("citation_violations")) for r in rs))
        comp = sum(1 for r in rs if istrue(r.get("completed")))
        avg_rounds = sum(num(r.get("rounds")) for r in rs) / n
        ti = int(sum(num(r.get("tokens_in")) for r in rs))
        to = int(sum(num(r.get("tokens_out")) for r in rs))
        L.append(f"| {scn} | {m} | {hits}/{n} | {cit} | {comp}/{n} | {avg_rounds:.1f} | {ti}/{to} |")

# 长会话：按 (场景, 方法, 轮数) 聚合
groups = {}
for r in probe_rows:
    groups.setdefault((r.get("scenario", ""), r.get("method", ""), r.get("rounds", "")), []).append(r)
if groups:
    L += [
        "",
        "## 长会话（probe）",
        "",
        "| 场景 | 记忆方法 | 轮数 | 会话数 | 诊断完成 | 探针命中/计分 | 平均成功率 | tokens |",
        "| --- | --- | --- | --- | --- | --- | --- | --- |",
    ]
    for (scn, m, nr), rs in sorted(groups.items()):
        n = len(rs)
        dtot = int(sum(num(r.get("diagnose_total")) for r in rs))
        dcomp = int(sum(num(r.get("diagnose_completed")) for r in rs))
        ph = int(sum(num(r.get("probe_hits")) for r in rs))
        ps = int(sum(num(r.get("probe_scored")) for r in rs))
        sr = sum(num(r.get("success_rate")) for r in rs) / n
        tk = int(sum(num(r.get("tokens_total")) for r in rs))
        L.append(f"| {scn} | {m} | {nr} | {n} | {dcomp}/{dtot} | {ph}/{ps} | {sr:.0%} | {tk} |")

fail_path = os.path.join(out, "failures.txt")
fails = []
if os.path.exists(fail_path):
    with open(fail_path, encoding="utf-8") as f:
        fails = [l.strip() for l in f if l.strip()]
L += ["", "## 失败清单", ""]
L += [f"- {f}" for f in fails] if fails else ["无"]

with open(os.path.join(out, "summary.md"), "w", encoding="utf-8") as f:
    f.write("\n".join(L) + "\n")

all_rows = [dict(r, dim="diag") for r in diag_rows] + [dict(r, dim="probe") for r in probe_rows]
if all_rows:
    cols = ["dim"]
    for r in all_rows:
        for k in r:
            if k not in cols:
                cols.append(k)
    with open(os.path.join(out, "summary.csv"), "w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=cols, restval="")
        w.writeheader()
        w.writerows(all_rows)
print(f"summary: {len(all_rows)} 行（diag {len(diag_rows)} / probe {len(probe_rows)}）")
print(f"报告 → {os.path.join(out, 'summary.md')}")

# 聚合退出码：失败清单非空 → 非 0（与批级退出语义一致）
sys.exit(1 if fails else 0)
PY
}

echo "bench-run: DIMS=$DIMS OUT=$OUT"
echo "bench-run: scenarios → $SCENARIOS"

if [ "$AGG_ONLY" = "1" ]; then
    [ -d "$OUT" ] || scn_die "AGG_ONLY=1 but OUT not found: $OUT"
    aggregate || { echo "bench-run: 失败清单非空（见 summary.md）" >&2; exit 1; }
    echo "bench-run: 聚合完成（AGG_ONLY，未跑单元未碰集群）"
    exit 0
fi

if [ "$DRYRUN" = "1" ]; then
    # 干跑：只打印计划 + 透传两个 sweep 的干跑输出（单元命令与计数），零集群零 LLM；
    # 目录尽力建（fail() 记清单用；不可创建时清单只进 stderr）；干跑中记录到失败同样影响退出码
    mkdir -p "$OUT" 2>/dev/null || true
    for scn in $SCENARIOS; do
        plan="fresh-up → up"
        if want_diag; then plan="$plan → diag"; fi
        if want_probe; then plan="$plan → probe"; fi
        plan="$plan → down"
        echo "[plan] $scn: $plan"
        if want_diag; then
            run_sweep diag "$ROOT/scripts/eval-sweep.sh" "$OUT/diag/$scn" "$scn"
        fi
        if want_probe; then
            if [ -f "$ROOT/scenarios/$scn/probe.yaml" ]; then
                run_sweep probe "$ROOT/scripts/probe-sweep.sh" "$OUT/probe/$scn" "$scn"
            else
                echo "  probe: SKIP（$scn 无 probe.yaml）"
            fi
        fi
        echo
    done
    echo "DRYRUN：未执行任何集群操作与 LLM 调用" >&2
    exit "$overall"
fi

# ---------- 真跑 ----------
preflight
mkdir -p "$OUT"
: >"$OUT/failures.txt"
: >"$OUT/bench-run.stdout.log"
write_manifest
echo "manifest → $OUT/manifest.json"

for scn in $SCENARIOS; do
    echo "== scenario: $scn =="

    # fresh-up：残留集群先拆；拆除失败不得静默复用脏集群（smoke-all 同款语义）
    cluster="$(scn_cluster_name "$scn")"
    if scn_kind_exists "$cluster"; then
        echo "  fresh-up: removing pre-existing cluster '$cluster'"
        run_step fresh-down bash "$DOWN_SCRIPT" "$scn" || true
        if scn_kind_exists "$cluster"; then
            fail "up-FAIL scenario=$scn (fresh-up teardown failed; cluster '$cluster' still exists — refusing to reuse stale state)"
            continue
        fi
    fi

    if run_step up bash "$UP_SCRIPT" "$scn"; then
        if want_diag; then
            run_sweep diag "$ROOT/scripts/eval-sweep.sh" "$OUT/diag/$scn" "$scn"
            [ -s "$OUT/diag/$scn/eval-sweep.csv" ] ||
                fail "records-missing scenario=$scn dim=diag (eval-sweep.csv 缺失或为空)"
        fi
        if want_probe; then
            if [ -f "$ROOT/scenarios/$scn/probe.yaml" ]; then
                run_sweep probe "$ROOT/scripts/probe-sweep.sh" "$OUT/probe/$scn" "$scn"
                [ -s "$OUT/probe/$scn/probe-summary.csv" ] ||
                    fail "records-missing scenario=$scn dim=probe (probe-summary.csv 缺失或为空)"
            else
                echo "  probe: SKIP（$scn 无 probe.yaml）"
            fi
        fi
        run_step down bash "$DOWN_SCRIPT" "$scn" || fail "down-FAIL scenario=$scn"
    else
        # up 可能半建成集群（kind create 成功而后续步骤失败）：best-effort 拆除防泄漏；
        # 拆除失败不掩盖原始失败（残留由下次 fresh-up 兑底清除）
        if scn_kind_exists "$cluster"; then
            run_step best-effort-down bash "$DOWN_SCRIPT" "$scn" || true
        fi
        fail "up-FAIL scenario=$scn (skip both dims; see $OUT/bench-run.stdout.log)"
    fi
    echo
done

aggregate || overall=1
if [ "$overall" -eq 0 ]; then
    echo "bench-run: ALL OK → $OUT/summary.md"
else
    echo "bench-run: FAILURES recorded in $OUT/failures.txt（详见 $OUT/summary.md 失败清单）" >&2
fi
exit "$overall"
