#!/usr/bin/env bash
# select.sh: given a changed file (and optional line range), print the tests
# that cover those lines directly. If nothing covers the exact lines, fall
# back to tests exercising any file in a package that transitively imports
# the changed file's package.
#
# Usage: select.sh <repo-relative-file> [start_line] [end_line]
# Example: select.sh internal/vcs/git.go 40 80

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DB="${STORE_DB:-$SCRIPT_DIR/store.sqlite}"
MODULE_PREFIX="${MODULE_PREFIX:-github.com/kyleking/gh-repo-dashboard}"

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <repo-relative-file> [start_line] [end_line]" >&2
  exit 1
fi

REL_FILE="$1"
START_LINE="${2:-1}"
END_LINE="${3:-999999}"
FILE_KEY="$MODULE_PREFIX/$REL_FILE"

echo "# change: $REL_FILE:$START_LINE-$END_LINE"
echo "# file key: $FILE_KEY"
echo

echo "## direct coverage (tests exercising these lines)"
DIRECT=$(sqlite3 "$DB" <<SQL
SELECT DISTINCT test FROM coverage
WHERE file = '$FILE_KEY'
  AND start_line <= $END_LINE
  AND end_line >= $START_LINE
ORDER BY test;
SQL
)
if [[ -n "$DIRECT" ]]; then
  echo "$DIRECT"
else
  echo "(none)"
fi
echo

DIRECT_COUNT=$(echo "$DIRECT" | grep -c . || true)
if [[ "$DIRECT_COUNT" -gt 0 ]]; then
  exit 0
fi

echo "## fallback: importer-package tests (no direct line coverage found)"
PKG=$(sqlite3 "$DB" "SELECT pkg FROM file_pkg WHERE file = '$FILE_KEY' LIMIT 1;")
if [[ -z "$PKG" ]]; then
  echo "(file not found in file_pkg table: $FILE_KEY)"
  exit 0
fi
echo "# package: $PKG"

IMPORTERS=$(sqlite3 "$DB" <<SQL
WITH RECURSIVE importers(pkg) AS (
  SELECT '$PKG'
  UNION
  SELECT imports.src_pkg
  FROM imports
  JOIN importers ON imports.dst_pkg = importers.pkg
)
SELECT pkg FROM importers;
SQL
)
echo "# importer packages (incl. self): $(echo "$IMPORTERS" | wc -l | tr -d ' ')"

IMPORTER_LIST=$(echo "$IMPORTERS" | sed "s/.*/'&'/" | paste -sd, -)
FALLBACK=$(sqlite3 "$DB" <<SQL
SELECT DISTINCT c.test
FROM coverage c
JOIN file_pkg fp ON fp.file = c.file
WHERE fp.pkg IN ($IMPORTER_LIST)
ORDER BY c.test;
SQL
)
if [[ -n "$FALLBACK" ]]; then
  echo "$FALLBACK"
else
  echo "(none)"
fi
