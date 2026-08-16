#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
go build -o /tmp/editloop_bin .

for format in str_replace hashline; do
  for task in T1 T2 T3 T4 T5; do
    for run in 1 2; do
      echo "=== $format $task run$run ==="
      /tmp/editloop_bin -format "$format" -task "$task" -run "$run" -root .
    done
  done
done
