#!/usr/bin/env bash
# Run a command inside the wavez.sb Seatbelt profile.
#
# Usage: ./run.sh [PROJECT_ROOT] -- command [args...]
#   PROJECT_ROOT defaults to ./proj (the demo Go module) if omitted.
#
# Creates one session tmp dir under $TMPDIR, redirects GOCACHE/GOMODCACHE/
# GOTMPDIR into it (see README.md: neither Go cache needs write access
# outside SESSION_TMP once redirected), and resolves every path param
# through realpath first: Seatbelt's subpath matching is a literal prefix
# match against the post-symlink path, and /tmp, /var, /etc are symlinks
# into /private on macOS, so an unresolved path silently fails to match.

set -euo pipefail

DEMO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROFILE="$DEMO_DIR/${WAVEZ_PROFILE:-wavez.sb}"

PROJECT_ROOT="$DEMO_DIR/proj"
if [[ "${1:-}" != "--" && -n "${1:-}" ]]; then
  PROJECT_ROOT="$1"
  shift
fi
if [[ "${1:-}" == "--" ]]; then
  shift
fi
if [[ $# -eq 0 ]]; then
  echo "usage: $0 [PROJECT_ROOT] -- command [args...]" >&2
  exit 2
fi

PROJECT_ROOT="$(realpath "$PROJECT_ROOT")"
SESSION_TMP="$(realpath "$(mktemp -d "${TMPDIR%/}/wavez-session.XXXXXX")")"
trap 'rm -rf "$SESSION_TMP"' EXIT

mkdir -p "$SESSION_TMP/gocache" "$SESSION_TMP/gomodcache" "$SESSION_TMP/gotmp"
export GOCACHE="$SESSION_TMP/gocache"
export GOMODCACHE="$SESSION_TMP/gomodcache"
export GOTMPDIR="$SESSION_TMP/gotmp"

sandbox-exec \
  -D PROJECT_ROOT="$PROJECT_ROOT" \
  -D SESSION_TMP="$SESSION_TMP" \
  -D HOME="$HOME" \
  -f "$PROFILE" \
  "$@"
exit $?
