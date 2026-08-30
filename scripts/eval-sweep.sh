#!/usr/bin/env bash
# eval-sweep —— 创新点一实验矩阵驱动器（0.1.2 步骤 4，裁决 4/5/6）
#
# 矩阵：场景 × 方法(b1-serial/b2-random/b4-cheapest/ours/b3-react) × K(1/2/3/5/8) × 重复
# 每单元：env 注入 method/K/seed → aruing run --eval-json 落记录 → 逐场景 judge
# 汇总 → CSV（联结判分与记录观测量）。曲线横轴用实测 rounds（裁决 5），名义 K
# 仅作注入参数与分桶校验。
#
# 成本纪律：全矩阵 4×5×5×3 = 300 单元全 LLM 调用——先 DRYRUN=1 核对矩阵、
# 按既往 token 均值估算成本报批后再真跑（裁决 4）。
#
# 用法：
#   make eval-sweep DRYRUN=1            # 干跑：打印全部命令与单元计数，不执行
#   make eval-sweep                     # 真跑（须 kind 场景 up + LLM 配额已授权）
#   scripts/eval-sweep.sh OUT=mydir METHODS="ours b1-serial" KS="1 3"   # 子集矩阵
set -euo pipefail

SCENARIOS="${SCENARIOS:-crashloop-bad-image svc-wrong-selector same-name-multi-ns log-time-window}"
METHODS="${METHODS:-b1-serial b2-random b4-cheapest ours b3-react}"
KS="${KS:-1 2 3 5 8}"
REPS="${REPS:-3}"
OUT="${OUT:-eval/results/0.1.2}"
CONFIG="${CONFIG:-playground/config.yaml}"
ARUING="${ARUING:-go run ./cmd/aruing}"
DRYRUN="${DRYRUN:-0}"
FORCE="${FORCE:-0}"   # 1 = 忽略已有记录强制重跑（默认跳过已完成的单元）
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# 场景问题 = prompts.md 第 1 条有序列表提示词（与 smoke 验收同源）；
# cases 协议场景（无顶层 prompts.md）取首个 case 的首条；
# 取首个「与其后首个」之间的文本（允许尾部备注）
scn_question() {
    local f="$ROOT/scenarios/$1/prompts.md" line
    if [ ! -f "$f" ]; then
        f="$ROOT/scenarios/$1/cases/01-default/prompts.md"
    fi
    line="$(grep -m1 -E '^[[:space:]]*[0-9]+\.' "$f" || true)"
    line="${line#*「}"
    line="${line%%」*}"
    echo "$line"
}

# 每场景独立 kind 集群：kubeconfig 由 scenario-up 落在 scenarios/.kube/<scn>.yaml
# （smoke-all 同源约定）；缺失则提前失败，避免烧错集群的配额
scn_kubeconfig() {
    echo "$ROOT/scenarios/.kube/$1.yaml"
}

units=0
missing=0
skipped=0
scn_count=0
mkdir -p "$OUT"

# 预检：全部场景集群就绪才开跑（真跑前由 make lab-up SCN=... 逐个拉起）
if [ "$DRYRUN" != "1" ]; then
    for scn in $SCENARIOS; do
        if [ ! -f "$(scn_kubeconfig "$scn")" ]; then
            echo "错误：场景 $scn 的 kubeconfig 缺失（$(scn_kubeconfig "$scn")）；先 make lab-up SCN=$scn" >&2
            exit 1
        fi
    done
    # manifest：装置归因（2026-08-30 追加裁决）——git / 模型 / 矩阵参数随批落盘
    model="$(grep -E '^[[:space:]]*model:' "$CONFIG" | head -1 | sed 's/^[[:space:]]*model:[[:space:]]*//' | tr -d '"')"
    printf '{\n  "tool": "eval-sweep",\n  "git": "%s",\n  "commit": "%s",\n  "model": "%s",\n  "config": "%s",\n  "scenarios": "%s",\n  "methods": "%s",\n  "ks": "%s",\n  "reps": "%s",\n  "started": "%s"\n}\n' \
        "$(git -C "$ROOT" describe --always --dirty 2>/dev/null || echo unknown)" \
        "$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)" \
        "${model:-unknown}" "$CONFIG" "$SCENARIOS" "$METHODS" "$KS" "$REPS" \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$OUT/manifest.json"
fi

