#!/usr/bin/env bash
# bench-run —— 0.1.5 评测基准统一跑批入口（benchmark 步骤 1 + 步骤 2）
#
# 一条命令跑完整基准：逐场景 fresh-up 起/拆 kind 集群 → 单轮诊断维（eval-sweep）
# → 长会话维（probe-sweep，须场景有 probe.yaml）→ 聚合逐场景/逐方法汇总报告。
# 单元循环、断点续跑守卫、判分全部复用两个 sweep 的原生语义（env 子集是其官方
# 口径）；本脚本只做集群生命周期编排与聚合，零 Go 改动。
#
# 每场景独立子 OUT（diag/<scn>/ probe/<scn>/）：两个 sweep 的汇总 CSV 与 manifest
# 是批级单文件，共用一个 OUT 会被后一次调用覆盖——子目录隔离后聚合时再合并。
#
# ③层抽样（步骤 2）：diag 维每场景 judge --sample-total 逐场景池化抽 RUBRIC_N 行
# （默认 20，0 = 跳过）落 diag/<scn>/rubric-<scn>.json；默认产出待回填表（人工三值
# 评分作业面），RUBRIC_LLM=1 时同模型逐行先评；断点守卫同款（文件存在跳过）。
# probe 维不抽（judge --probe 与抽样互斥是既有防线，内嵌诊断复判已覆盖①②层）。
#
# 开源清洗（步骤 2）：manifest config/aruing 写时归一（绝对路径不进产物：ROOT 内
# 剥成相对、其余取 basename）；两级 sweep manifest 的 config 字段后处理重写（sweep
# 脚本零改动）；聚合前 scrub 自检门扫 OUT 下非 .log 文件，命中私人路径字面量
# （$HOME / $ROOT / 绝对 OUT）记 scrub-FAIL 入清单并使批非零退出。
# *.log 为调试产物不进开源集，排除清单：bench-run.stdout.log、run.rubric.stderr.log、
# diag/*/run.stderr.log、probe/*/{run.stderr,probe.stdout}.log。
#
# 场景级 chat-env 注入（步骤 3）：逐场景把 scenarios/<scn>/chat-env 的 K=V 经 env
# 前缀注入两维 sweep 调用于进程环境（单元继承生效，sweep 脚本零改动）——基准按
# 场景声明的验收配置跑（如 bigtable-fleet 的 map-reduce 投影），无 chat-env 场景
# 零注入；注入同时落 $OUT/scenario-env.txt 并进 summary.md 头部（留痕可复现）
#
# 用法：
#   make bench-run DRYRUN=1                          # 干跑：打印计划与单元命令，零集群零 LLM
#   make bench-run                                    # 真跑（须 Docker/kind/kubectl + LLM 配置；先 DRYRUN 核对成本）
#   scripts/bench-run.sh SCENARIOS="crashloop-bad-image" DIMS=probe OUT=/tmp/b1
#   scripts/bench-run.sh RUBRIC_N=30 RUBRIC_LLM=1    # ③层 LLM 辅助评，30 行/场景
#   scripts/bench-run.sh AGG_ONLY=1 OUT=<既有批目录>   # 只重聚合（补 summary，不碰集群不跑单元）
#
# 输出（OUT 默认 eval/results/bench/<UTC 时间戳>）：
#   manifest.json            顶层批档案（git/model/kind·kubectl 版本/矩阵参数/③层参数）
#   diag/<scn>/ probe/<scn>/ 两个 sweep 原生布局（records/、judge-*.json、CSV）
#   diag/<scn>/rubric-<scn>.json  ③层抽样评分表（待回填或 LLM 已评）
#   summary.md / summary.csv 聚合报告（人读 / 机器可联结）
#   rubric-summary.csv       ③层逐场景分布（机器可联结）
#   failures.txt             场景级失败清单（up/down/sweep/rubric/记录缺失/scrub，全量记录不静默）
#   scenario-env.txt         场景级 chat-env 注入留痕（<scn>\tK=V，有注入才建）
#   bench-run.stdout.log     生命周期输出（fresh-up/up/down；调试产物不进开源集）
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
# ③层抽样（步骤 2）：RUBRIC_N=0 跳过；RUBRIC_LLM=1 时 judge --rubric-llm 同模型逐行评
RUBRIC_N="${RUBRIC_N:-20}"
RUBRIC_SEED="${RUBRIC_SEED:-0}"
RUBRIC_LLM="${RUBRIC_LLM:-0}"
# 生命周期脚本可覆盖（stub 测试注入用）；默认真脚本
UP_SCRIPT="${UP_SCRIPT:-$ROOT/scripts/scenario-up.sh}"
DOWN_SCRIPT="${DOWN_SCRIPT:-$ROOT/scripts/scenario-down.sh}"

