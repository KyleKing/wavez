package codeintel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// SearchMode selects a retrieval strategy for Search.
type SearchMode string

const (
	// SearchFuzzy matches symbol names, paths, and file text through the
	// trigram FTS index.
	SearchFuzzy SearchMode = "fuzzy"
	// SearchGraph walks the edges table one hop from a seed symbol key.
	// EdgeAdapter fills that table from codegraph, so this mode returns
	// nothing on a project codegraph has not indexed.
	SearchGraph SearchMode = "graph"
	// SearchSemantic is declared for v0.2 and returns ErrModeUnimplemented.
	SearchSemantic SearchMode = "semantic"
	// SearchHybrid is declared for v0.2 and returns ErrModeUnimplemented.
	SearchHybrid SearchMode = "hybrid"
)

// ErrModeUnimplemented reports a SearchMode this build declares but does
// not yet implement.
var ErrModeUnimplemented = errors.New("search mode not implemented")

// ErrUnknownSearchMode reports a SearchMode value Search does not
// recognize at all.
var ErrUnknownSearchMode = errors.New("unknown search mode")

// SearchQuery is one Search request.
type SearchQuery struct {
	Mode SearchMode
	// Text is the fuzzy match string in SearchFuzzy, or the edges.src/dst
	// seed key in SearchGraph.
	Text  string
	Limit int
}

// Edge is one row in the (currently unpopulated) edges table.
type Edge struct {
	Src        string
	Dst        string
	Kind       string
	ID         int64
	Confidence float64
}

// SearchResult is one match. Exactly one of Symbol or Edge is set,
// depending on the query's mode.
type SearchResult struct {
	Symbol *Symbol
	Edge   *Edge
	Kind   string
	File   string
	Text   string
}

const defaultSearchLimit = 20

// Search is the store's one query entry point. Fuzzy and graph modes are
// implemented in v0.1; semantic and hybrid are declared but return
// ErrModeUnimplemented until v0.2. Results are ordered deterministically
// for a given store so a model's context stays reproducible.
func (s *Store) Search(ctx context.Context, q SearchQuery) ([]SearchResult, error) {
	if q.Limit <= 0 {
		q.Limit = defaultSearchLimit
	}
	switch q.Mode {
	case SearchFuzzy:
		return s.searchFuzzy(ctx, q)
	case SearchGraph:
		return s.searchGraph(ctx, q)
	case SearchSemantic, SearchHybrid:
		return nil, fmt.Errorf("mode %q: %w", q.Mode, ErrModeUnimplemented)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownSearchMode, q.Mode)
	}
}

// ftsQuery makes arbitrary text safe to hand FTS5. Unquoted input is parsed as
// query syntax, so anything holding a path, an operator, or punctuation is a
// syntax error rather than a search. Each run of query-safe characters becomes
// its own quoted phrase, which also keeps a caller from injecting operators.
//
// Terms are joined with OR rather than FTS5's implicit AND. A caller listing
// several names wants the files mentioning any of them, and requiring one
// document to hold all of them answers "no matches" to a query whose terms
// each exist: measured on this repo, a five-symbol query returned nothing
// across 473 indexed files and the model abandoned the tool for shell grep
// for the rest of the run. Ranking by bm25 still puts a document matching
// every term above one matching a single term, so precision survives.
func ftsQuery(text string) string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	if len(fields) == 0 {
		return `""`
	}

	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted = append(quoted, `"`+f+`"`)
	}

	return strings.Join(quoted, " OR ")
}

func (s *Store) searchFuzzy(ctx context.Context, q SearchQuery) ([]SearchResult, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT kind, ref_id, text FROM fts WHERE fts MATCH ? ORDER BY rank, kind, ref_id LIMIT ?`,
		ftsQuery(q.Text), q.Limit)
	if err != nil {
		return nil, fmt.Errorf("fuzzy search %q: %w", q.Text, err)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only cursor, nothing actionable on close failure

	var results []SearchResult
	for rows.Next() {
		var kind string
		var refID int64
		var text string
		if err := rows.Scan(&kind, &refID, &text); err != nil {
			return nil, fmt.Errorf("scanning fts row: %w", err)
		}
		result, err := s.hydrateFTSResult(ctx, kind, refID, text)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading fts rows: %w", err)
	}

	return results, nil
}

func (s *Store) hydrateFTSResult(ctx context.Context, kind string, refID int64, text string) (SearchResult, error) {
	switch kind {
	case "symbol":
		sym, err := s.symbolByID(ctx, refID)
		if err != nil {
			return SearchResult{}, err
		}

		return SearchResult{Kind: kind, File: sym.FilePath, Text: text, Symbol: &sym}, nil
	default: // "path", "file"
		var path string
		if err := s.db.QueryRowContext(ctx, `SELECT path FROM files WHERE id = ?`, refID).Scan(&path); err != nil {
			return SearchResult{}, fmt.Errorf("resolving file %d: %w", refID, err)
		}

		return SearchResult{Kind: kind, File: path, Text: text}, nil
	}
}

func (s *Store) searchGraph(ctx context.Context, q SearchQuery) ([]SearchResult, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, src, dst, kind, confidence FROM edges WHERE src = ? OR dst = ?
		 ORDER BY confidence DESC, kind, id LIMIT ?`,
		q.Text, q.Text, q.Limit)
	if err != nil {
		return nil, fmt.Errorf("graph search %q: %w", q.Text, err)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only cursor, nothing actionable on close failure

	var results []SearchResult
	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.ID, &e.Src, &e.Dst, &e.Kind, &e.Confidence); err != nil {
			return nil, fmt.Errorf("scanning edge row: %w", err)
		}
		results = append(results, SearchResult{Kind: "edge", Text: e.Kind, Edge: &e})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading edge rows: %w", err)
	}

	return results, nil
}

func (s *Store) symbolByID(ctx context.Context, id int64) (Symbol, error) {
	return scanSymbol(s.db.QueryRowContext(ctx, `
		SELECT symbols.id, symbols.file_id, files.path, symbols.kind, symbols.name,
		       symbols.start_byte, symbols.end_byte, symbols.start_line, symbols.end_line,
		       symbols.signature, symbols.doc
		FROM symbols JOIN files ON files.id = symbols.file_id
		WHERE symbols.id = ?`, id))
}

func scanSymbol(row *sql.Row) (Symbol, error) {
	var sym Symbol
	err := row.Scan(&sym.ID, &sym.FileID, &sym.FilePath, &sym.Kind, &sym.Name,
		&sym.StartByte, &sym.EndByte, &sym.StartLine, &sym.EndLine, &sym.Signature, &sym.Doc)
	if err != nil {
		return Symbol{}, fmt.Errorf("scanning symbol: %w", err)
	}

	return sym, nil
}
