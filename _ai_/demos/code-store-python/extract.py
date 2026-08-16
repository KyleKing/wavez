# /// script
# requires-python = ">=3.10"
# dependencies = ["coverage>=7.13"]
# ///
"""Read a coverage.py SQLite context db and write store.sqlite (file, start_line, end_line, test).

Usage: uv run extract.py <path-to-.coverage> <path-to-store.sqlite>
"""

import sqlite3
import sys
from pathlib import Path

from coverage.numbits import numbits_to_nums


def line_ranges(nums: list[int]) -> list[tuple[int, int]]:
    ranges: list[tuple[int, int]] = []
    for n in sorted(nums):
        if ranges and n == ranges[-1][1] + 1:
            ranges[-1] = (ranges[-1][0], n)
        else:
            ranges.append((n, n))
    return ranges


def main() -> None:
    cov_path, store_path = sys.argv[1], sys.argv[2]
    Path(store_path).unlink(missing_ok=True)

    src = sqlite3.connect(cov_path)
    dst = sqlite3.connect(store_path)
    dst.execute("""
        CREATE TABLE coverage (
            file TEXT NOT NULL,
            start_line INTEGER NOT NULL,
            end_line INTEGER NOT NULL,
            test TEXT NOT NULL
        )
    """)

    rows = src.execute("""
        SELECT file.path, context.context, line_bits.numbits
        FROM line_bits
        JOIN file ON file.id = line_bits.file_id
        JOIN context ON context.id = line_bits.context_id
        WHERE context.context != ''
    """).fetchall()

    inserted = 0
    for path, test, numbits in rows:
        nums = numbits_to_nums(numbits)
        for start, end in line_ranges(nums):
            dst.execute(
                "INSERT INTO coverage (file, start_line, end_line, test) VALUES (?, ?, ?, ?)",
                (path, start, end, test),
            )
            inserted += 1

    dst.commit()
    print(f"wrote {inserted} rows to {store_path} (from {len(rows)} file/test line_bits rows)")


if __name__ == "__main__":
    main()