case "$DIMS" in
both | diag | probe) ;;
*) scn_die "DIMS must be one of: both | diag | probe (got: $DIMS)" ;;
esac
case "$RUBRIC_LLM" in
0 | 1) ;;
*) scn_die "RUBRIC_LLM must be 0 or 1 (got: $RUBRIC_LLM)" ;;
esac
case "$RUBRIC_N" in
'' | *[!0-9]* | 0?*) scn_die "RUBRIC_N must be a decimal integer without leading zeros (got: $RUBRIC_N)" ;;
esac
# 种子允许负号；剥去后同样禁止非数字与前导零（Go flag 对 08 等按 base-0 八进制解析报错，
# 校验前置到入口，不让它海到 judge 才以 rubric-fail 暴露）
_RUBRIC_SEED_ABS="${RUBRIC_SEED#-}"
case "$_RUBRIC_SEED_ABS" in
'' | *[!0-9]* | 0?*) scn_die "RUBRIC_SEED must be a decimal integer without leading zeros (got: $RUBRIC_SEED)" ;;
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

# canon_path <path> → 开源清洗写时归一：绝对路径——ROOT 内剥前缀取相对，其余取
# basename；相对值原样（"go run ./cmd/aruing" 等非路径值不受影响）
canon_path() {
    case "$1" in
    "$ROOT"/*) echo "${1#"$ROOT"/}" ;;
    /*) echo "${1##*/}" ;;
    *) echo "$1" ;;
    esac
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

# chat_env_lines <scn> → 场景 chat-env（KEY=VALUE 行，# 注释跳过）逐行输出；无文件输出空
# 与 smoke-all 的 chat_env_args 同源语义：值不含空白（scenarios/README 约定）
chat_env_lines() {
    local f="$ROOT/scenarios/$1/chat-env" line
    [ -f "$f" ] || return 0
    while IFS= read -r line; do
        case "$line" in '' | \#*) continue ;; esac
        printf '%s\n' "$line"
    done <"$f"
}

# emit_chat_env <scn> <inject> → 打印场景级注入行；真跑时追加 scenario-env.txt 留痕
# （干跑零文件写纪律，只打印不落盘；inject 为空直接返回）
emit_chat_env() {
    local scn="$1" inject="${2:-}"
    [ -n "$inject" ] || return 0
    echo "  chat-env: 注入 $(echo $inject)"
    if [ "$DRYRUN" != "1" ]; then
        printf '%s\t%s\n' "$scn" "$(echo $inject)" >>"$OUT/scenario-env.txt"
    fi
}

# run_sweep <dim> <script> <outdir> <scn> <inject> → 以单场景子集调用 sweep（参数透传，
# 未设参数传空串 → sweep 侧 ${VAR:-default} 回落自身默认）；场景 chat-env 经 env 前缀
# 注入子进程环境（空注入 = env 直传，行为不变；值不含空白为 scenarios/README 既有约定，
# smoke-all 同款先例）；非零退出记失败不中断；
# 失败消息日志引用一律 OUT 相对路径（开源清洗：绝对 OUT 不进产物）
run_sweep() {
    local dim="$1" script="$2" outdir="$3" scn="$4" inject="${5:-}"
    echo "---- $dim ← $(basename "$script") (OUT=$outdir)"
    if ! env $inject SCENARIOS="$scn" OUT="$outdir" DRYRUN="$DRYRUN" FORCE="$FORCE" \
        METHODS="${METHODS:-}" KS="${KS:-}" ROUNDS="${ROUNDS:-}" REPS="${REPS:-}" \
        CONFIG="$CONFIG" ARUING="$ARUING" \
        bash "$script"; then
        fail "sweep-fail scenario=$scn dim=$dim ($(basename "$script") 非零退出，详见 $dim/$scn/run.stderr.log)"
    fi
}

