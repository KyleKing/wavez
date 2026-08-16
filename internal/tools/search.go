package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/tool"
)

var searchSchema = buildSchema(map[string]schemaProperty{
	"mode": {
		Type: schemaTypeString,
		Enum: []string{"fuzzy", "graph", "semantic", "hybrid"},
		Description: "Retrieval strategy. fuzzy matches symbol names, paths, and file text. " +
			"graph walks call/reference edges one hop from query. semantic and hybrid are " +
			"not yet available and return an error.",
	},
	"query": {
		Type: schemaTypeString,
		Description: "For fuzzy mode, a search string (FTS5 syntax). For graph mode, a " +
			"symbol key to walk edges from.",
	},
	"limit": {
		Type:        schemaTypeInteger,
		Description: "Maximum results to return. Defaults to 20 if omitted or zero.",
	},
}, "mode", "query")

// Search is the one query tool over codeintel.Store.Search, selecting a
// retrieval strategy with mode rather than exposing one tool per strategy:
// a small model plans better against one tool with a mode than several it
// must choose between.
type Search struct {
	store *codeintel.Store
}

// NewSearch builds a Search tool over store.
func NewSearch(store *codeintel.Store) *Search {
	return &Search{store: store}
}

// Name implements tool.Tool.
func (*Search) Name() string { return "search" }

// Description implements tool.Tool.
func (*Search) Description() string {
	return "Search the project's code index. Use mode=fuzzy for a name or text search, " +
		"mode=graph to find callers and callees of a symbol. Pick the narrowest query that " +
		"names the symbol or text you want; a query that is too broad returns noise."
}

// Schema implements tool.Tool.
func (*Search) Schema() json.RawMessage { return searchSchema }

type searchInput struct {
	Mode  string `json:"mode"`
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// Run implements tool.Tool.
func (s *Search) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("search: %w", err)
	}

	var in searchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Errorf("invalid input: %v", err), nil
	}

	if in.Query == "" {
		return tool.Errorf("query is required"), nil
	}

	results, err := s.store.Search(ctx, codeintel.SearchQuery{
		Mode:  codeintel.SearchMode(in.Mode),
		Text:  in.Query,
		Limit: in.Limit,
	})
	if err != nil {
		return tool.Errorf("%v", err), nil
	}

	return tool.Result{Content: formatSearchResults(results)}, nil
}

func formatSearchResults(results []codeintel.SearchResult) string {
	if len(results) == 0 {
		return "no results"
	}

	var b strings.Builder

	fmt.Fprintf(&b, "%d results\n", len(results))

	for _, r := range results {
		switch {
		case r.Symbol != nil:
			fmt.Fprintf(&b, "%s %s %s:%d-%d %s\n",
				r.Kind, r.Symbol.Name, r.Symbol.FilePath, r.Symbol.StartLine, r.Symbol.EndLine, r.Symbol.Signature)
		case r.Edge != nil:
			fmt.Fprintf(&b, "edge %s: %s -> %s (confidence %.2f)\n",
				r.Edge.Kind, r.Edge.Src, r.Edge.Dst, r.Edge.Confidence)
		default:
			fmt.Fprintf(&b, "%s %s\n", r.Kind, r.File)
		}
	}

	return strings.TrimSuffix(b.String(), "\n")
}
