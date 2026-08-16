package codeintel

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	_ "modernc.org/sqlite" // pure-Go sqlite driver, registers as "sqlite"
)

// Store is one project's code intelligence database: files, symbols, edges,
// full-text search, and coverage in a single SQLite file. All writes go
// through the store's single write path, so concurrent readers never see a
// torn index. A Store is safe for concurrent use.
type Store struct {
	db      *sql.DB
	writeMu sync.Mutex
}

// Open opens or creates the SQLite store at path, applying any pending
// migrations. Opening a store written by a newer build than this one
// returns an error rather than risking silent corruption.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening store %s: %w", path, err)
	}

	// A single-writer local store: WAL lets readers proceed during a write,
	// NORMAL synchronous is safe under WAL, and foreign_keys must be set per
	// connection since SQLite does not persist it in the file.
	for _, pragma := range []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA synchronous = NORMAL`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close() //nolint:errcheck // best-effort cleanup after an earlier failure
			return nil, fmt.Errorf("setting %s: %w", pragma, err)
		}
	}

	if err := applyMigrations(ctx, db); err != nil {
		_ = db.Close() //nolint:errcheck // best-effort cleanup after an earlier failure
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close releases the store's underlying database handle.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("closing store: %w", err)
	}

	return nil
}

// withWrite serializes fn against every other write on this store, so two
// goroutines can never interleave writes to the index.
func (s *Store) withWrite(ctx context.Context, fn func(tx *sql.Tx) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning write: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback() //nolint:errcheck // best-effort abort; the original error is already returned
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing write: %w", err)
	}

	return nil
}
