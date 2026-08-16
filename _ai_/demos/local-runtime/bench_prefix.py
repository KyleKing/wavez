import json
import sys
import time
import urllib.request

PREFIX = open("prompts/prefix.txt").read()
PREFIX_TRIMMED = open("prompts/prefix_trimmed.txt").read()
SUFFIXES = [open(f"prompts/suffix_{i}.txt").read() for i in (1, 2, 3)]

def ollama_call(content):
    body = {
        "model": "qwen3:8b",
        "messages": [{"role": "user", "content": content}],
        "stream": False,
        "think": False,
        "options": {"num_ctx": 8192},
    }
    req = urllib.request.Request("http://localhost:11434/api/chat", data=json.dumps(body).encode(),
                                  headers={"Content-Type": "application/json"})
    t0 = time.monotonic()
    with urllib.request.urlopen(req) as resp:
        obj = json.loads(resp.read())
    wall = time.monotonic() - t0
    pe_tok = obj["prompt_eval_count"]
    pe_s = obj["prompt_eval_duration"] / 1e9
    return {"wall_s": round(wall, 3), "prompt_eval_tokens": pe_tok,
            "prompt_eval_s": round(pe_s, 3), "prompt_eval_tok_s": round(pe_tok / pe_s, 1) if pe_s else None}

def llamacpp_call(content, port=8090):
    body = {"model": "qwen3-8b", "messages": [{"role": "user", "content": content}], "reasoning_effort": "none"}
    req = urllib.request.Request(f"http://127.0.0.1:{port}/v1/chat/completions", data=json.dumps(body).encode(),
                                  headers={"Content-Type": "application/json"})
    t0 = time.monotonic()
    with urllib.request.urlopen(req) as resp:
        obj = json.loads(resp.read())
    wall = time.monotonic() - t0
    t = obj["timings"]
    return {"wall_s": round(wall, 3), "prompt_eval_tokens": t["prompt_n"], "cache_n": t["cache_n"],
            "prompt_eval_s": round(t["prompt_ms"] / 1000, 3), "prompt_eval_tok_s": round(t["prompt_per_second"], 1)}

if __name__ == "__main__":
    backend = sys.argv[1]
    call = ollama_call if backend == "ollama" else llamacpp_call

    sequence = []
    for i, suf in enumerate(SUFFIXES):
        sequence.append({"step": f"prefix+suffix{i+1}", **call(PREFIX + suf)})

    trimmed_step = {"step": "trimmed_prefix+suffix_new", **call(PREFIX_TRIMMED + "\n\nSummarize the account balance invariant in one sentence.")}
    unchanged_step = {"step": "unchanged_prefix+suffix_new (control)", **call(PREFIX + "\n\nSummarize the account balance invariant in one sentence.")}

    print(json.dumps({"append_only_sequence": sequence, "trimmed_prefix": trimmed_step, "unchanged_prefix_control": unchanged_step}, indent=2))
