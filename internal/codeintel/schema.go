package codeintel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrSchemaTooNew reports a store file stamped with a schema version this
// build does not know how to read.
var ErrSchemaTooNew = errors.New("store schema is newer than this build supports")

// schemaVersion is the store's current migration level. Store.Open refuses to
// open a file stamped with a version higher than this.
const schemaVersion = 1

// migrations holds one entry per schema version, applied in order starting
// from the store's current version. Index 0 upgrades an empty store to
// version 1, index 1 would upgrade version 1 to 2, and so on.
var migrations = []string{
	migrationV1,
}

const migrationV1 = `
CREATE TABLE files (
	id INTEGER PRIMARY KEY,
	path TEXT NOT NULL UNIQUE,
	content_hash TEXT NOT NULL,
	mtime INTEGER NOT NULL,
	size INTEGER NOT NULL
);

CREATE TABLE symbols (
	id INTEGER PRIMARY KEY,
	file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	name TEXT NOT NULL,
	start_byte INTEGER NOT NULL,
	end_byte INTEGER NOT NULL,
	start_line INTEGER NOT NULL,
	end_line INTEGER NOT NULL,
	signature TEXT NOT NULL DEFAULT '',
	doc TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_symbols_file ON symbols(file_id);
CREATE INDEX idx_symbols_name ON symbols(name);

-- edges is a declared seam for the codegraph adapter (DESIGN.md "Code
-- intelligence"): src/dst are symbol keys ("path:name:start_byte") rather
-- than a foreign key to symbols.id, because the adapter's node identity is
-- its own and rows are copied in, not joined live.
CREATE TABLE edges (
	id INTEGER PRIMARY KEY,
	src TEXT NOT NULL,
	dst TEXT NOT NULL,
	kind TEXT NOT NULL,
	confidence REAL NOT NULL
);

CREATE INDEX idx_edges_src ON edges(src);
CREATE INDEX idx_edges_dst ON edges(dst);

CREATE VIRTUAL TABLE fts USING fts5(
	text,
	kind UNINDEXED,
	ref_id UNINDEXED,
	tokenize = 'trigram'
);

CREATE TABLE coverage (
	id INTEGER PRIMARY KEY,
	file TEXT NOT NULL,
	start_line INTEGER NOT NULL,
	end_line INTEGER NOT NULL,
	test_id TEXT NOT NULL,
	test_hash TEXT NOT NULL
);

CREATE INDEX idx_coverage_file_line ON coverage(file, start_line, end_line);
`

func applyMigrations(ctx context.Context, db *sql.DB) error {
	var current int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	if current > schemaVersion {
		return fmt.Errorf("%w: store is version %d, this build supports up to %d",
			ErrSchemaTooNew, current, schemaVersion)
	}

	for version := current; version < schemaVersion; version++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("beginning migration %d: %w", version+1, err)
		}
		if _, err := tx.ExecContext(ctx, migrations[version]); err != nil {
			_ = tx.Rollback() //nolint:errcheck // best-effort abort; the original error is already returned
			return fmt.Errorf("applying migration %d: %w", version+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, version+1)); err != nil {
			_ = tx.Rollback() //nolint:errcheck // best-effort abort; the original error is already returned
			return fmt.Errorf("stamping migration %d: %w", version+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing migration %d: %w", version+1, err)
		}
	}

	return nil
}
