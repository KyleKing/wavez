#!/usr/bin/env python3
"""Mode B: local-full. Sends the same task prompt to qwen3:8b via Ollama's
/api/chat with think:false, non-streaming, and records wall time + token
counts + the raw response."""
import json
import sys
import time
import urllib.request
from pathlib import Path

BASE = Path(__file__).parent
MODEL = "qwen3:8b"


def run(task: str, run_idx: int):
    prompt = (BASE / "tasks" / f"{task}.md").read_text()

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
    with urllib.request.urlopen(req, timeout=600) as resp:
        body = json.loads(resp.read())
    wall_ms = int((time.monotonic() - start) * 1000)

    body["_wall_ms"] = wall_ms
    out = BASE / "logs" / f"local_full_{task}_run{run_idx}.json"
    out.write_text(json.dumps(body, indent=2))

    content = body.get("message", {}).get("content", "")
    txt_out = BASE / "logs" / "results" / f"local_full_{task}_run{run_idx}.txt"
    txt_out.write_text(content)

    print(
        f"{task} run{run_idx}: wall={wall_ms}ms "
        f"prompt_eval={body.get('prompt_eval_count')} eval={body.get('eval_count')} "
        f"eval_duration_ns={body.get('eval_duration')} "
        f"prompt_eval_duration_ns={body.get('prompt_eval_duration')}"
    )


if __name__ == "__main__":
    run(sys.argv[1], sys.argv[2])
