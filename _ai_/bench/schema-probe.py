#!/usr/bin/env python3
"""Ask the local model to make one tool call and report whether the arguments arrived whole.

A tool's JSON schema is a grammar on the fast tier, so a property left out of
`required` is an exit the model can take mid-call. This is how that is measured
rather than argued: run it before and after a schema change and compare the
complete counts.

    _ai_/bench/schema-probe.py str_replace 5 "README.md line 5 reads ..."
"""

import json
import pathlib
import shutil
import subprocess
import sys
import urllib.request

ENDPOINT = "http://127.0.0.1:8080/v1/chat/completions"


def schema_of(tool: str) -> dict:
    """Read one tool's live schema out of the tree rather than a copy of it.

    The generated package sits in a dot directory, which the go tool leaves out
    of `./...` so it cannot reach the build while it exists.
    """
    src = (
        "package main\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/kyleking/wavez/internal/tools\"\n)\n\n"
        f"func main() {{ fmt.Println(string(tools.New{tool}(\"/tmp\", nil).Schema())) }}\n"
    )
    at = pathlib.Path(".schemaprobe")
    at.mkdir(exist_ok=True)
    try:
        (at / "main.go").write_text(src)
        out = subprocess.run(  # noqa: S603
            ["go", "run", "./" + at.name], capture_output=True, text=True, check=True
        )
    finally:
        shutil.rmtree(at, ignore_errors=True)

    return json.loads(out.stdout)


def probe(tool: str, schema: dict, prompt: str) -> tuple[bool, str]:
    req = {
        "model": "qwen3:8b",
        "messages": [
            {"role": "system", "content": "You edit files with tools."},
            {"role": "user", "content": prompt},
        ],
        "tools": [{"type": "function", "function": {"name": tool, "parameters": schema}}],
        "tool_choice": "required",
        "temperature": 0.01,
        "max_tokens": 300,
    }
    body = json.dumps(req).encode()
    with urllib.request.urlopen(  # noqa: S310
        urllib.request.Request(ENDPOINT, body, {"Content-Type": "application/json"}), timeout=60
    ) as resp:
        message = json.load(resp)["choices"][0]["message"]

    calls = message.get("tool_calls")
    if not calls:
        return False, "no tool call"

    args = json.loads(calls[0]["function"]["arguments"])
    edits = args.get("edits") or args.get("docs") or [args]
    whole = bool(edits) and all(
        all(v not in (None, "") for k, v in e.items() if k != "path") for e in edits
    )
    return whole, json.dumps(args)[:160]


def main() -> int:
    if len(sys.argv) < 4:
        print(__doc__)
        return 2

    tool, samples, prompt = sys.argv[1], int(sys.argv[2]), sys.argv[3]
    schema = schema_of(tool)
    ok = 0

    for _ in range(samples):
        whole, shown = probe(tool, schema, prompt)
        ok += whole
        print(("COMPLETE " if whole else "PARTIAL  ") + shown)

    print(f"{ok} of {samples} complete")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
