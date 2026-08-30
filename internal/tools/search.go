package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/codeintel/lang"
	"github.com/kyleking/wavez/internal/tool"
)

var searchSchema = buildSchema(map[string]schemaProperty{
	"mode": {
		Type: schemaTypeString,
		// semantic and hybrid exist in the store and return an error until
		// v0.2, so they are deliberately absent here: advertising a mode
		// costs every request tokens to say it does not work.
		Enum: []string{"fuzzy", "literal", "graph"},
		Description: "Retrieval strategy. fuzzy matches symbol names, paths, and file text. " +
			"literal matches an exact substring, case-sensitively. graph walks " +
			"call/reference edges one hop from query.",
	},
	"query": {
		Type: schemaTypeString,
		Description: "For fuzzy mode, a search string. Combine several names with OR to " +
			"find any of them in one call (\"ChoiceFast OR ChoiceDeep\"). For literal mode, " +
			"the exact text to find, punctuation and case included (\"edit.ApplyToFile\"), " +
			"at least 3 characters. For graph mode, a symbol key to walk edges from.",
	},
	propPath: {
		Type: schemaTypeString,
		Description: "Narrow results to this file or directory, relative to the project " +
			"root. Omit it to search the whole project.",
	},
	"limit": {
		Type:        schemaTypeInteger,
		Description: "Maximum results to return. Defaults to 20 if omitted or zero.",
	},
}, "mode", "query")

// worthRetryingAsFuzzy reports a literal query that reads as a description
// rather than as text in a file. A phrase with whitespace can occur
// literally, so a hit is answered as asked and only an empty result is
// retried. Measured across the `h6` lanes, 6 of 9 searches asked for
// literal text like "truncate function in internal/thread/thread.go" and
// got nothing, while every literal search naming one identifier landed.
func worthRetryingAsFuzzy(in searchInput) bool {
	return codeintel.SearchMode(in.Mode) == codeintel.SearchLiteral
}

// Index is the code index Search queries. Search refreshes as a side effect
// of querying, so a caller can never read an index that has drifted from
// the tree.
type Index interface {
	Search(ctx context.Context, q codeintel.SearchQuery) ([]codeintel.SearchResult, codeintel.IndexStats, error)
}

// Search is the one query tool over the code index, selecting a retrieval
// strategy with mode rather than exposing one tool per strategy: a small
// model plans better against one tool with a mode than several it must
// choose between.
type Search struct {
	index Index
}

// NewSearch builds a Search tool over index.
func NewSearch(index Index) *Search {
	return &Search{index: index}
}

// Name implements tool.Tool.
func (*Search) Name() string { return "search" }

// Description implements tool.Tool.
func (*Search) Description() string {
	return "Search the project's code index. Use mode=fuzzy for a name or text search, " +
		"mode=literal when the exact characters matter, mode=graph to find callers and " +
		"callees of a symbol. Pick the narrowest query that names the symbol or text you " +
		"want; a query that is too broad returns noise."
}

// Schema implements tool.Tool.
func (*Search) Schema() json.RawMessage { return searchSchema }

type searchInput struct {
	Mode  string `json:"mode"`
	Query string `json:"query"`
	Path  string `json:"path"`
	Limit int    `json:"limit"`
}

// scopeTo narrows results to a file or directory. The index answers for the
// whole project, and a caller who already knows where to look was otherwise
// reaching for `grep -n pattern one/file.go`: measured over the thread logs,
// 106 of 278 shell calls were a search the shell could scope and this tool
// could not.
func scopeTo(results []codeintel.SearchResult, path string) []codeintel.SearchResult {
	if path == "" {
		return results
	}

	out := make([]codeintel.SearchResult, 0, len(results))

	for i := range results {
		if under(fileOf(results[i]), path) {
			out = append(out, results[i])
		}
	}

	return out
}

// fileOf is the path a result belongs to, whichever shape it has.
func fileOf(r codeintel.SearchResult) string {
	if r.Symbol != nil {
		return r.Symbol.FilePath
	}

	return r.File
}

// Run implements tool.Tool.
func (s *Search) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("search: %w", err)
	}

	var in searchInput
	if err := decodeInput(input, &in); err != nil {
		return tool.Fail(tool.CauseMalformed, "invalid input: %v", err), nil
	}

	if in.Query == "" {
		return tool.Fail(tool.CauseBadInput, "query is required"), nil
	}

	// An absent mode reads as fuzzy rather than as an error. The index
	// rejects the empty string by name ("unknown search mode"), which reads
	// as a bad value rather than a missing field, and fuzzy is both the mode
	// the description leads with and the one that cannot do harm.
	if in.Mode == "" {
		in.Mode = string(codeintel.SearchFuzzy)
	}

	results, stats, err := s.index.Search(ctx, codeintel.SearchQuery{
		Mode:  codeintel.SearchMode(in.Mode),
		Text:  in.Query,
		Limit: in.Limit,
	})
	if err != nil {
		return tool.Fail(tool.CauseUpstream, "%v", err), nil
	}

	scoped := scopeTo(results, in.Path)
	if len(scoped) > 0 || !worthRetryingAsFuzzy(in) {
		return tool.Result{Content: formatSearchResults(scoped, stats, in.Query)}, nil
	}

	retried, stats, err := s.index.Search(ctx, codeintel.SearchQuery{
		Mode:  codeintel.SearchFuzzy,
		Text:  fuzzyRetryText(in.Query),
		Limit: in.Limit,
	})
	if err != nil {
		return tool.Fail(tool.CauseUpstream, "%v", err), nil
	}

	scoped = scopeTo(retried, in.Path)
	if len(scoped) == 0 {
		return tool.Result{Content: formatSearchResults(scoped, stats, in.Query)}, nil
	}

	return tool.Result{Content: fmt.Sprintf("%s:\n%s",
		literalMissReason(in.Query), formatSearchResults(scoped, stats, in.Query))}, nil
}

