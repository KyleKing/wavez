#!/usr/bin/env python3
"""Mode C phase 2: given a hand-built skeleton (prefix/suffix around a body
hole), ask qwen3:8b to fill ONLY the body. Records wall time + token counts."""
import json
import sys
import time
import urllib.request
from pathlib import Path

BASE = Path(__file__).parent
MODEL = "qwen3:8b"

INSTRUCTION = (
    "Return only the Go statements for the body, no signature, no fences, "
    "no explanation. Do not repeat the function signature or the closing brace."
)


def run(task: str, run_idx: int):
    skel = json.loads((BASE / "skeletons" / f"{task}.json").read_text())
    prefix = skel["prefix"]
    suffix = skel["suffix"]

    prompt = (
        f"{INSTRUCTION}\n\n"
        f"--- prefix (already written) ---\n{prefix}\n"
        f"--- suffix (already written) ---\n{suffix}\n"
        f"--- your job: the body that goes between them ---"
    )

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
    body["_prefix"] = prefix
    body["_suffix"] = suffix

    out = BASE / "logs" / f"body_fill_{task}_run{run_idx}.json"
    out.write_text(json.dumps(body, indent=2))

    content = body.get("message", {}).get("content", "")
    full = prefix + content.strip("\n") + suffix
    txt_out = BASE / "logs" / "results" / f"body_fill_{task}_run{run_idx}.go"
    txt_out.write_text(full)

    print(f"{task} run{run_idx}: wall={wall_ms}ms prompt_eval={body.get('prompt_eval_count')} "
          f"eval={body.get('eval_count')}")


if __name__ == "__main__":
    run(sys.argv[1], sys.argv[2])
