"""List tests covering a file/line-range in store.sqlite.

Usage: uv run select.py <store.sqlite> <file> <start_line> <end_line>
"""

import sqlite3
import sys


def main() -> None:
    store_path, file, start, end = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
    conn = sqlite3.connect(store_path)
    rows = conn.execute(
        """
        SELECT DISTINCT test FROM coverage
        WHERE file = ? AND start_line <= ? AND end_line >= ?
        ORDER BY test
        """,
        (file, end, start),
    ).fetchall()
    for (test,) in rows:
        print(test)
    print(f"-- {len(rows)} test(s) cover {file}:{start}-{end}", file=sys.stderr)


if __name__ == "__main__":
    main()