// fuzzyRetryText is what a literal miss searches for instead. The index
// tokenizes on trigrams, so a one-identifier query matches an exact
// substring in fuzzy mode too and the retry finds the same nothing.
// Splitting it on case and underscores lets the parts match a name spelled
// with more of them: `maxLines` reaches `maxReadLines`. Parts shorter than
// a trigram are dropped, since the tokenizer indexes no term below three
// characters and one would match every row.
func fuzzyRetryText(query string) string {
	if strings.ContainsFunc(query, unicode.IsSpace) {
		return query
	}

	notWord := func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

	var parts []string

	for _, word := range strings.FieldsFunc(query, notWord) {
		parts = append(parts, splitCamel(word)...)
	}

	kept := parts[:0]

	for _, p := range parts {
		if len(p) >= minTrigram {
			kept = append(kept, p)
		}
	}

	// One part is the query again, so the retry would find the same nothing.
	if len(kept) < minSplitParts {
		return query
	}

	return strings.Join(kept, " ")
}

// minTrigram is the shortest term the index's trigram tokenizer holds.
const minTrigram = 3

// minSplitParts is how many parts make a split worth searching.
const minSplitParts = 2

func splitCamel(word string) []string {
	var (
		out     []string
		current []rune
	)

	for _, r := range word {
		if unicode.IsUpper(r) && len(current) > 0 {
			out = append(out, string(current))
			current = current[:0]
		}

		current = append(current, r)
	}

	if len(current) > 0 {
		out = append(out, string(current))
	}

	return out
}

// literalMissReason says why a literal query found nothing and what the
// fuzzy results below it are. A single token that misses is a name the
// index does not hold under that spelling, and naming the ones it does
// hold is the same argument as reporting a near match on a failed edit
// anchor: one run searched `maxLines`, was told only that nothing matched,
// and answered from unrelated constants rather than from `maxReadLines`.
func literalMissReason(query string) string {
	if strings.ContainsFunc(query, unicode.IsSpace) {
		return fmt.Sprintf("no literal match for %q, which is several words and literal matches "+
			"an exact substring. Searched fuzzy instead", query)
	}

	return fmt.Sprintf("no literal match for %q. The closest names the index holds", query)
}

// indexedExtensions names what the index can hold, for an absence that would
// otherwise read as an absence from the tree. A run investigating a web
// project was told "no matches for search-highlight across 149 indexed files"
// for a rule sitting in main.css, concluded the CSS did not style the class,
// and proposed correcting the project's own notes to say so.
var indexedExtensions = strings.Join(lang.NewDefaultRegistry().Indexed(), ", ")

const bytesPerKB = 1024

// partialNote names what the index does not cover yet or will never cover,
// so a miss either one caused reads differently from a symbol that does not
// exist.
func partialNote(stats codeintel.IndexStats) string {
	note := ""
	if stats.FilesDeferred > 0 {
		note += fmt.Sprintf(" %d further file(s) have not been read into the index yet; retry in a moment.",
			stats.FilesDeferred)
	}

	if stats.FilesTooLarge > 0 {
		note += fmt.Sprintf(" %d further file(s) are over %d kB and are not indexed at all; rg reaches those.",
			stats.FilesTooLarge, codeintel.MaxFileBytes/bytesPerKB)
	}

	return note
}

// formatSearchResults distinguishes an empty result from an index that
// covers nothing. Reporting both as "no results" told a model to narrow a
// query that could not have matched anything, and it spent four turns
// retrying.
func formatSearchResults(results []codeintel.SearchResult, stats codeintel.IndexStats, query string) string {
	if len(results) == 0 {
		if stats.Building != "" {
			return "no matches: " + stats.Building
		}

		if stats.EdgesUnavailable != "" {
			return fmt.Sprintf("no matches: the call graph is unavailable (%s), so graph mode "+
				"cannot answer here. Use mode=fuzzy, or shell with rg, instead", stats.EdgesUnavailable)
		}

		if stats.FilesScanned == 0 {
			return "no matches: the code index covers no files in this project, " +
				"so search cannot answer here. Use shell with rg, or read, instead"
		}

		return fmt.Sprintf("no matches for %q across %d indexed files, which are this project's "+
			"%s files. No other file type is indexed, so a match in one is invisible here: "+
			"use shell with rg to search those.%s",
			query, stats.FilesScanned, indexedExtensions, partialNote(stats))
	}

	var b strings.Builder

	if stats.MatchesTotal > len(results) {
		fmt.Fprintf(&b, "%d results, of %d that matched; raise limit or narrow the query to see the rest\n",
			len(results), stats.MatchesTotal)
	} else {
		fmt.Fprintf(&b, "%d results, which is all of them\n", len(results))
	}

	for i := range results {
		r := &results[i]
		switch {
		case r.Symbol != nil:
			fmt.Fprintf(&b, "%s %s %s:%d-%d %s\n",
				r.Kind, r.Symbol.Name, r.Symbol.FilePath, r.Symbol.StartLine, r.Symbol.EndLine, r.Symbol.Signature)
		case r.Edge != nil:
			fmt.Fprintf(&b, "edge %s: %s -> %s (confidence %.2f)\n",
				r.Edge.Kind, r.Edge.Src, r.Edge.Dst, r.Edge.Confidence)
		default:
			fmt.Fprintf(&b, "%s %s\n", r.Kind, r.File)
			for _, l := range r.Lines {
				fmt.Fprintf(&b, "  %d: %s\n", l.Line, l.Text)
			}
		}
	}

	return strings.TrimSuffix(b.String(), "\n")
}
