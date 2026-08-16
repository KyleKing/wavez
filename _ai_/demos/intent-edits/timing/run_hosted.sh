#!/usr/bin/env bash
# Mode A: hosted-full. Runs a task prompt through `claude -p` and captures
# wall time, tokens, and cost from the JSON result.
set -euo pipefail

TASK="$1"        # T1 | T2 | T3
RUN="$2"          # run index, e.g. 1 or 2
MODEL="${3:-claude-sonnet-5}"

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TASK_FILE="$DIR/tasks/${TASK}.md"
OUT="$DIR/logs/hosted_${TASK}_${MODEL}_run${RUN}.json"

PROMPT="$(cat "$TASK_FILE")"

START_NS=$(date +%s%N)
claude -p "$PROMPT" \
  --model "$MODEL" \
  --output-format json \
  --disallowedTools "Read,Write,Edit,Bash,Glob,Grep,WebSearch,WebFetch,Task,NotebookEdit" \
  --no-session-persistence \
  --safe-mode \
  > "$OUT"
END_NS=$(date +%s%N)

WALL_MS=$(( (END_NS - START_NS) / 1000000 ))

echo "wrote $OUT (wall ${WALL_MS}ms)"