# run_rubric <scn> → ③层抽样：diag 维记录逐场景池化抽 RUBRIC_N 行出评分表（默认
# 待回填；RUBRIC_LLM=1 同模型逐行评）；judge 非零退出（含全失败记录 no-pairs）记
# 失败不中断（#18）；probe 维不抽（judge --probe 与抽样互斥是既有防线）
run_rubric() {
    local scn="$1" recdir rubric llm
    [ "$RUBRIC_N" -gt 0 ] || { echo "  rubric: SKIP（RUBRIC_N=0）"; return 0; }
    recdir="$OUT/diag/$scn/records/$scn"
    rubric="$OUT/diag/$scn/rubric-$scn.json"
    # 断点续跑守卫：抽样表已存在且非 FORCE 则跳过（重抽：FORCE=1 或删文件）
    if [ "$FORCE" != "1" ] && [ -s "$rubric" ]; then
        echo "  rubric: 已有抽样表，跳过（重抽：FORCE=1）"
        return 0
    fi
    # 无 diag 记录不抽：根因已由 records-missing 记录，不双记
    if [ "$DRYRUN" != "1" ] && ! ls "$recdir"/*.json >/dev/null 2>&1; then
        echo "  rubric: SKIP（$scn 无 diag 记录）"
        return 0
    fi
    # 可选旗标走数组引号展开：CONFIG 含空格时仍为单参数（防 word-split 注入）
    local judge_args=(judge --run-json "$recdir" --scenario "$ROOT/scenarios/$scn/scenario.yaml"
        --sample-total "$RUBRIC_N" --seed "$RUBRIC_SEED")
    [ "$RUBRIC_LLM" = "1" ] && judge_args+=(--rubric-llm --config "$CONFIG")
    if [ "$DRYRUN" = "1" ]; then
        printf '  rubric: %s %s > diag/%s/rubric-%s.json\n' \
            "$ARUING" "${judge_args[*]}" "$scn" "$scn"
        return 0
    fi
    if $ARUING "${judge_args[@]}" >"$rubric" 2>>"$OUT/run.rubric.stderr.log"; then
        echo "  rubric=ok → diag/$scn/rubric-$scn.json"
    else
        rm -f "$rubric" # 失败不残留半截文件（断点续跑不误判已抽）
        fail "rubric-fail scenario=$scn (judge --sample-total 非零退出——全失败记录无对可抽亦走此路；详见 run.rubric.stderr.log)"
    fi
}

write_manifest() {
    local kind_v kubectl_v model_v
    # 环境探测命令替换一律 || true 兜底：探测失败/无匹配时记 unknown 继续，
    # 不得反向中止批（set -euo pipefail 下 grep 无匹配即非零）
    kind_v="$(kind version 2>/dev/null | head -1 | tr -d '\r' || true)"
    kubectl_v="$(kubectl version --client 2>/dev/null | head -1 | tr -d '\r' || true)"
    model_v="$(grep -E '^[[:space:]]*model:' "$CONFIG" 2>/dev/null | head -1 | sed 's/^[[:space:]]*model:[[:space:]]*//' | tr -d '"' || true)"
    python3 - "$OUT" \
        "$(git -C "$ROOT" describe --always --dirty 2>/dev/null || echo unknown)" \
        "$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)" \
        "${model_v:-unknown}" "${kind_v:-unknown}" "${kubectl_v:-unknown}" \
        "$(canon_path "$CONFIG")" "$DIMS" "$SCENARIOS" \
        "${METHODS:-(sweep default)}" "${KS:-(sweep default)}" \
        "${ROUNDS:-(sweep default)}" "${REPS:-(sweep default)}" \
        "$(canon_path "$ARUING")" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        "$RUBRIC_N" "$RUBRIC_SEED" "$RUBRIC_LLM" <<'PY'
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
    "rubric_n": int(sys.argv[16]),
    "rubric_seed": int(sys.argv[17]),
    "rubric_llm": int(sys.argv[18]),
}
with open(out + "/manifest.json", "w", encoding="utf-8") as f:
    json.dump(man, f, ensure_ascii=False, indent=2)
PY
}

# rewrite_sweep_manifests → 开源清洗后处理：两级 sweep manifest（diag/*/manifest.json、
# probe/*/manifest.json）的 config 字段归一（同 canon_path 口径）；sweep 脚本零改动；
# 单份 json 损坏跳过并警告，不中止批
rewrite_sweep_manifests() {
    python3 - "$ROOT" "$OUT" <<'PY'
import glob, json, os, sys

root, out = sys.argv[1], sys.argv[2]


def canon(p):
    if p.startswith(root + os.sep):
        return p[len(root) + 1:]
    if p.startswith("/"):
        return p.rsplit("/", 1)[-1]
    return p


paths = glob.glob(os.path.join(out, "diag", "*", "manifest.json"))
paths += glob.glob(os.path.join(out, "probe", "*", "manifest.json"))
changed = skipped = 0
for mp in sorted(paths):
    try:
        with open(mp, encoding="utf-8") as f:
            man = json.load(f)
    except (OSError, ValueError) as e:
        print(f"scrub: WARN 跳过无法解析的 {os.path.relpath(mp, out)}: {e}", file=sys.stderr)
        skipped += 1
        continue
    if "config" in man and man["config"] != canon(man["config"]):
        man["config"] = canon(man["config"])
        with open(mp, "w", encoding="utf-8") as f:
            json.dump(man, f, ensure_ascii=False, indent=2)
        changed += 1
print(f"scrub: sweep manifest config 字段归一（改写 {changed} 份，跳过 {skipped} 份）")
PY
}

