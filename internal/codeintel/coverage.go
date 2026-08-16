package codeintel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CoverageRow is one line range a test exercised in file, produced by a
// gate adapter (Go coverprofile parsing, coverage.py dynamic contexts).
type CoverageRow struct {
	File  string
	Start int
	End   int
}

// CoverageTest identifies a test whose coverage the store holds.
type CoverageTest struct {
	TestID   string
	TestHash string
}

// WriteCoverage replaces the stored coverage for testID with rows, keyed by
// testHash: if testHash already matches what is stored, the call is a
// no-op that touches no rows, the same incremental-by-hash contract Index
// uses for source files.
func (s *Store) WriteCoverage(ctx context.Context, testID, testHash string, rows []CoverageRow) error {
	return s.withWrite(ctx, func(tx *sql.Tx) error {
		var current sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT test_hash FROM coverage WHERE test_id = ? LIMIT 1`, testID).Scan(&current)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("reading coverage hash for %s: %w", testID, err)
		}
		if current.Valid && current.String == testHash {
			return nil
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM coverage WHERE test_id = ?`, testID); err != nil {
			return fmt.Errorf("clearing coverage for %s: %w", testID, err)
		}
		for _, row := range rows {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO coverage (file, start_line, end_line, test_id, test_hash) VALUES (?, ?, ?, ?, ?)`,
				row.File, row.Start, row.End, testID, testHash); err != nil {
				return fmt.Errorf("inserting coverage for %s: %w", testID, err)
			}
		}

		return nil
	})
}

// CoveringTests returns every test whose recorded coverage overlaps
// [start, end] in file, ordered by test ID for a deterministic result.
func (s *Store) CoveringTests(ctx context.Context, file string, start, end int) ([]CoverageTest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT test_id, test_hash FROM coverage
		 WHERE file = ? AND start_line <= ? AND end_line >= ?
		 ORDER BY test_id`,
		file, end, start)
	if err != nil {
		return nil, fmt.Errorf("querying coverage for %s:%d-%d: %w", file, start, end, err)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only cursor, nothing actionable on close failure

	var tests []CoverageTest
	for rows.Next() {
		var t CoverageTest
		if err := rows.Scan(&t.TestID, &t.TestHash); err != nil {
			return nil, fmt.Errorf("scanning coverage test row: %w", err)
		}
		tests = append(tests, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading coverage test rows: %w", err)
	}

	return tests, nil
}
