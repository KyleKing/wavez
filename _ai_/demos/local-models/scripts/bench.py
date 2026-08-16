#!/usr/bin/env python3
"""Benchmark a local Ollama model: load/memory, decode+prompt-eval speed,
tool calling, and prompt-prefix cache reuse. Writes raw JSON logs to
../logs/<model>-<test>.json and prints a summary to stdout.

Usage: python3 bench.py <model> <logs_dir>
"""
import json
import subprocess
import sys
import time
import urllib.request

BASE = "http://localhost:11434"
HERE = __import__("pathlib").Path(__file__).parent
PROMPT = (HERE / "coding_prompt.txt").read_text()


def post(path, payload, timeout=300):
    req = urllib.request.Request(
        f"{BASE}{path}",
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read())


def ollama_ps():
    out = subprocess.run(["ollama", "ps"], capture_output=True, text=True).stdout
    return out.strip()


def mem_free_pct():
    out = subprocess.run(["memory_pressure", "-Q"], capture_output=True, text=True).stdout
    for line in out.splitlines():
        if "free percentage" in line:
            return line.strip()
    return out.strip()


def unload(model):
    post("/api/generate", {"model": model, "prompt": "", "keep_alive": 0})
    time.sleep(1)


def load_and_measure(model, logs_dir):
    unload(model)
    mem_before = mem_free_pct()
    t0 = time.time()
    resp = post("/api/generate", {"model": model, "prompt": "hi", "stream": False})
    load_s = time.time() - t0
    mem_after = mem_free_pct()
    ps = ollama_ps()
    result = {
        "load_wall_seconds": load_s,
        "mem_before": mem_before,
        "mem_after": mem_after,
        "ollama_ps": ps,
        "response_load_duration_ns": resp.get("load_duration"),
    }
    (logs_dir / f"{model.replace(':', '_')}-load.json").write_text(json.dumps(result, indent=2))
    return result


def decode_speed_runs(model, logs_dir, n=3):
    runs = []
    for i in range(n):
        resp = post("/api/generate", {"model": model, "prompt": PROMPT, "stream": False, "options": {"num_predict": 300}})
        eval_count = resp.get("eval_count", 0)
        eval_dur = resp.get("eval_duration", 1)
        prompt_eval_count = resp.get("prompt_eval_count", 0)
        prompt_eval_dur = resp.get("prompt_eval_duration", 1)
        run = {
            "run": i,
            "eval_count": eval_count,
            "eval_duration_ns": eval_dur,
            "decode_toks_per_sec": eval_count / (eval_dur / 1e9) if eval_dur else None,
            "prompt_eval_count": prompt_eval_count,
            "prompt_eval_duration_ns": prompt_eval_dur,
            "prompt_eval_toks_per_sec": prompt_eval_count / (prompt_eval_dur / 1e9) if prompt_eval_dur else None,
            "total_duration_ns": resp.get("total_duration"),
        }
        runs.append(run)
    (logs_dir / f"{model.replace(':', '_')}-decode.json").write_text(json.dumps(runs, indent=2))
    return runs


TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "rename_symbol",
            "description": "Rename a symbol (identifier) throughout a file.",
            "parameters": {
                "type": "object",
                "properties": {
                    "file": {"type": "string", "description": "Path to the file"},
                    "old": {"type": "string", "description": "Current symbol name"},
                    "new": {"type": "string", "description": "New symbol name"},
                },
                "required": ["file", "old", "new"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "edit_file",
            "description": "Replace an exact text span in a file with new text.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {"type": "string", "description": "Path to the file"},
                    "old_text": {"type": "string", "description": "Exact text to find"},
                    "new_text": {"type": "string", "description": "Replacement text"},
                },
                "required": ["path", "old_text", "new_text"],
            },
        },
    },
]

TOOL_MESSAGES = [
    {
        "role": "user",
        "content": "Rename DefaultTTL to TTL in internal/lease/lease.go",
    }
]


def tool_call_runs(model, logs_dir, n=3):
    runs = []
    for i in range(n):
        resp = post(
            "/api/chat",
            {
                "model": model,
                "messages": TOOL_MESSAGES,
                "tools": TOOLS,
                "stream": False,
            },
        )
        msg = resp.get("message", {})
        tool_calls = msg.get("tool_calls")
        run = {
            "run": i,
            "well_formed": bool(tool_calls),
            "tool_calls": tool_calls,
            "content": msg.get("content"),
            "raw_message": msg,
        }
        runs.append(run)
    (logs_dir / f"{model.replace(':', '_')}-toolcall.json").write_text(json.dumps(runs, indent=2))
    return runs


def prefix_cache_test(model, logs_dir):
    long_prefix = (PROMPT * 5)[:8000]  # ~2k tokens
    q1 = long_prefix + "\n\nSummarize the above in one sentence."
    r1 = post("/api/generate", {"model": model, "prompt": q1, "stream": False})
    r2 = post("/api/generate", {"model": model, "prompt": q1, "stream": False})
    result = {
        "first_prompt_eval_duration_ns": r1.get("prompt_eval_duration"),
        "first_prompt_eval_count": r1.get("prompt_eval_count"),
        "second_prompt_eval_duration_ns": r2.get("prompt_eval_duration"),
        "second_prompt_eval_count": r2.get("prompt_eval_count"),
        "speedup_ratio": (
            r1.get("prompt_eval_duration", 0) / r2.get("prompt_eval_duration", 1)
            if r2.get("prompt_eval_duration")
            else None
        ),
    }
    (logs_dir / f"{model.replace(':', '_')}-prefixcache.json").write_text(json.dumps(result, indent=2))
    return result


def main():
    model = sys.argv[1]
    logs_dir = __import__("pathlib").Path(sys.argv[2])
    logs_dir.mkdir(parents=True, exist_ok=True)

    print(f"=== {model} ===")
    load = load_and_measure(model, logs_dir)
    print("load:", json.dumps(load, indent=2))

    decode = decode_speed_runs(model, logs_dir)
    print("decode runs:", json.dumps(decode, indent=2))

    tools = tool_call_runs(model, logs_dir)
    print("tool call runs:", json.dumps([{k: v for k, v in r.items() if k != "raw_message"} for r in tools], indent=2))

    cache = prefix_cache_test(model, logs_dir)
    print("prefix cache:", json.dumps(cache, indent=2))


if __name__ == "__main__":
    main()
