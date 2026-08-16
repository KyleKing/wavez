#!/usr/bin/env python3
"""Verify a Mode C (intent+hole) body-fill result: splice the assembled
function into a fresh copy of the baseline module (as a new file alongside
the original) and build/vet."""
import re
import shutil
import subprocess
import sys
from pathlib import Path

BASE = Path(__file__).parent
COPIES = BASE / "copies"

TASK_TARGET = {
    "T1": ("t1", "vcs/hole_gen.go"),
    "T2": ("t2", "models/hole_gen.go"),
    "T3": ("t3", "models/hole_gen.go"),
}


def strip_package_line(go_src: str) -> str:
    """Drop only the leading 'package X' line; keep imports (this becomes a
    new file in the package, so it needs its own import block)."""
    lines = go_src.split("\n")
    out = [line for line in lines if not line.strip().startswith("package ")]
    return "\n".join(out).strip("\n") + "\n"


def main():
    task = sys.argv[1]
    result_path = Path(sys.argv[2])
    label = sys.argv[3] if len(sys.argv) > 3 else result_path.stem

    module, target_rel = TASK_TARGET[task]
    work_dir = BASE / "logs" / "verify" / f"{task}_hole_{label}"
    if work_dir.exists():
        shutil.rmtree(work_dir)
    shutil.copytree(COPIES / module, work_dir)

    go_src = result_path.read_text()
    body = strip_package_line(go_src)
    pkg_line = re.search(r"^package\s+(\S+)", go_src, re.MULTILINE).group(1)

    target = work_dir / target_rel
    target.write_text(f"package {pkg_line}\n\n{body}")

    if task == "T3":
        # The hole-fill task assumes the SizeBytes field (a separate,
        # prior intent in the real pipeline) already landed.
        notes_go = work_dir / "models" / "notes.go"
        text = notes_go.read_text()
        text = text.replace(
            "type NoteFile struct {\n\tName      string `json:\"name\"`\n\tFirstLine string `json:\"first_line,omitempty\"`\n}",
            "type NoteFile struct {\n\tName      string `json:\"name\"`\n\tFirstLine string `json:\"first_line,omitempty\"`\n\tSizeBytes int64  `json:\"size_bytes\"`\n}",
        )
        notes_go.write_text(text)

    result = {}
    for step, args in [
        ("build", ["go", "build", "./..."]),
        ("vet", ["go", "vet", "./..."]),
    ]:
        p = subprocess.run(args, cwd=work_dir, capture_output=True, text=True, timeout=60)
        result[step] = p
        if p.returncode != 0:
            break

    compiled = result["build"].returncode == 0
    vetted = result.get("vet") is not None and result["vet"].returncode == 0 if compiled else False
    print(f"RESULT {task} hole {label}: compile={compiled} vet={vetted}")
    if not compiled:
        print(result["build"].stderr[-2000:])
    elif not vetted:
        print(result["vet"].stderr[-2000:])


if __name__ == "__main__":
    main()
