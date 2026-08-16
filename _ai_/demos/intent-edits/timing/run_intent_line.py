#!/usr/bin/env python3
"""Mode C phase 1: ask qwen3:8b for a single intent line in the design's
grammar. Records wall time + token counts."""
import json
import sys
import time
import urllib.request
from pathlib import Path

BASE = Path(__file__).parent
MODEL = "qwen3:8b"


def run(task: str, run_idx: int):
    prompt = (BASE / "tasks" / "intent" / f"{task}_intent.md").read_text()
    payload = {
        "model": MODEL,
        "messages": [{"role": "user", "content": prompt}],
        "stream": False,
        "think": False,
    }
    start = time.monotonic()
    req = urllib.request.Request(
        "http://localhost:11434/api/chat",
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=120) as resp:
        body = json.loads(resp.read())
    wall_ms = int((time.monotonic() - start) * 1000)
    body["_wall_ms"] = wall_ms

    out = BASE / "logs" / f"intent_line_{task}_run{run_idx}.json"
    out.write_text(json.dumps(body, indent=2))

    content = body.get("message", {}).get("content", "").strip()
    print(f"{task} run{run_idx}: wall={wall_ms}ms prompt_eval={body.get('prompt_eval_count')} "
          f"eval={body.get('eval_count')} intent={content!r}")


if __name__ == "__main__":
    run(sys.argv[1], sys.argv[2])
