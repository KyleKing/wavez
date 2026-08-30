package codeintel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kyleking/wavez/internal/codeintel/lang"
)

// IndexStats summarizes one Index call. FilesUnchanged files are, by
// construction, never written to: their content hash matched and no SQL
// statement referenced their rows.
type IndexStats struct {
	// EdgesUnavailable carries why the edge copy could not run, empty when
	// it did. A graph query over an empty edges table means something
	// different depending on which it is, so the reason has to travel with
	// the stats rather than be inferred from a zero count.
	EdgesUnavailable string
	// MatchesTotal is how many rows the query matched before Limit cut the
	// result set, so a caller can tell a complete answer from a page of one.
	// It belongs to the query rather than the index, and travels here
	// because this is what a search already hands back beside its results.
	MatchesTotal   int
	FilesScanned   int
	FilesIndexed   int
	FilesUnchanged int
	FilesRemoved   int
	SymbolsIndexed int
}

// skipDirs never descends into these directory names while scanning. They
// hold a project's dependencies rather than its own code, which the index is
// for: Go keeps its dependencies outside the tree, so the first Python
// project the indexer met put 34,934 of its 35,888 symbols in `.venv` and
// buried the one the run was looking for under pytest and pluggy internals.
var skipDirs = map[string]bool{
	".git":          true,
	".mypy_cache":   true,
	".pytest_cache": true,
	".ruff_cache":   true,
	".tox":          true,
	".venv":         true,
	"__pycache__":   true,
	"node_modules":  true,
	"site-packages": true,
	"venv":          true,
}

// Index walks root for files registry claims, reparsing only those whose
// content hash changed since the last Index call, and removes rows for
// files that no longer exist. A re-index of an unchanged tree issues no
// write statements at all.
func (s *Store) Index(ctx context.Context, root string, registry *lang.Registry) (IndexStats, error) {
	found, err := scanFiles(root, registry)
	if err != nil {
		return IndexStats{}, err
	}

	var stats IndexStats
	err = s.withWrite(ctx, func(tx *sql.Tx) error {
		existing, err := loadExistingFiles(ctx, tx)
		if err != nil {
			return err
		}

		run := &indexRun{tx: tx, registry: registry, existing: existing, stats: &stats}

		seen, err := run.indexScannedFiles(ctx, found)
		if err != nil {
			return err
		}

		return run.removeStaleFiles(ctx, seen)
	})
	if err != nil {
		return IndexStats{}, err
	}

	return stats, nil
}

// indexRun bundles the state one Index call threads through its helpers, so
// each helper takes one argument instead of the same five every time.
type indexRun struct {
	tx       *sql.Tx
	registry *lang.Registry
	existing map[string]File
	stats    *IndexStats
}

// indexScannedFiles writes rows for every file whose content hash is new or
// changed, and returns the set of relative paths seen on disk this call.
func (run *indexRun) indexScannedFiles(ctx context.Context, found []scannedFile) (map[string]bool, error) {
	seen := make(map[string]bool, len(found))
	for _, sf := range found {
		run.stats.FilesScanned++
		seen[sf.relPath] = true

		if err := run.indexOneFile(ctx, sf); err != nil {
			return nil, err
		}
	}

	return seen, nil
}

func (run *indexRun) indexOneFile(ctx context.Context, sf scannedFile) error {
	content, err := os.ReadFile(sf.absPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", sf.absPath, err)
	}
	hash := contentHash(content)

	prior, existed := run.existing[sf.relPath]
	if existed && prior.ContentHash == hash {
		run.stats.FilesUnchanged++

		return nil
	}

	symbols, err := run.registry.Extract(sf.relPath, content)
	if err != nil {
		return fmt.Errorf("indexing %s: %w", sf.relPath, err)
	}

	info, err := os.Stat(sf.absPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", sf.absPath, err)
	}

	fileID := prior.ID
	if existed {
		if err := deleteFileIndexRows(ctx, run.tx, fileID); err != nil {
			return err
		}
		if err := updateFileRow(ctx, run.tx, fileID, hash, info.ModTime().UnixNano(), info.Size()); err != nil {
			return err
		}
	} else {
		fileID, err = insertFileRow(ctx, run.tx, sf.relPath, hash, info.ModTime().UnixNano(), info.Size())
		if err != nil {
			return err
		}
	}

	n, err := insertFileIndexRows(ctx, run.tx, fileID, sf.relPath, content, symbols)
	if err != nil {
		return err
	}
	run.stats.SymbolsIndexed += n
	run.stats.FilesIndexed++

	return nil
}

