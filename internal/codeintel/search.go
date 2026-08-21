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

// LineMatch is one line of a file that held a query term, with Line
// counting from 1.
type LineMatch struct {
	Text string
	Line int
}

// SearchResult is one match. Exactly one of Symbol or Edge is set,
// depending on the query's mode.
//
// A file-level match carries Lines rather than the file text it matched
// through. A hit that names a file and not a place in it leaves the caller
// to find the place, and measured on a dogfood run that meant falling
// straight back to grep -rn on the file search had just found.
type SearchResult struct {
	Symbol *Symbol
	Edge   *Edge
	Kind   string
	File   string
	Text   string
	Lines  []LineMatch
}

// maxLineMatches caps the lines reported for one file hit. Enough to show
// where a symbol is used, short of pasting a file the caller can read.
const maxLineMatches = 5

// maxLineMatchWidth truncates a matched line, so one minified or generated
// line cannot swamp a result set.
const maxLineMatchWidth = 200

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
	fields := queryTerms(text)
	if len(fields) == 0 {
		return `""`
	}

	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		quoted = append(quoted, `"`+f+`"`)
	}

	return strings.Join(quoted, " OR ")
}

func queryTerms(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
}

// matchingLines finds where terms occur in content. The trigram tokenizer
// matches without regard to case, so this does too, or a hit would report
// no lines for the query that found it.
func matchingLines(content string, terms []string) []LineMatch {
	if len(terms) == 0 {
		return nil
	}

	lowered := make([]string, 0, len(terms))
	for _, t := range terms {
		lowered = append(lowered, strings.ToLower(t))
	}

	var matches []LineMatch
	for i, line := range strings.Split(content, "\n") {
		if len(matches) >= maxLineMatches {
			break
		}
		if !holdsAnyTerm(strings.ToLower(line), lowered) {
			continue
		}
		matches = append(matches, LineMatch{Line: i + 1, Text: truncateLine(strings.TrimSpace(line))})
	}

	return matches
}

func holdsAnyTerm(line string, terms []string) bool {
	for _, t := range terms {
		if strings.Contains(line, t) {
			return true
		}
	}

	return false
}

func truncateLine(line string) string {
	if len(line) <= maxLineMatchWidth {
		return line
	}

	return line[:maxLineMatchWidth] + "..."
}

// CountMatches is how many rows a fuzzy query matches in full, which a
// limited result set cannot say for itself. A model that cannot tell "these
// are all of them" from "these are the first twenty" reaches for grep: one
// dogfood run spent 44 shell calls, most of them `grep -rn ... | head -30`,
// establishing what a count would have told it.
func (s *Store) CountMatches(ctx context.Context, q SearchQuery) (int, error) {
	if q.Mode != SearchFuzzy {
		return 0, nil
	}

	var total int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM fts WHERE fts MATCH ?`, ftsQuery(q.Text)).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("counting matches for %q: %w", q.Text, err)
	}

	return total, nil
}

func (s *Store) searchFuzzy(ctx context.Context, q SearchQuery) ([]SearchResult, error) {
	terms := queryTerms(q.Text)
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
		result, err := s.hydrateFTSResult(ctx, kind, refID, text, terms)
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

func (s *Store) hydrateFTSResult(
	ctx context.Context, kind string, refID int64, text string, terms []string,
) (SearchResult, error) {
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

		if kind == "file" {
			return SearchResult{Kind: kind, File: path, Lines: matchingLines(text, terms)}, nil
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
