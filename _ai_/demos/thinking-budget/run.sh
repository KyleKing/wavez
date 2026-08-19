#!/bin/zsh
# usage: run.sh <gguf> [samples=3]
# Runs the four timing-harness tasks through `wavez -p -model local` in three
# arms and appends rows tagged with the arm to $WZ_BENCH/results.jsonl:
#   off      wavez starts llama-server itself (enable_thinking=false, as shipped)
#   on       a llama-server started here with --reasoning on, which wavez reuses
#   budget   the same with --reasoning-budget 256
# Needs the bench copy from _ai_/bench/timing/README.md under $WZ_BENCH.
set -u
GGUF=$1; N=${2:-3}
B=${WZ_BENCH:?set WZ_BENCH to the bench dir}
HERE=${0:A:h}
RUN=$HERE/../../bench/timing/run.sh

serve() {
  llama-server -m $GGUF --host 127.0.0.1 --port 8080 -c 32768 -np 1 --jinja \
    --spec-type ngram-simple --cache-reuse 256 "$@" > $B/llama-$ARM.log 2>&1 &
  SRV=$!
  until curl -s localhost:8080/health | grep -q '"ok"'; do sleep 1; done
}

arm() {
  ARM=$1; shift
  case $ARM in
    off) ;;
    *) serve "$@" ;;
  esac
  for i in $(seq $N); do
    for t in e1 e2 e3 q1; do
      line=$($RUN wavez-local $t | tail -1)
      echo "${line%\}},\"arm\":\"$ARM\",\"sample\":$i}" >> $B/thinking-budget.jsonl
      echo "$ARM $i $t: $line"
    done
  done
  [[ $ARM != off ]] && { kill $SRV; wait $SRV 2>/dev/null; }
  pkill -f 'llama-server.*--port 8080' 2>/dev/null; sleep 2
}

arm off
arm on --reasoning on
arm budget --reasoning on --reasoning-budget 256
