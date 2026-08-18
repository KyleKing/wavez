#!/bin/zsh
# usage: run.sh <runner> <task-id> ; prints one JSON line to results.jsonl
set -u
B=${WZ_BENCH:-/tmp/wz-bench}; R=$B/repo; runner=$1; tid=$2
prompt=$(grep "^$tid|" $B/tasks.txt | cut -d'|' -f2-)
cd $R || exit 1
git checkout -q -- . && git clean -fdq -e .wavez -e .jj -e .codegraph
out=$B/out-$runner-$tid.txt
start=$(date +%s.%N)
case $runner in
  wavez-local)  $B/wavez -p "$prompt" -json -model local  -allow-all -max-wall-clock 300s > $out 2>$out.err; code=$? ;;
  wavez-hosted) $B/wavez -p "$prompt" -json -model hosted -allow-all -max-wall-clock 300s -max-hosted-spend 0.50 > $out 2>$out.err; code=$? ;;
  wavez-auto)   $B/wavez -p "$prompt" -json -allow-all -max-wall-clock 300s -max-hosted-spend 0.50 > $out 2>$out.err; code=$? ;;
  claude)       claude -p "$prompt" --output-format json --dangerously-skip-permissions > $out 2>$out.err; code=$? ;;
esac
end=$(date +%s.%N)
wall=$(printf '%.1f' $((end-start)))
files=$(git status --porcelain | grep -v '.wavez/' | wc -l | tr -d ' ')
build=ok; go build ./... >/dev/null 2>&1 || build=FAIL
vet=ok; go vet ./internal/sysinfo ./internal/daemon >/dev/null 2>&1 || vet=FAIL
tests=ok; go test ./internal/sysinfo ./internal/daemon -count=1 >/dev/null 2>&1 || tests=FAIL
git diff > $B/diff-$runner-$tid.patch; git status --porcelain | grep '^??' | grep -v '.wavez' | awk '{print $2}' | while read f; do echo "=== NEW $f"; cat "$f"; done >> $B/diff-$runner-$tid.patch
printf '{"runner":"%s","task":"%s","wall_s":%s,"exit":%s,"changed_files":%s,"build":"%s","vet":"%s","tests":"%s"}\n' $runner $tid $wall $code $files $build $vet $tests | tee -a $B/results.jsonl
git checkout -q -- . && git clean -fdq -e .wavez -e .jj -e .codegraph
