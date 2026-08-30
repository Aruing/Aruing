#!/usr/bin/env bash
# probe-sweep —— 创新点三探针实验矩阵驱动器（0.1.3 步骤 4）
#
# 矩阵：场景 × 记忆方法(ours/d1-last-n/d2-flat-summary) × 轮数(20/50) × 重复
# 每单元：env 注入 method → aruing probe 单进程跑一条脚本化长会话落会话级记录 →
# 逐场景 judge --probe 汇总 → CSV（联结判分与记录观测量）。
# 断崖曲线横轴 = 轮数，纵轴 = 探针成功率（出图随统一实验批补）。
#
# 成本纪律：全矩阵 2×3×2×3 = 36 会话、每会话 N 轮全 LLM 调用（D2 臂每轮全历史
# 平铺摘要、D1 臂 50 轮注入量大）——先 DRYRUN=1 核对矩阵、按既往 token 均值估算
# 成本报批后再真跑；真跑数推迟统一实验批（2026-08-30 裁决）。
#
# 用法：
#   make probe-sweep DRYRUN=1                        # 干跑：打印全部命令与单元计数
#   make probe-sweep                                 # 真跑（须 kind 场景 up + LLM 配额已授权）
#   scripts/probe-sweep.sh OUT=mydir METHODS="ours"  # 子集矩阵
set -euo pipefail

SCENARIOS="${SCENARIOS:-crashloop-bad-image svc-wrong-selector}"
METHODS="${METHODS:-ours d1-last-n d2-flat-summary}"
ROUNDS="${ROUNDS:-20 50}"
REPS="${REPS:-3}"
OUT="${OUT:-eval/results/0.1.3}"
CONFIG="${CONFIG:-playground/config.yaml}"
ARUING="${ARUING:-go run ./cmd/aruing}"
DRYRUN="${DRYRUN:-0}"
FORCE="${FORCE:-0}"   # 1 = 忽略已有记录强制重跑（默认跳过已完成的单元）
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# 每场景独立 kind 集群：kubeconfig 由 scenario-up 落在 scenarios/.kube/<scn>.yaml
# （smoke-all / eval-sweep 同源约定）；缺失则提前失败，避免烧错集群的配额
scn_kubeconfig() {
    echo "$ROOT/scenarios/.kube/$1.yaml"
}

units=0
scn_count=0
missing=0
skipped=0
mkdir -p "$OUT"

# 预检：全部场景集群就绪 + probe.yaml 存在才开跑（真跑前 make lab-up SCN=...）
if [ "$DRYRUN" != "1" ]; then
    for scn in $SCENARIOS; do
        if [ ! -f "$(scn_kubeconfig "$scn")" ]; then
            echo "错误：场景 $scn 的 kubeconfig 缺失（$(scn_kubeconfig "$scn")）；先 make lab-up SCN=$scn" >&2
            exit 1
        fi
        if [ ! -f "$ROOT/scenarios/$scn/probe.yaml" ]; then
            echo "错误：场景 $scn 缺 probe.yaml（探针规格）" >&2
            exit 1
        fi
    done
    # manifest：装置归因（2026-08-30 追加裁决）——git / 模型 / 矩阵参数随批落盘
    model="$(grep -E '^[[:space:]]*model:' "$CONFIG" | head -1 | sed 's/^[[:space:]]*model:[[:space:]]*//' | tr -d '"')"
    printf '{\n  "tool": "probe-sweep",\n  "git": "%s",\n  "commit": "%s",\n  "model": "%s",\n  "config": "%s",\n  "scenarios": "%s",\n  "methods": "%s",\n  "rounds": "%s",\n  "reps": "%s",\n  "started": "%s"\n}\n' \
        "$(git -C "$ROOT" describe --always --dirty 2>/dev/null || echo unknown)" \
        "$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || echo unknown)" \
        "${model:-unknown}" "$CONFIG" "$SCENARIOS" "$METHODS" "$ROUNDS" "$REPS" \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$OUT/manifest.json"
fi

