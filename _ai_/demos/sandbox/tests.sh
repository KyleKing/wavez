#!/usr/bin/env bash
# Probe wavez.sb: positive and negative cases, PASS/FAIL per probe.
set -uo pipefail

DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(realpath "$DEMO_DIR/proj")"
SESSION_TMP="$(realpath "$(mktemp -d "${TMPDIR%/}/wavez-test.XXXXXX")")"
OUTSIDE_TMP="$(realpath "$(mktemp -d "${TMPDIR%/}/wavez-outside.XXXXXX")")"
mkdir -p "$SESSION_TMP/gocache" "$SESSION_TMP/gomodcache" "$SESSION_TMP/gotmp"
trap 'rm -rf "$SESSION_TMP" "$OUTSIDE_TMP"' EXIT

sbx() {
  sandbox-exec -D PROJECT_ROOT="$PROJECT_ROOT" -D SESSION_TMP="$SESSION_TMP" -D HOME="$HOME" \
    -f "$DEMO_DIR/wavez.sb" bash -c "$1"
}

PASS=0
FAIL=0
declare -a ROWS

probe() {
  local name="$1" expect="$2" cmd="$3"
  local got
  if sbx "$cmd" >"$OUTSIDE_TMP/probe.out" 2>&1; then got=ok; else got=denied; fi
  local status="FAIL"
  if [[ "$got" == "$expect" ]]; then status="PASS"; PASS=$((PASS + 1)); else FAIL=$((FAIL + 1)); fi
  ROWS+=("$name|$expect|$got|$status")
  echo "[$status] $name (expected=$expect got=$got)"
}

probe "write inside project" ok \
  "echo x > '$PROJECT_ROOT/.probe' && rm -f '$PROJECT_ROOT/.probe'"

probe "write outside project (\$TMPDIR sibling)" denied \
  "echo x > '$OUTSIDE_TMP/probe'"

probe "read ~/.ssh" denied \
  "cat '$HOME/.ssh/config'"

if curl -s -m 2 -o /dev/null http://127.0.0.1:11434/api/tags; then
  probe "curl 127.0.0.1:11434 (Ollama up)" ok \
    "curl -sf -m 3 -o /dev/null http://127.0.0.1:11434/api/tags"
else
  echo "[SKIP] curl 127.0.0.1:11434 -- Ollama not running on this host"
fi

probe "curl https://example.com" denied \
  "curl -sf -m 3 -o /dev/null https://example.com"

probe "go build" ok \
  "cd '$PROJECT_ROOT' && GOCACHE='$SESSION_TMP/gocache' GOMODCACHE='$SESSION_TMP/gomodcache' GOTMPDIR='$SESSION_TMP/gotmp' go build ./..."

probe "go test" ok \
  "cd '$PROJECT_ROOT' && GOCACHE='$SESSION_TMP/gocache' GOMODCACHE='$SESSION_TMP/gomodcache' GOTMPDIR='$SESSION_TMP/gotmp' go test ./..."

probe "python3 -c 'print(1)'" ok \
  "python3 -c 'print(1)'"

MARKER_DIR="$(mktemp -d "$OUTSIDE_TMP/rmrf-victim.XXXXXX")"
touch "$MARKER_DIR/victim"
probe "rm -rf outside project" denied \
  "rm -rf '$MARKER_DIR'"
rm -rf "$MARKER_DIR"

echo
printf '%-32s %-9s %-8s %s\n' "PROBE" "EXPECTED" "GOT" "STATUS"
for row in "${ROWS[@]}"; do
  IFS='|' read -r name expect got status <<<"$row"
  printf '%-32s %-9s %-8s %s\n' "$name" "$expect" "$got" "$status"
done
echo
echo "PASS=$PASS FAIL=$FAIL"
[[ $FAIL -eq 0 ]]
