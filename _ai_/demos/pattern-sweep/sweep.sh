#!/usr/bin/env bash
# Enumerate the sibling sites of a root cause expressed as a structural
# pattern, so the generalize phase triages a list instead of imagining one.
#
# Usage: sweep.sh <ast-grep pattern> [path]
set -euo pipefail

pattern=$1
path=${2:-internal/}

ast-grep run -p "$pattern" -l go "$path" --json=compact | python3 -c '
import json, sys
hits = json.load(sys.stdin)
for h in hits:
    print("%s:%d" % (h["file"], h["range"]["start"]["line"] + 1))
sys.stderr.write("--- %d hit(s)\n" % len(hits))
'