for scn in $SCENARIOS; do
    q="$(scn_question "$scn")"
    if [ -z "$q" ]; then
        echo "警告：场景 $scn 无提示词，跳过" >&2
        continue
    fi
    mkdir -p "$OUT/records/$scn"
    scn_count=$((scn_count + 1))
    kube="KUBECONFIG=$(scn_kubeconfig "$scn")"
    for m in $METHODS; do
        for k in $KS; do
            for r in $(seq 1 "$REPS"); do
                units=$((units + 1))
                rec="$OUT/records/$scn/${scn}__${m}__k${k}__r${r}.json"
                # 断点续跑守卫：记录已存在且非空则跳过（中断重跑不重烧配额；FORCE=1 覆盖）
                if [ "$DRYRUN" != "1" ] && [ "$FORCE" != "1" ] && [ -s "$rec" ]; then
                    echo "[$units] $scn $m k=$k r=$r 已有记录，跳过"
                    skipped=$((skipped + 1))
                    continue
                fi
                envs="KUBECONFIG=$(scn_kubeconfig "$scn") ARUING_AGENT_ACQUIRE_METHOD=$m ARUING_AGENT_ACQUIRE_MAX_ROUNDS=$k"
                # b2 臂种子 = 重复号（同方法同重复可复现；其余臂不注入）
                if [ "$m" = "b2-random" ]; then
                    envs="$envs ARUING_AGENT_ACQUIRE_SEED=$r"
                fi
                if [ "$DRYRUN" = "1" ]; then
                    printf 'env %s %s run --config %s --eval-json %s "%s"\n' \
                        "$envs" "$ARUING" "$CONFIG" "$rec" "$q"
                    continue
                fi
                echo "[$units] $scn $m k=$k r=$r"
                # 单元失败不中断矩阵（失败 run 全量报告是统计纪律）；
                # 失败路径同样落评测记录，事后按记录缺失计数
                if ! env $envs $ARUING run --config "$CONFIG" --eval-json "$rec" "$q" \
                    >/dev/null 2>>"$OUT/run.stderr.log"; then
                    echo "  单元非零退出（失败记录仍计入）" >&2
                fi
                if [ ! -s "$rec" ]; then
                    echo "  记录缺失：$rec" >&2
                    missing=$((missing + 1))
                fi
            done
        done
    done
done

if [ "$DRYRUN" = "1" ]; then
    m_count=$(echo $METHODS | wc -w | tr -d ' ')
    k_count=$(echo $KS | wc -w | tr -d ' ')
    echo "DRYRUN：$units 单元（$scn_count 场景 × $m_count 方法 × $k_count K × $REPS 重复），未执行" >&2
    exit 0
fi

# 逐场景判分（judge 一次吃一个场景真值）→ 每场景一份判分 JSON
for scn in $SCENARIOS; do
    [ -d "$OUT/records/$scn" ] || continue
    $ARUING judge --run-json "$OUT/records/$scn" \
        --scenario "$ROOT/scenarios/$scn/scenario.yaml" >"$OUT/judge-$scn.json"
done

# CSV 汇总：判分结果 × 记录观测量按 run_id 联结（python3 标准库，无三方依赖）
python3 - "$OUT" <<'PY'
import csv, glob, json, os, sys

out = sys.argv[1]
rows = []
for jf in sorted(glob.glob(os.path.join(out, "judge-*.json"))):
    scn = os.path.basename(jf)[len("judge-"):-len(".json")]
    judged = {r["run_id"]: r for r in json.load(open(jf, encoding="utf-8"))}
    for rf in sorted(glob.glob(os.path.join(out, "records", scn, "*.json"))):
        rec = json.load(open(rf, encoding="utf-8"))
        # 文件名即矩阵坐标：scn__method__kK__rR
        _, method, k, rep = os.path.basename(rf)[:-5].split("__")
        j = judged.get(rec["run_id"], {})
        toks = rec.get("tokens", {}) or {}
        rows.append({
            "scenario": scn,
            "method": rec.get("acquire_method") or method,
            "k": k[1:],
            "rep": rep[1:],
            "run_id": rec["run_id"],
            "completed": rec.get("completed", False),
            "root_cause_hit": j.get("root_cause_hit", False),
            "citation_violations": len(j.get("citation_violations", []) or []),
            "exit": rec.get("acquire_exit", ""),
            "rounds": rec.get("rounds", 0),
            "tokens_in": sum(t.get("in", 0) for t in toks.values()),
            "tokens_out": sum(t.get("out", 0) for t in toks.values()),
            "wall_ms": rec.get("wall_time_ms", 0),
        })

csv_path = os.path.join(out, "eval-sweep.csv")
with open(csv_path, "w", newline="", encoding="utf-8") as f:
    w = csv.DictWriter(f, fieldnames=list(rows[0].keys()) if rows else ["scenario"])
    w.writeheader()
    w.writerows(rows)
print(f"CSV：{len(rows)} 行 → {csv_path}")
print(f"总 token：in={sum(r['tokens_in'] for r in rows)} out={sum(r['tokens_out'] for r in rows)}")
PY

# 出图（可选）：本地 venv 的 matplotlib；缺则提示不阻塞
if [ -x ".venv/bin/python3" ]; then
    .venv/bin/python3 scripts/bench-plot.py --mode acquire --csv "$OUT/eval-sweep.csv" --out-dir "$OUT/plots"
else
    echo "提示：出图需 matplotlib（python3 -m venv .venv && .venv/bin/pip install matplotlib 后重跑）"
fi

echo "完成：$units 单元（跳过 $skipped），记录缺失 $missing（详见 $OUT/run.stderr.log）"
