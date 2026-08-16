import json
import sys
import time
import urllib.request

EDIT_PROMPT = open("prompts/edit_prompt.txt").read()

def ollama_run():
    body = {
        "model": "qwen3:8b",
        "messages": [{"role": "user", "content": EDIT_PROMPT}],
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
    dec_tok = obj["eval_count"]
    dec_s = obj["eval_duration"] / 1e9
    return {"wall_s": round(wall, 3), "decode_tokens": dec_tok, "decode_tok_s": round(dec_tok / dec_s, 1) if dec_s else None}

def llamacpp_run(port=8090):
    body = {"model": "qwen3-8b", "messages": [{"role": "user", "content": EDIT_PROMPT}], "reasoning_effort": "none"}
    req = urllib.request.Request(f"http://127.0.0.1:{port}/v1/chat/completions", data=json.dumps(body).encode(),
                                  headers={"Content-Type": "application/json"})
    t0 = time.monotonic()
    with urllib.request.urlopen(req) as resp:
        obj = json.loads(resp.read())
    wall = time.monotonic() - t0
    t = obj["timings"]
    return {"wall_s": round(wall, 3), "decode_tokens": t["predicted_n"], "decode_tok_s": round(t["predicted_per_second"], 1)}

if __name__ == "__main__":
    backend = sys.argv[1]
    n = int(sys.argv[2]) if len(sys.argv) > 2 else 3
    fn = ollama_run if backend == "ollama" else llamacpp_run
    print(json.dumps([fn() for _ in range(n)], indent=2))