for scn in $SCENARIOS; do
    mkdir -p "$OUT/records/$scn"
    scn_count=$((scn_count + 1))
    for m in $METHODS; do
        for n in $ROUNDS; do
            for r in $(seq 1 "$REPS"); do
                units=$((units + 1))
                rec="$OUT/records/$scn/${scn}__${m}__n${n}__r${r}.json"
                gen="$OUT/records/$scn/probe-session-$scn-$m-n$n-s$r.json"
                # 断点续跑守卫：矩阵坐标记录已存在则跳过；残留生成名 = 上次中断的半成品，清掉重跑
                if [ "$DRYRUN" != "1" ] && [ "$FORCE" != "1" ] && [ -s "$rec" ]; then
                    echo "[$units] $scn $m n=$n r=$r 已有记录，跳过"
                    skipped=$((skipped + 1))
                    continue
                fi
                [ "$DRYRUN" = "1" ] || rm -f "$gen"
                # 种子 = 重复号：同方法同重复脚本一致，跨重复脚本不同
                envs="KUBECONFIG=$(scn_kubeconfig "$scn") ARUING_AGENT_MEMORY_METHOD=$m"
                if [ "$DRYRUN" = "1" ]; then
                    printf 'env %s %s probe --config %s --scenario %s --rounds %s --seed %s --out %s\n' \
                        "$envs" "$ARUING" "$CONFIG" "$ROOT/scenarios/$scn/scenario.yaml" "$n" "$r" "$OUT/records/$scn"
                    continue
                fi
                echo "[$units] $scn $m n=$n r=$r"
                # 单元失败不中断矩阵（失败 run 全量报告是统计纪律）；
                # probe 路径失败也落部分记录（轮次失败清单在记录内）
                if ! env $envs $ARUING probe --config "$CONFIG" \
                    --scenario "$ROOT/scenarios/$scn/scenario.yaml" \
                    --rounds "$n" --seed "$r" --out "$OUT/records/$scn" \
                    >>"$OUT/probe.stdout.log" 2>>"$OUT/run.stderr.log"; then
                    echo "  单元非零退出（部分记录仍计入）" >&2
                fi
                # 单元落盘名与脚本生成名不同：probe 子命令按规格名+方法+轮数+种子命名；
                # 这里统一改名为矩阵坐标名，便于 CSV 联结（gen 已在守卫处定义并清理）
                if [ -s "$gen" ]; then
                    mv "$gen" "$rec"
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
    n_count=$(echo $ROUNDS | wc -w | tr -d ' ')
    echo "DRYRUN：$units 单元（$scn_count 场景 × $m_count 方法 × $n_count 轮数 × $REPS 重复），未执行" >&2
    exit 0
fi

# 逐场景判分（judge --probe 一次吃一个场景真值）→ 每场景一份判分 JSON
for scn in $SCENARIOS; do
    [ -d "$OUT/records/$scn" ] || continue
    $ARUING judge --probe --run-json "$OUT/records/$scn" \
        --scenario "$ROOT/scenarios/$scn/scenario.yaml" >"$OUT/judge-$scn.json"
done

# CSV 汇总：判分结果 × 记录观测量按 session_id 联结（python3 标准库，无三方依赖）
python3 - "$OUT" <<'PY'
import csv, glob, json, os, sys

out = sys.argv[1]
rows = []
for jf in sorted(glob.glob(os.path.join(out, "judge-*.json"))):
    scn = os.path.basename(jf)[len("judge-"):-len(".json")]
    judged = {r["session_id"]: r for r in json.load(open(jf, encoding="utf-8"))}
    for rf in sorted(glob.glob(os.path.join(out, "records", scn, "*.json"))):
        rec = json.load(open(rf, encoding="utf-8"))
        # 文件名即矩阵坐标：scn__method__nN__rR
        _, method, n, rep = os.path.basename(rf)[:-5].split("__")
        j = judged.get(rec["session_id"], {})
        rows.append({
            "scenario": scn,
            "method": rec.get("memory_method") or method,
            "rounds": rec.get("rounds") or int(n[1:]),
            "rep": rep[1:],
            "session_id": rec["session_id"],
            "completed": rec.get("completed", False),
            "turns_executed": rec.get("turns_executed", 0),
            "diagnose_total": j.get("diagnose_total", 0),
            "diagnose_completed": j.get("diagnose_completed", 0),
            "diagnose_root_cause_hits": j.get("diagnose_root_cause_hits", 0),
            "probe_total": j.get("probe_total", 0),
            "probe_scored": j.get("probe_scored", 0),
            "probe_hits": j.get("probe_hits", 0),
            "success_rate": j.get("success_rate", 0.0),
            "no_diagnosis": j.get("no_diagnosis", 0),
            "no_facts": j.get("no_facts", 0),
            "tokens_total": sum(
                (t or {}).get("in", 0) + (t or {}).get("out", 0)
                for t in (rec.get("tokens", {}) or {}).values()),
            "wall_time_ms": rec.get("wall_time_ms", 0),
        })
csv_path = os.path.join(out, "probe-summary.csv")
with open(csv_path, "w", newline="", encoding="utf-8") as f:
    w = csv.DictWriter(f, fieldnames=list(rows[0].keys()) if rows else ["scenario"])
    w.writeheader()
    w.writerows(rows)
print(f"csv: {csv_path} ({len(rows)} sessions)")
PY
echo "完成：${units} 会话（跳过 ${skipped}），缺失记录：${missing}" >&2
[ "$missing" -eq 0 ]
