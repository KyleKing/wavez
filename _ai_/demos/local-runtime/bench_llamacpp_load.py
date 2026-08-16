import json
import subprocess
import sys
import time
import urllib.request

GGUF = open("gguf_path.txt").read().strip()
PORT = 8090
URL = f"http://127.0.0.1:{PORT}/v1/chat/completions"

def wait_ready(proc, timeout=120):
    t0 = time.monotonic()
    while time.monotonic() - t0 < timeout:
        if proc.poll() is not None:
            raise RuntimeError("llama-server exited early")
        try:
            urllib.request.urlopen(f"http://127.0.0.1:{PORT}/health", timeout=1)
            return
        except Exception:
            time.sleep(0.2)
    raise TimeoutError("server did not become healthy")

def rss_kb(pid):
    out = subprocess.run(["ps", "-o", "rss=", "-p", str(pid)], capture_output=True, text=True).stdout.strip()
    return int(out) if out else None

def run_once(extra_args):
    proc = subprocess.Popen(
        ["llama-server", "-m", GGUF, "-c", "8192", "-np", "1", "--port", str(PORT), *extra_args],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    try:
        t0 = time.monotonic()
        wait_ready(proc)
        body = {
            "model": "qwen3-8b",
            "messages": [{"role": "user", "content": "Say the single word: ready"}],
            "stream": True,
            "reasoning_effort": "none",
        }
        req = urllib.request.Request(URL, data=json.dumps(body).encode(), headers={"Content-Type": "application/json"})
        first_token_t = None
        with urllib.request.urlopen(req) as resp:
            for line in resp:
                if not line.strip() or line.startswith(b"data: [DONE]"):
                    continue
                if line.startswith(b"data: "):
                    obj = json.loads(line[6:])
                    delta = obj.get("choices", [{}])[0].get("delta", {})
                    if first_token_t is None and delta.get("content"):
                        first_token_t = time.monotonic()
        rss = rss_kb(proc.pid)
        return {
            "wall_to_first_token_s": round((first_token_t - t0), 3) if first_token_t else None,
            "server_rss_kb": rss,
        }
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            proc.kill()

if __name__ == "__main__":
    n = int(sys.argv[1]) if len(sys.argv) > 1 else 3
    extra = sys.argv[2:]
    results = [run_once(extra) for _ in range(n)]
    print(json.dumps(results, indent=2))
