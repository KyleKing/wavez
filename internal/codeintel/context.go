package codeintel

import (
	"context"
	"fmt"
	"sort"
)

// TouchedRange names a line range in a file the caller is about to touch
// or has just changed, the seed for Context.
type TouchedRange struct {
	File  string
	Start int
	End   int
}

// ContextRequest is one Context call.
type ContextRequest struct {
	Touched []TouchedRange
	// TokenBudget caps the bundle's estimated size. Zero means unbounded.
	TokenBudget int
}

// ContextBundle is the ranked first-turn context for a model: the touched
// symbols, whatever one-hop edges the codegraph adapter recorded for them,
// and the tests covering the touched ranges.
type ContextBundle struct {
	Symbols    []Symbol
	Neighbors  []Edge
	Tests      []CoverageTest
	TokensUsed int
	Truncated  bool
}

// Context returns a ranked bundle for a model's first turn on the touched
// ranges, stopping once req.TokenBudget is spent. Ranking is deterministic
// given the same store, so the bundle is reproducible across calls.
func (s *Store) Context(ctx context.Context, req ContextRequest) (ContextBundle, error) {
	symbols, err := s.touchedSymbols(ctx, req.Touched)
	if err != nil {
		return ContextBundle{}, err
	}

	neighbors, err := s.oneHopNeighbours(ctx, symbols)
	if err != nil {
		return ContextBundle{}, err
	}

	tests, err := s.coveringTestsFor(ctx, req.Touched)
	if err != nil {
		return ContextBundle{}, err
	}

	return budgetBundle(symbols, neighbors, tests, req.TokenBudget), nil
}

func (s *Store) touchedSymbols(ctx context.Context, ranges []TouchedRange) ([]Symbol, error) {
	byID := make(map[int64]*Symbol)
	for _, r := range ranges {
		if err := s.collectTouchedSymbols(ctx, r, byID); err != nil {
			return nil, err
		}
	}

	symbols := make([]Symbol, 0, len(byID))
	for _, sym := range byID {
		symbols = append(symbols, *sym)
	}
	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].FilePath != symbols[j].FilePath {
			return symbols[i].FilePath < symbols[j].FilePath
		}
		if symbols[i].StartLine != symbols[j].StartLine {
			return symbols[i].StartLine < symbols[j].StartLine
		}

		return symbols[i].ID < symbols[j].ID
	})

	return symbols, nil
}

func (s *Store) collectTouchedSymbols(ctx context.Context, r TouchedRange, byID map[int64]*Symbol) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT symbols.id, symbols.file_id, files.path, symbols.kind, symbols.name,
		       symbols.start_byte, symbols.end_byte, symbols.start_line, symbols.end_line,
		       symbols.signature, symbols.doc
		FROM symbols JOIN files ON files.id = symbols.file_id
		WHERE files.path = ? AND symbols.start_line <= ? AND symbols.end_line >= ?`,
		r.File, r.End, r.Start)
	if err != nil {
		return fmt.Errorf("finding touched symbols in %s: %w", r.File, err)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only cursor, nothing actionable on close failure

	for rows.Next() {
		var sym Symbol
		if err := rows.Scan(&sym.ID, &sym.FileID, &sym.FilePath, &sym.Kind, &sym.Name,
			&sym.StartByte, &sym.EndByte, &sym.StartLine, &sym.EndLine, &sym.Signature, &sym.Doc); err != nil {
			return fmt.Errorf("scanning touched symbol: %w", err)
		}
		byID[sym.ID] = &sym
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading touched symbol rows: %w", err)
	}

	return nil
}

// symbolKey is the edges.src/edges.dst convention the codegraph adapter
// follows for its rows to resolve against this store's symbols: file path,
// symbol name, and start byte, colon-joined.
func symbolKey(path, name string, startByte uint) string {
	return fmt.Sprintf("%s:%s:%d", path, name, startByte)
}

func (s *Store) oneHopNeighbours(ctx context.Context, symbols []Symbol) ([]Edge, error) {
	byID := make(map[int64]*Edge)
	for i := range symbols {
		key := symbolKey(symbols[i].FilePath, symbols[i].Name, symbols[i].StartByte)
		if err := s.collectNeighbours(ctx, key, byID); err != nil {
			return nil, err
		}
	}

	edges := make([]Edge, 0, len(byID))
	for _, e := range byID {
		edges = append(edges, *e)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Confidence != edges[j].Confidence {
			return edges[i].Confidence > edges[j].Confidence
		}
		if edges[i].Kind != edges[j].Kind {
			return edges[i].Kind < edges[j].Kind
		}

		return edges[i].ID < edges[j].ID
	})

	return edges, nil
}

func (s *Store) collectNeighbours(ctx context.Context, key string, byID map[int64]*Edge) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, src, dst, kind, confidence FROM edges WHERE src = ? OR dst = ?`, key, key)
	if err != nil {
		return fmt.Errorf("finding neighbors of %s: %w", key, err)
	}
	defer func() { _ = rows.Close() }() //nolint:errcheck // read-only cursor, nothing actionable on close failure

	for rows.Next() {
		var e Edge
		if err := rows.Scan(&e.ID, &e.Src, &e.Dst, &e.Kind, &e.Confidence); err != nil {
			return fmt.Errorf("scanning edge row: %w", err)
		}
		byID[e.ID] = &e
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading edge rows: %w", err)
	}

	return nil
}

func (s *Store) coveringTestsFor(ctx context.Context, ranges []TouchedRange) ([]CoverageTest, error) {
	seen := make(map[string]CoverageTest)
	for _, r := range ranges {
		tests, err := s.CoveringTests(ctx, r.File, r.Start, r.End)
		if err != nil {
			return nil, err
		}
		for _, t := range tests {
			seen[t.TestID] = t
		}
	}

	tests := make([]CoverageTest, 0, len(seen))
	for _, t := range seen {
		tests = append(tests, t)
	}
	sort.Slice(tests, func(i, j int) bool { return tests[i].TestID < tests[j].TestID })

	return tests, nil
}

// bytesPerToken approximates a tokenizer at about four bytes per token,
// close enough for a budget that only needs to be consistent across calls
// against the same store.
const bytesPerToken = 4

func estimateTokens(s string) int {
	return (len(s) + bytesPerToken - 1) / bytesPerToken
}

func budgetBundle(symbols []Symbol, neighbors []Edge, tests []CoverageTest, budget int) ContextBundle {
	bundle := ContextBundle{}
	used := 0
	over := func(cost int) bool {
		return budget > 0 && used+cost > budget
	}

	for i := range symbols {
		cost := estimateTokens(symbols[i].Name + symbols[i].Signature + symbols[i].Doc)
		if over(cost) {
			bundle.Truncated = true
			break
		}
		bundle.Symbols = append(bundle.Symbols, symbols[i])
		used += cost
	}

	if !bundle.Truncated {
		for _, e := range neighbors {
			cost := estimateTokens(e.Src + e.Dst + e.Kind)
			if over(cost) {
				bundle.Truncated = true
				break
			}
			bundle.Neighbors = append(bundle.Neighbors, e)
			used += cost
		}
	}

	if !bundle.Truncated {
		for _, t := range tests {
			cost := estimateTokens(t.TestID)
			if over(cost) {
				bundle.Truncated = true
				break
			}
			bundle.Tests = append(bundle.Tests, t)
			used += cost
		}
	}

	bundle.TokensUsed = used

	return bundle
}
