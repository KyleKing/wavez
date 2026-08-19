#!/usr/bin/env python3
"""Summarize thinking-budget.jsonl (from run.sh) plus the per-run JSON kept
under $WZ_BENCH/keep, one row per arm: landed edits, mean wall, output
tokens, tool calls, and how the runs stopped.

usage: summarize.py <bench dir>
"""
import collections
import glob
import json
import os
import sys

B = sys.argv[1]
rows = [json.loads(l) for l in open(os.path.join(B, "thinking-budget.jsonl")) if l.strip()]

# A run's own JSON (stop, output_tokens, tool_calls) is matched to a row by
# task and order, since run.sh overwrites out-*.txt per task and the keeper
# copies each non-empty version in mtime order.
kept = collections.defaultdict(list)
for f in sorted(glob.glob(os.path.join(B, "keep", "*-out-wavez-local-*.txt"))):
    task = f.rsplit("-", 1)[1].split(".")[0]
    try:
        kept[task].append(json.load(open(f)))
    except Exception:
        pass
idx = collections.Counter()
for r in rows:
    seq = kept[r["task"]]
    k = idx[r["task"]]
    idx[r["task"]] += 1
    r["detail"] = seq[len(seq) - (sum(1 for x in rows if x["task"] == r["task"]) - k)] if len(seq) >= sum(
        1 for x in rows if x["task"] == r["task"]) else None


def landed(r):
    t = r["task"]
    if t == "q1":
        return None
    if t == "e1":
        return r["changed_files"] == 1 and r["exit"] == 0
    return r["build"] == "ok" and r["tests"] == "ok" and r["changed_files"] > 0


print(f"{'arm':8} {'edits':>11} {'wall s':>8} {'out tok':>8} {'calls':>6} stops")
for arm in ("off", "on", "budget"):
    rs = [r for r in rows if r["arm"] == arm]
    edits = [landed(r) for r in rs if landed(r) is not None]
    det = [r["detail"] for r in rs if r.get("detail")]
    stops = collections.Counter(d.get("stop") for d in det)
    out = sum(d.get("output_tokens", 0) for d in det) / max(len(det), 1)
    calls = sum(d.get("tool_calls", 0) for d in det) / max(len(det), 1)
    wall = sum(r["wall_s"] for r in rs) / max(len(rs), 1)
    print(f"{arm:8} {sum(edits):>5}/{len(edits):<5} {wall:8.1f} {out:8.0f} {calls:6.1f} {dict(stops)}")
