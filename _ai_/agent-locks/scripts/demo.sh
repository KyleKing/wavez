#!/usr/bin/env bash
# Drives the hook with synthetic stdin for two simulated sessions and asserts what the
# model would see. Uses a scratch state dir and a scratch repo, so it never touches
# real session state.
set -uo pipefail

BIN=${BIN:-$(cd "$(dirname "$0")/.." && pwd)/bin/agentlocks}
WORK=$(mktemp -d)
export AGENT_LOCKS_DIR="$WORK/state"
export AGENT_LOCKS_COOLDOWN=10m
REPO="$WORK/repo"
trap 'rm -rf "$WORK"' EXIT

mkdir -p "$REPO/internal/api" "$REPO/cmd/tui"
git -C "$REPO" init -q
: >"$REPO/internal/api/routes.go"
: >"$REPO/internal/api/handler.go"

pass=0 fail=0
check() { # check <label> <expect-substring|-> <actual>
  local label=$1 want=$2 got=$3
  if [[ $want == "-" ]]; then
    if [[ -z $got ]]; then printf '  PASS  %s\n' "$label"; pass=$((pass + 1));
    else printf '  FAIL  %s\n        wanted silence, got: %s\n' "$label" "$got"; fail=$((fail + 1)); fi
  elif [[ $got == *"$want"* ]]; then
    printf '  PASS  %s\n' "$label"; pass=$((pass + 1))
  else
    printf '  FAIL  %s\n        wanted %q in: %s\n' "$label" "$want" "${got:-<empty>}"; fail=$((fail + 1))
  fi
}

hook() { # hook <event> <json>
  printf '%s' "$2" | "$BIN" hook "$1"
}

evt() { # evt <session> <agent> <tool> <file> [command]
  printf '{"session_id":"%s","agent_id":"%s","cwd":"%s","tool_name":"%s","tool_input":{"file_path":"%s","command":"%s"}}' \
    "$1" "$2" "$REPO" "$3" "$4" "${5:-}"
}

echo "== two sessions, same subtree =="
hook session-start "$(evt sessionAAAA "" "" "")" >/dev/null
hook session-start "$(evt sessionBBBB "" "" "")" >/dev/null

out=$(hook pre-tool-use "$(evt sessionAAAA "" Edit "$REPO/internal/api/routes.go")")
check "first writer sees nothing" - "$out"

hook post-tool-use "$(evt sessionAAAA "" Edit "$REPO/internal/api/routes.go")" >/dev/null

out=$(hook pre-tool-use "$(evt sessionBBBB "" Edit "$REPO/internal/api/handler.go")")
check "second writer warned about the subtree" "internal/api is being edited by session sessionA" "$out"
check "warning is context, not a decision" "additionalContext" "$out"
check "no permission decision is emitted" - "$(grep -o permissionDecision <<<"$out")"

out=$(hook pre-tool-use "$(evt sessionBBBB "" Edit "$REPO/internal/api/handler.go")")
check "repeat write is suppressed by cooldown" - "$out"

echo "== unrelated subtree =="
out=$(hook pre-tool-use "$(evt sessionBBBB "" Edit "$REPO/cmd/tui/model.go")")
check "different subtree is quiet" - "$out"

echo "== subagent of the same session =="
out=$(hook pre-tool-use "$(evt sessionAAAA agent-7 Edit "$REPO/internal/api/routes.go")")
check "subagent is a distinct actor" "internal/api is being edited by session sessionA" "$out"

echo "== bash writes =="
hook post-tool-use "$(evt sessionAAAA "" Bash "" "gofmt -w cmd/tui/model.go")" >/dev/null
out=$(hook pre-tool-use "$(evt sessionBBBB "" Edit "$REPO/cmd/tui/model.go")")
check "gofmt -w registers a subtree" "cmd/tui is being edited" "$out"

out=$(hook pre-tool-use "$(evt sessionBBBB "" Bash "" "cat internal/api/routes.go")")
check "reading via bash warns about nothing new" - "$out"

echo "== manual claim =="
(cd "$REPO" && "$BIN" claim cmd/tui -m "hand-editing the layout" >/dev/null)
export AGENT_LOCKS_COOLDOWN=0s
out=$(hook pre-tool-use "$(evt sessionBBBB "" Edit "$REPO/cmd/tui/model.go")")
check "manual claim surfaces to the agent" "manually claimed by the user" "$out"
check "claim note is included" "hand-editing the layout" "$out"

(cd "$REPO" && "$BIN" release cmd/tui >/dev/null)
out=$(hook pre-tool-use "$(evt sessionBBBB "" Edit "$REPO/cmd/tui/model.go")")
check "release drops the manual claim" - "$(grep -o 'manually claimed' <<<"$out")"

echo "== commit downgrades the lease =="
hook post-tool-use "$(evt sessionAAAA "" Bash "" "git commit -m 'feat: x'")" >/dev/null
out=$(hook pre-tool-use "$(evt sessionBBBB "" Edit "$REPO/internal/api/handler.go")")
check "post-commit reads as rebase risk" "Rebase risk" "$out"

echo "== session end releases =="
hook session-end "$(evt sessionAAAA "" "" "")" >/dev/null
out=$(hook pre-tool-use "$(evt sessionBBBB "" Edit "$REPO/internal/api/handler.go")")
check "ended session holds nothing" - "$out"

echo "== malformed input fails open =="
out=$(printf 'not json' | "$BIN" hook pre-tool-use; echo "exit=$?")
check "bad stdin exits 0 and says nothing" "exit=0" "$out"

echo
echo "== status =="
"$BIN" status || true
echo
printf '%d passed, %d failed\n' "$pass" "$fail"
[[ $fail -eq 0 ]]
