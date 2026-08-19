#!/usr/bin/env python3
"""Measure what a second thread costs the first on one llama-server.

usage: run.py <gguf> [turns=4]

Three arms, each a fresh llama-server on port 8091 with qwen-style
--jinja and --cache-reuse 256, two scripted threads A and B whose prefixes
are ~3k tokens of distinct text, alternating one short turn each:

  np1        -np 1 -c 8192           the shipped shape, with llama-server's
                                     host-RAM prompt cache at its default
  np1-noram  -np 1 -c 8192 --cache-ram 0   the same with that cache off, so
                                     B's prompt evicts A's from the one slot
  np2        -np 2 -c 16384          one slot per thread, 8k each
  np1-save   -np 1 -c 8192 + slot save/restore to disk around each switch

Per request it prints prompt_n, cache_n, and prompt_ms from the timings block,
plus the server's resident set after the arm. Timings are per llama-server's
own /completion accounting.
"""
import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.request

GGUF = sys.argv[1]
TURNS = int(sys.argv[2]) if len(sys.argv) > 2 else 4
PORT = 8091
BASE = f"http://127.0.0.1:{PORT}"


def post(path, body, method="POST"):
    req = urllib.request.Request(BASE + path, data=json.dumps(body).encode(), method=method,
                                 headers={"Content-Type": "application/json"})
    try:
        return json.load(urllib.request.urlopen(req, timeout=600))
    except urllib.error.HTTPError as e:
        raise SystemExit(f"{path}: {e.code} {e.read()[:300]!r}")


def prefix(seed):
    lines = []
    for i in range(64):
        lines.append(f"func {seed}Handler{i}(ctx context.Context, req *{seed}Request{i}) (*{seed}Reply{i}, error) {{ "
                     f"if req == nil {{ return nil, errNil{seed}{i} }}; return &{seed}Reply{i}{{ID: req.ID + {i}}}, nil }}")
    return "You are reviewing this package.\n" + "\n".join(lines)


def start(args, env=None):
    p = subprocess.Popen(["llama-server", "-m", GGUF, "--host", "127.0.0.1", "--port", str(PORT), "--jinja",
                          "--cache-reuse", "256", "--chat-template-kwargs", '{"enable_thinking":false}'] + args,
                         stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env)
    for _ in range(300):
        try:
            if json.load(urllib.request.urlopen(BASE + "/health"))["status"] == "ok":
                return p
        except Exception:
            pass
        time.sleep(1)
    raise SystemExit("llama-server did not come up")


def rss_mb(pid):
    out = subprocess.run(["ps", "-o", "rss=", "-p", str(pid)], capture_output=True, text=True).stdout.strip()
    return int(out) // 1024 if out else -1


def turn(name, history, slot=None):
    body = {"model": "local", "messages": history, "max_tokens": 16, "stream": False}
    if slot is not None:
        body["id_slot"] = slot
    t0 = time.time()
    rep = post("/v1/chat/completions", body)
    tm = rep.get("timings", {})
    wall = (time.time() - t0) * 1000
    print(f"  {name}: prompt_n={tm.get('prompt_n')} cache_n={tm.get('cache_n')} "
          f"prompt_ms={tm.get('prompt_ms', 0):.0f} wall_ms={wall:.0f}")
    return rep["choices"][0]["message"]["content"], tm


def arm(label, args, save_dir=None):
    print(f"== {label}: {' '.join(args)}")
    p = start(args)
    try:
        hist = {"A": [{"role": "system", "content": prefix("Alpha")}],
                "B": [{"role": "system", "content": prefix("Beta")}]}
        totals = {"A": [], "B": []}
        for i in range(TURNS):
            for name in ("A", "B"):
                if save_dir:
                    other = "B" if name == "A" else "A"
                    if i > 0 or name == "B":
                        post(f"/slots/0?action=save", {"filename": f"{other}.bin"})
                        if os.path.exists(os.path.join(save_dir, f"{name}.bin")):
                            t0 = time.time()
                            post(f"/slots/0?action=restore", {"filename": f"{name}.bin"})
                            print(f"  restore {name}: {1000*(time.time()-t0):.0f} ms")
                hist[name].append({"role": "user", "content": f"turn {i}: which handler returns ID plus {i}? one word."})
                text, tm = turn(f"{name}{i}", hist[name], slot=(0 if save_dir else None))
                hist[name].append({"role": "assistant", "content": text})
                totals[name].append(tm.get("prompt_ms", 0))
        print(f"  rss={rss_mb(p.pid)} MB; prompt_ms sum A={sum(totals['A']):.0f} B={sum(totals['B']):.0f}, "
              f"after the first turn A={sum(totals['A'][1:]):.0f} B={sum(totals['B'][1:]):.0f}")
    finally:
        p.terminate()
        p.wait()


arm("np1", ["-np", "1", "-c", "8192"])
arm("np1-noram", ["-np", "1", "-c", "8192", "--cache-ram", "0"])
arm("np2", ["-np", "2", "-c", "16384"])
d = tempfile.mkdtemp(prefix="kvslots")
arm("np1-save", ["-np", "1", "-c", "8192", "--slot-save-path", d], save_dir=d)
