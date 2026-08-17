#!/usr/bin/env python3
"""Measure what a thread's transcript is made of, and what survives distillation.

Feeds the Cycles section of DESIGN.md. The claim under test: almost
everything a long context accumulates is re-derivable from the code, and the
part that is not is small and typed.

Token counts are chars/4, the same rough estimator internal/agent uses for
routing. Absolute numbers are approximate; the ratios are the point.
"""
import glob, json, sys
from collections import Counter

CHARS_PER_TOKEN = 4


def classify(ev):
    """Split a thread event into re-derivable and not.

    agent prose and tool output are re-derivable: the prose restates what the
    tools did, and the tool output can be produced again by running the tool
    against the same tree. user turns and gate verdicts are not.
    """
    kind = ev.get("kind")
    if kind in ("agent",):
        return "model prose"
    if kind == "tool":
        return "tool output"
    if kind in ("user",):
        return "goal and feedback"
    if kind == "gate":
        return "gate verdict"
    return "bookkeeping"


REDERIVABLE = {"model prose", "tool output", "bookkeeping"}


def measure(path):
    sizes = Counter()
    for line in open(path):
        ev = json.loads(line)
        sizes[classify(ev)] += len(line)
    return sizes


def main(paths):
    total = Counter()
    for p in paths:
        for k, v in measure(p).items():
            total[k] += v

    grand = sum(total.values())
    print(f"{'category':<20} {'bytes':>9} {'~tokens':>9} {'share':>7}  re-derivable")
    for k, v in total.most_common():
        print(f"{k:<20} {v:>9} {v // CHARS_PER_TOKEN:>9} {100 * v / grand:>6.1f}%  {k in REDERIVABLE}")

    keep = sum(v for k, v in total.items() if k not in REDERIVABLE)
    print(f"\n{'total':<20} {grand:>9} {grand // CHARS_PER_TOKEN:>9}")
    print(f"{'not re-derivable':<20} {keep:>9} {keep // CHARS_PER_TOKEN:>9} {100 * keep / grand:>6.1f}%")


if __name__ == "__main__":
    main(sys.argv[1:] or sorted(glob.glob(".wavez/threads/*.jsonl")))
