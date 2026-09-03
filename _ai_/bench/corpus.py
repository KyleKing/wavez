#!/usr/bin/env python3
"""Answer the recurring questions about a thread log or the whole corpus.

The numbers a dogfood entry quotes come from here, so a claim made once can be
re-run against a changed tree. Every subcommand takes a directory of thread
logs, defaulting to this project's own.

    _ai_/bench/corpus.py tools .wavez/threads/p-xxxx.jsonl
    _ai_/bench/corpus.py wall  .wavez/threads/p-xxxx.jsonl
    _ai_/bench/corpus.py reads .wavez/threads
"""

import collections
import datetime as dt
import glob
import json
import os
import sys

# A thread's sidecar ends in .jsonl like its event log and is not one.
SIDECAR = ".history.jsonl"


def events(path: str):
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            try:
                yield json.loads(line)
            except json.JSONDecodeError:
                continue


def logs(at: str) -> list[str]:
    if os.path.isfile(at):
        return [at]

    return [p for p in sorted(glob.glob(os.path.join(at, "*.jsonl"))) if not p.endswith(SIDECAR)]


def tool_input(event: dict) -> dict:
    """The call's arguments, which the event log carries as a string and truncates."""
    raw = (event.get("detail") or {}).get("input") or ""
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return {}


def cmd_tools(at: str) -> None:
    """Calls and recorded changes per tool. A writing tool with no changes is editing blind."""
    calls: collections.Counter = collections.Counter()
    changes: collections.Counter = collections.Counter()

    for path in logs(at):
        for event in events(path):
            if event.get("kind") != "tool":
                continue

            calls[event.get("tool")] += 1
            changes[event.get("tool")] += len(event.get("changes") or [])

    print(f"{'tool':16} {'calls':>7} {'changes':>8}")
    for name, n in calls.most_common():
        print(f"{name:16} {n:7d} {changes[name]:8d}")


def cmd_wall(at: str) -> None:
    """Wall clock attributed to the event that ends each gap, which says where a run's time went."""
    spans: collections.Counter = collections.Counter()
    total = 0.0
    prev = None

    for path in logs(at):
        for event in events(path):
            when = dt.datetime.fromisoformat(event["at"])
            if prev is not None:
                gap = (when - prev).total_seconds()
                spans[event.get("kind")] += gap
                total += gap

            prev = when

    print(f"total {total:.0f}s")
    for kind, seconds in spans.most_common():
        print(f"  {kind:12} {seconds:8.1f}s {100 * seconds / total:5.1f}%")


def cmd_reads(at: str) -> None:
    """How much of a run's reading is a path it has already read in the same thread."""
    total = repeat = 0
    total_bytes = repeat_bytes = 0

    for path in logs(at):
        seen: collections.Counter = collections.Counter()
        for event in events(path):
            if event.get("kind") != "tool" or event.get("tool") != "read":
                continue

            args = tool_input(event)
            key = json.dumps(args.get("path") or args.get("paths"), sort_keys=True)
            size = len(event.get("text") or "")
            total += 1
            total_bytes += size

            if seen[key]:
                repeat += 1
                repeat_bytes += size

            seen[key] += 1

    if not total:
        print("no reads")
        return

    print(f"reads {total} ({total_bytes} bytes)")
    print(f"re-reads {repeat} ({100 * repeat / total:.0f}%), {repeat_bytes} bytes")


COMMANDS = {"tools": cmd_tools, "wall": cmd_wall, "reads": cmd_reads}


def main() -> int:
    if len(sys.argv) < 2 or sys.argv[1] not in COMMANDS:
        print(__doc__)
        return 2

    COMMANDS[sys.argv[1]](sys.argv[2] if len(sys.argv) > 2 else ".wavez/threads")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