func (run *indexRun) removeStaleFiles(ctx context.Context, seen map[string]bool) error {
	for relPath, f := range run.existing {
		if seen[relPath] {
			continue
		}
		if err := deleteFileIndexRows(ctx, run.tx, f.ID); err != nil {
			return err
		}
		if _, err := run.tx.ExecContext(ctx, `DELETE FROM files WHERE id = ?`, f.ID); err != nil {
			return fmt.Errorf("deleting file row %s: %w", relPath, err)
		}
		run.stats.FilesRemoved++
	}

	return nil
}

type scannedFile struct {
	relPath string
	absPath string
}

func scanFiles(root string, registry *lang.Registry) ([]scannedFile, error) {
	var found []scannedFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking %s: %w", path, err)
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}

			return nil
		}
		if !registry.Claims(path) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relativizing %s: %w", path, err)
		}
		found = append(found, scannedFile{
			relPath: filepath.ToSlash(rel),
			absPath: path,
		})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scanning %s: %w", root, err)
	}

	return found, nil
}

func contentHash(content []byte) string {
	sum := sha256.Sum256(content)

	return hex.EncodeToString(sum[:])
}

func loadExistingFiles(ctx context.Context, tx *sql.Tx) (map[string]File, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, path, content_hash, mtime, size FROM files`)
	if err != nil {
		return nil, fmt.Errorf("loading files: %w", err)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only cursor, nothing actionable on close failure

	existing := make(map[string]File)
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.Path, &f.ContentHash, &f.MTime, &f.Size); err != nil {
			return nil, fmt.Errorf("scanning file row: %w", err)
		}
		existing[f.Path] = f
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading file rows: %w", err)
	}

	return existing, nil
}

func insertFileRow(ctx context.Context, tx *sql.Tx, path, hash string, mtime, size int64) (int64, error) {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO files (path, content_hash, mtime, size) VALUES (?, ?, ?, ?)`,
		path, hash, mtime, size)
	if err != nil {
		return 0, fmt.Errorf("inserting file %s: %w", path, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading id for inserted file %s: %w", path, err)
	}

	return id, nil
}

func updateFileRow(ctx context.Context, tx *sql.Tx, fileID int64, hash string, mtime, size int64) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE files SET content_hash = ?, mtime = ?, size = ? WHERE id = ?`,
		hash, mtime, size, fileID)
	if err != nil {
		return fmt.Errorf("updating file %d: %w", fileID, err)
	}

	return nil
}

// deleteFileIndexRows removes a file's symbols and every fts row derived
// from it. FTS is a virtual table with no foreign key to files or symbols,
// so these deletes cannot ride on ON DELETE CASCADE and must run explicitly.
func deleteFileIndexRows(ctx context.Context, tx *sql.Tx, fileID int64) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM fts WHERE kind = 'symbol' AND ref_id IN (SELECT id FROM symbols WHERE file_id = ?)`,
		fileID); err != nil {
		return fmt.Errorf("deleting symbol fts rows for file %d: %w", fileID, err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM fts WHERE kind IN ('path', 'file') AND ref_id = ?`, fileID); err != nil {
		return fmt.Errorf("deleting file fts rows for file %d: %w", fileID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM symbols WHERE file_id = ?`, fileID); err != nil {
		return fmt.Errorf("deleting symbols for file %d: %w", fileID, err)
	}

	return nil
}

func insertFileIndexRows(
	ctx context.Context, tx *sql.Tx, fileID int64, relPath string, content []byte, symbols []lang.Symbol,
) (int, error) {
	for _, sym := range symbols {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO symbols (file_id, kind, name, start_byte, end_byte, start_line, end_line, signature, doc)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fileID, sym.Kind, sym.Name, sym.StartByte, sym.EndByte, sym.StartLine, sym.EndLine, sym.Signature, sym.Doc)
		if err != nil {
			return 0, fmt.Errorf("inserting symbol %s: %w", sym.Name, err)
		}
		symbolID, err := res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("reading symbol id for %s: %w", sym.Name, err)
		}
		text := strings.Join([]string{sym.Name, sym.Signature, sym.Doc}, "\n")
		if err := insertFTS(ctx, tx, "symbol", symbolID, text); err != nil {
			return 0, err
		}
	}

	if err := insertFTS(ctx, tx, "path", fileID, relPath); err != nil {
		return 0, err
	}
	if err := insertFTS(ctx, tx, "file", fileID, string(content)); err != nil {
		return 0, err
	}

	return len(symbols), nil
}

func insertFTS(ctx context.Context, tx *sql.Tx, kind string, refID int64, text string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO fts (text, kind, ref_id) VALUES (?, ?, ?)`, text, kind, refID)
	if err != nil {
		return fmt.Errorf("inserting fts row (%s, %d): %w", kind, refID, err)
	}

	return nil
}
