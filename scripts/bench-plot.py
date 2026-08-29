#!/usr/bin/env python3
"""bench-plot: 把 aruing bench 的 CSV 汇总成创新点二主实验图。

三份产物（写入 --out-dir，默认 bench/results/plots）：
  1. position-hitrate.png   —— 杀手图③：根因位置桶 × 方法命中率分组柱状图
  2. budget-curves.png      —— 预算-命中率曲线（按表大小 N 分面）
  3. ablation.md            —— 消融对比表（greedy vs random / simplestat / knapsack）

只读 CSV（--csv，默认 bench/results/projection-bench.csv），不重算判分。
依赖 matplotlib（本地 venv 跑，不进 CI）：
  python3 -m venv .venv && .venv/bin/pip install matplotlib && .venv/bin/python scripts/bench-plot.py
"""

import argparse
import csv
import os
from collections import defaultdict

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt

# 主对比方法展示序（消融臂不进主图，另出消融表）
MAIN_METHODS = ["full", "greedy", "fast", "greedy-knapsack", "head-tail", "uniform"]
ABLATION_METHODS = ["greedy", "random", "simplestat", "greedy-knapsack"]


def load_rows(path):
    with open(path, newline="", encoding="utf-8") as f:
        return list(csv.DictReader(f))


def hitrate_by(rows, method, key_fn):
    """按 key_fn 分组聚合某方法的命中率；返回 {key: (rate, n)}"""
    hits = defaultdict(int)
    total = defaultdict(int)
    for r in rows:
        if r["method"] != method:
            continue
        k = key_fn(r)
        total[k] += 1
        hits[k] += int(r["hit"])
    return {k: (hits[k] / total[k], total[k]) for k in total}


def plot_position(rows, out_dir):
    """杀手图③：位置桶 × 方法命中率。C2 中段塌、Ours 三桶持平是主叙事。"""
    positions = sorted({int(r["position_bucket"]) for r in rows})
    methods = [m for m in MAIN_METHODS if any(r["method"] == m for r in rows)]
    fig, ax = plt.subplots(figsize=(10, 5))
    width = 0.8 / max(len(methods), 1)
    for i, m in enumerate(methods):
        rates = hitrate_by(rows, m, lambda r: int(r["position_bucket"]))
        xs = [p + i * width for p in positions]
        ys = [rates.get(p, (float("nan"), 0))[0] for p in positions]
        ax.bar(xs, ys, width=width, label=m)
    ax.set_xlabel("root-cause position (% of table)")
    ax.set_ylabel("hit rate")
    ax.set_ylim(0, 1.05)
    ax.set_xticks([p + 0.4 for p in positions])
    ax.set_xticklabels([f"{p}%" for p in positions])
    ax.set_title("Root-cause hit rate by position x method")
    ax.legend(ncol=3, fontsize=9)
    fig.tight_layout()
    path = os.path.join(out_dir, "position-hitrate.png")
    fig.savefig(path, dpi=150)
    plt.close(fig)
    return path


def plot_budget(rows, out_dir):
    """预算-命中率曲线，按 N 分面。"""
    ns = sorted({int(r["N"]) for r in rows})
    methods = [m for m in MAIN_METHODS if any(r["method"] == m for r in rows)]
    fig, axes = plt.subplots(1, len(ns), figsize=(5 * len(ns), 4), sharey=True, squeeze=False)
    for ax, n in zip(axes[0], ns):
        sub = [r for r in rows if int(r["N"]) == n]
        for m in methods:
            rates = hitrate_by(sub, m, lambda r: int(r["budget"]))
            if not rates:
                continue
            xs = sorted(rates)
            ax.plot(xs, [rates[x][0] for x in xs], marker="o", label=m)
        ax.set_title(f"N={n}")
        ax.set_xlabel("budget (runes)")
        ax.set_ylim(-0.05, 1.05)
    axes[0][0].set_ylabel("hit rate")
    axes[0][0].legend(fontsize=8)
    fig.suptitle("Hit rate vs projection budget")
    fig.tight_layout()
    path = os.path.join(out_dir, "budget-curves.png")
    fig.savefig(path, dpi=150)
    plt.close(fig)
    return path


def write_ablation(rows, out_dir):
    """消融对比表：贪心 vs 随机 / 简单统计量 / knapsack（总体命中率 + 单元数）。"""
    lines = [
        "# Ablation (overall hit rate)",
        "",
        "| method | hit rate | units |",
        "| --- | --- | --- |",
    ]
    for m in ABLATION_METHODS:
        sub = [r for r in rows if r["method"] == m]
        if not sub:
            continue
        hit = sum(int(r["hit"]) for r in sub)
        lines.append(f"| {m} | {hit / len(sub):.3f} | {len(sub)} |")
    path = os.path.join(out_dir, "ablation.md")
    with open(path, "w", encoding="utf-8") as f:
        f.write("\n".join(lines) + "\n")
    return path


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--csv", default=os.path.join("bench", "results", "projection-bench.csv"))
    ap.add_argument("--out-dir", default=os.path.join("bench", "results", "plots"))
    args = ap.parse_args()

    rows = load_rows(args.csv)
    if not rows:
        raise SystemExit(f"no rows in {args.csv}")
    os.makedirs(args.out_dir, exist_ok=True)
    for p in (plot_position(rows, args.out_dir), plot_budget(rows, args.out_dir), write_ablation(rows, args.out_dir)):
        print(p)


if __name__ == "__main__":
    main()
