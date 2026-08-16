import json
import subprocess
import sys
import time
import urllib.request

MODEL = "qwen3:8b"
URL = "http://localhost:11434/api/chat"

def stop_model():
    subprocess.run(["ollama", "stop", MODEL], capture_output=True)
    time.sleep(1)

def runner_rss_kb():
    out = subprocess.run(["ps", "-axo", "pid,rss,command"], capture_output=True, text=True).stdout
    total = 0
    found = False
    for line in out.splitlines():
        if "lib/ollama/llama-server" in line:
            found = True
            parts = line.split(None, 2)
            total += int(parts[1])
    return total if found else None

def run_once():
    stop_model()
    body = {
        "model": MODEL,
        "messages": [{"role": "user", "content": "Say the single word: ready"}],
        "stream": True,
        "think": False,
        "options": {"num_ctx": 8192},
    }
    req = urllib.request.Request(URL, data=json.dumps(body).encode(), headers={"Content-Type": "application/json"})
    t0 = time.monotonic()
    first_token_t = None
    with urllib.request.urlopen(req) as resp:
        for line in resp:
            if not line.strip():
                continue
            obj = json.loads(line)
            if first_token_t is None and obj.get("message", {}).get("content"):
                first_token_t = time.monotonic()
            if obj.get("done"):
                break
    rss = runner_rss_kb()
    return {
        "wall_to_first_token_s": round((first_token_t - t0), 3) if first_token_t else None,
        "runner_rss_kb": rss,
    }

if __name__ == "__main__":
    n = int(sys.argv[1]) if len(sys.argv) > 1 else 3
    results = [run_once() for _ in range(n)]
    print(json.dumps(results, indent=2))
