#!/usr/bin/env python3
"""Apply a hosted/local-full raw-text result to a fresh copy of the target
module and record compile/vet/test pass-fail. Single-file tasks (T1) write
one file; multi-file tasks (T2/T3) parse the "=== path ===" section markers.
"""
import re
import shutil
import subprocess
import sys
from pathlib import Path

BASE = Path(__file__).parent
COPIES = BASE / "copies"

TASK_FILES = {
    "T1": {"module": "t1", "files": ["vcs/identity.go"]},
    "T2": {"module": "t2", "files": ["models/enums.go", "models/enums_test.go"]},
    "T3": {"module": "t3", "files": ["models/notes.go", "models/notes_test.go", "cli/cli.go"]},
}


def split_sections(text: str) -> dict[str, str]:
    """Parse '=== path ===' delimited sections; strip stray markdown fences."""
    sections: dict[str, str] = {}
    parts = re.split(r"^===\s*(\S+)\s*===\s*$", text, flags=re.MULTILINE)
    # parts[0] is preamble (ignored); then alternating path, content
    for i in range(1, len(parts), 2):
        path = parts[i].strip()
        content = parts[i + 1]
        sections[path] = content
    return sections


def strip_fences(content: str) -> str:
    content = content.strip("\n")
    lines = content.split("\n")
    if lines and lines[0].strip().startswith("```"):
        lines = lines[1:]
    if lines and lines[-1].strip().startswith("```"):
        lines = lines[:-1]
    return "\n".join(lines) + "\n"


def apply_result(task: str, result_text: str, work_dir: Path) -> list[str]:
    cfg = TASK_FILES[task]
    src_module = COPIES / cfg["module"]
    shutil.copytree(src_module, work_dir, dirs_exist_ok=True)

    written = []
    if len(cfg["files"]) == 1:
        target = work_dir / cfg["files"][0]
        target.write_text(strip_fences(result_text))
        written.append(str(target))
    else:
        sections = split_sections(result_text)
        for rel in cfg["files"]:
            if rel not in sections:
                raise ValueError(f"missing section for {rel} in result; found keys={list(sections)}")
            target = work_dir / rel
            target.write_text(strip_fences(sections[rel]))
            written.append(str(target))
    return written


def build_vet_test(work_dir: Path) -> dict:
    result = {}
    for step, args in [
        ("build", ["go", "build", "./..."]),
        ("vet", ["go", "vet", "./..."]),
        ("test", ["go", "test", "./..."]),
    ]:
        p = subprocess.run(args, cwd=work_dir, capture_output=True, text=True, timeout=60)
        result[step] = {"rc": p.returncode, "stdout": p.stdout[-4000:], "stderr": p.stderr[-4000:]}
        if p.returncode != 0:
            break
    return result


def main():
    task = sys.argv[1]
    result_path = Path(sys.argv[2])
    label = sys.argv[3] if len(sys.argv) > 3 else result_path.stem

    work_dir = BASE / "logs" / "verify" / f"{task}_{label}"
    if work_dir.exists():
        shutil.rmtree(work_dir)
    work_dir.mkdir(parents=True)

    result_text = result_path.read_text()
    try:
        written = apply_result(task, result_text, work_dir)
    except Exception as e:
        print(f"APPLY_FAIL {task} {label}: {e}")
        sys.exit(1)

    outcome = build_vet_test(work_dir)
    compiled = outcome["build"]["rc"] == 0
    vetted = outcome.get("vet", {}).get("rc") == 0 if compiled else False
    tested = outcome.get("test", {}).get("rc") == 0 if vetted else False

    print(f"RESULT {task} {label}: compile={compiled} vet={vetted} test={tested}")
    if not compiled:
        print("--- build stderr ---")
        print(outcome["build"]["stderr"])
    elif not vetted:
        print("--- vet stderr ---")
        print(outcome["vet"]["stderr"])
    elif not tested:
        print("--- test stdout/stderr ---")
        print(outcome["test"]["stdout"])
        print(outcome["test"]["stderr"])


if __name__ == "__main__":
    main()