# scrub_check → 开源清洗自检门：扫 OUT 下全部非 .log 文件（字面子串匹配，ROOT 含
# 正则元字符也不误判），命中 $HOME / $ROOT /（OUT 为绝对时）$OUT → scrub-FAIL 入
# failures.txt（去重，AGG_ONLY 重跑不双记）并返回非零；*.log 为调试产物不在扫描集
scrub_check() {
    python3 - "$ROOT" "$OUT" <<'PY'
import os, sys

root, out = sys.argv[1], sys.argv[2]
needles = []
home = os.path.expanduser("~")
if home and home != "/" and len(home) > 1:
    needles.append(home)
if os.path.isabs(root):
    needles.append(root)
if os.path.isabs(out):
    needles.append(out)
needles = list(dict.fromkeys(n for n in needles if n))

scanned = 0
hits = []
for dirpath, _dirnames, filenames in os.walk(out):
    for fn in filenames:
        if fn.endswith(".log"):
            continue
        p = os.path.join(dirpath, fn)
        try:
            with open(p, "rb") as f:
                data = f.read()
        except OSError:
            continue
        scanned += 1
        for needle in needles:
            if needle.encode() in data:
                hits.append(os.path.relpath(p, out))
                break

fail_path = os.path.join(out, "failures.txt")
existing = set()
if os.path.exists(fail_path):
    with open(fail_path, encoding="utf-8") as f:
        existing = {l.rstrip("\n") for l in f}
new_lines = []
for rel in hits:
    line = f"  FAIL scrub-FAIL file={rel}（含私人路径字面量；开源前须清洗或排除该文件）"
    if line not in existing:
        new_lines.append(line)
if new_lines:
    with open(fail_path, "a", encoding="utf-8") as f:
        f.write("\n".join(new_lines) + "\n")
for rel in hits:
    print(f"scrub-FAIL: {rel}", file=sys.stderr)
if hits:
    sys.exit(1)
print(f"scrub: OK — {scanned} 个非日志文件无私人路径字面量")
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
    if man.get("rubric_n"):
        L += [
            f"- ③层抽样：N={man.get('rubric_n', '?')} · seed={man.get('rubric_seed', '?')}"
            f" · LLM 辅助评={'on' if str(man.get('rubric_llm')) == '1' else 'off'}",
        ]

# 场景级 chat-env 注入留痕（真跑时 bench-run 写入；AGG_ONLY 重聚合同样呈现）
env_path = os.path.join(out, "scenario-env.txt")
if os.path.exists(env_path):
    with open(env_path, encoding="utf-8") as f:
        env_lines = [l.rstrip("\n") for l in f if l.strip()]
    if env_lines:
        L += ["", "场景级注入（chat-env）："]
        L += [f"- {l.replace(chr(9), ' ← ', 1)}" for l in env_lines]

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

# ③层抽样（diag 维）：逐场景 rubric 分布（待回填 = 空 verdict；error = LLM 辅助评失败占位）
rub_stats = {}
for p in sorted(glob.glob(os.path.join(out, "diag", "*", "rubric-*.json"))):
    scn = os.path.basename(os.path.dirname(p))
    try:
        with open(p, encoding="utf-8") as f:
            rows = json.load(f)
    except (OSError, ValueError):
        rub_stats[scn] = None # 损坏文件单列，不废整段
        continue
    c = {"supports": 0, "partial": 0, "not_supports": 0, "error": 0, "unfilled": 0, "other": 0}
    for r in rows:
        v = str(r.get("verdict", "")).strip()
        if v == "":
            c["unfilled"] += 1
        elif v in ("supports", "partial", "not_supports", "error"):
            c[v] += 1
        else:
            c["other"] += 1
    rub_stats[scn] = {"sampled": len(rows), **c}

diag_scns = sorted({r.get("scenario", "") for r in diag_rows})
if diag_scns:
    L += [
        "",
        "## ③层抽样（diag 维；逐场景池化）",
        "",
        "| 场景 | 抽样行 | supports | partial | not_supports | error | 待回填 |",
        "| --- | --- | --- | --- | --- | --- | --- |",
    ]
    for scn in diag_scns:
        st = rub_stats.get(scn)
        if scn in rub_stats and st is None:
            L.append(f"| {scn} | 损坏（rubric JSON 无法解析） | - | - | - | - | - |")
        elif st is None:
            L.append(f"| {scn} | —（未跑 / 无对可抽） | - | - | - | - | - |")
        else:
            L.append(
                f"| {scn} | {st['sampled']} | {st['supports']} | {st['partial']}"
                f" | {st['not_supports']} | {st['error']} | {st['unfilled']} |"
            )
    others = sum(s["other"] for s in rub_stats.values() if s)
    if others:
        L += ["", f"- 注意：存在未知 verdict 值 {others} 行（非三值 / error / 空）"]

if rub_stats:
    with open(os.path.join(out, "rubric-summary.csv"), "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["scenario", "sampled", "supports", "partial", "not_supports", "error", "unfilled"])
        for scn in sorted(rub_stats):
            st = rub_stats[scn]
            if st:
                w.writerow([scn, st["sampled"], st["supports"], st["partial"], st["not_supports"], st["error"], st["unfilled"]])
    print(f"rubric-summary: {len(rub_stats)} 场景 → rubric-summary.csv")

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
    rewrite_sweep_manifests || echo "bench-run: WARN sweep manifest 清洗异常（继续聚合）" >&2
    scrub_check || overall=1
    aggregate || overall=1
    if [ "$overall" -eq 0 ]; then
        echo "bench-run: 聚合完成（AGG_ONLY，未跑单元未碰集群未重抽样）"
    else
        echo "bench-run: 失败清单非空或 scrub 未过（见 summary.md）" >&2
    fi
    exit "$overall"
fi

if [ "$DRYRUN" = "1" ]; then
    # 干跑：只打印计划 + 透传两个 sweep 的干跑输出（单元命令与计数），零集群零 LLM；
    # 目录尽力建（fail() 记清单用；不可创建时清单只进 stderr）；干跑中记录到失败同样影响退出码
    mkdir -p "$OUT" 2>/dev/null || true
    for scn in $SCENARIOS; do
        plan="fresh-up → up"
        if want_diag; then plan="$plan → diag → rubric"; fi
        if want_probe; then plan="$plan → probe"; fi
        plan="$plan → down"
        echo "[plan] $scn: $plan"
        inject="$(chat_env_lines "$scn")"
        emit_chat_env "$scn" "$inject"
        if want_diag; then
            run_sweep diag "$ROOT/scripts/eval-sweep.sh" "$OUT/diag/$scn" "$scn" "$inject"
            run_rubric "$scn"
        fi
        if want_probe; then
            if [ -f "$ROOT/scenarios/$scn/probe.yaml" ]; then
                run_sweep probe "$ROOT/scripts/probe-sweep.sh" "$OUT/probe/$scn" "$scn" "$inject"
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
        inject="$(chat_env_lines "$scn")"
        emit_chat_env "$scn" "$inject"
        if want_diag; then
            run_sweep diag "$ROOT/scripts/eval-sweep.sh" "$OUT/diag/$scn" "$scn" "$inject"
            if [ -s "$OUT/diag/$scn/eval-sweep.csv" ]; then
                run_rubric "$scn"
            else
                fail "records-missing scenario=$scn dim=diag (eval-sweep.csv 缺失或为空)"
            fi
        fi
        if want_probe; then
            if [ -f "$ROOT/scenarios/$scn/probe.yaml" ]; then
                run_sweep probe "$ROOT/scripts/probe-sweep.sh" "$OUT/probe/$scn" "$scn" "$inject"
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
        fail "up-FAIL scenario=$scn (skip both dims; see bench-run.stdout.log)"
    fi
    echo
done

# 开源清洗：sweep manifest 字段归一 → 自检门（命中记 scrub-FAIL，批非零）→ 聚合
rewrite_sweep_manifests || echo "bench-run: WARN sweep manifest 清洗异常（继续聚合）" >&2
scrub_check || overall=1
aggregate || overall=1
if [ "$overall" -eq 0 ]; then
    echo "bench-run: ALL OK → $OUT/summary.md"
else
    echo "bench-run: FAILURES recorded in $OUT/failures.txt（详见 $OUT/summary.md 失败清单）" >&2
fi
exit "$overall"
