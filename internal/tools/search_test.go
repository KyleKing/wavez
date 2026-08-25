package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/codeintel/lang"
	"github.com/kyleking/wavez/internal/tools"
)

// openTestIndex builds an Indexer over a temp tree holding sources, keyed
// by relative path. An empty map gives a tree the index covers no files of,
// which is a distinct outcome from a query that misses.
func openTestIndex(t *testing.T, sources map[string]string) *codeintel.Indexer {
	t.Helper()

	root := t.TempDir()
	for rel, src := range sources {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	store, err := codeintel.Open(context.Background(), filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("codeintel.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	return codeintel.NewIndexer(store, root, lang.NewDefaultRegistry())
}

func TestSearch(t *testing.T) {
	t.Parallel()

	indexed := map[string]string{"greet.go": "package greet\n\nfunc Hello() string { return \"hi\" }\n"}

	tests := []struct {
		sources     map[string]string
		name        string
		mode        string
		query       string
		wantContent string
		limit       int
		wantIsError bool
	}{
		{
			name:        "a tree with nothing to index says so rather than reporting a miss",
			sources:     nil,
			mode:        "fuzzy",
			query:       "Hello",
			wantContent: "covers no files",
		},
		{
			name:        "a genuine miss names the query and what was searched",
			sources:     indexed,
			mode:        "fuzzy",
			query:       "Goodbye",
			wantContent: `no matches for "Goodbye" across 1 indexed files`,
		},
		{
			name:        "a hit comes back without any indexing having been asked for",
			sources:     indexed,
			mode:        "fuzzy",
			query:       "Hello",
			wantContent: "greet.go",
		},
		{
			name:        "a file hit says which line matched, so the caller need not grep for it",
			sources:     indexed,
			mode:        "fuzzy",
			query:       "Hello",
			wantContent: "  3: func Hello() string { return \"hi\" }",
		},
		{
			name: "a capped result set says how many it did not show",
			sources: map[string]string{
				"a.go": "package a\n\nfunc Hello() {}\n",
				"b.go": "package b\n\nfunc Hello() {}\n",
				"c.go": "package c\n\nfunc Hello() {}\n",
			},
			mode:        "fuzzy",
			query:       "Hello",
			limit:       1,
			wantContent: "of 6 that matched",
		},
		{name: "unimplemented mode is an error result", mode: "semantic", query: "anything", wantIsError: true},
		{name: "empty query is an error result", mode: "fuzzy", query: "", wantIsError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := tools.NewSearch(openTestIndex(t, tt.sources))
			input := mustJSON(t, map[string]any{"mode": tt.mode, "query": tt.query, "limit": tt.limit})

			result, err := s.Run(context.Background(), input)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if result.IsError != tt.wantIsError {
				t.Errorf("IsError = %v, want %v (content=%q)", result.IsError, tt.wantIsError, result.Content)
			}

			if tt.wantContent != "" && !strings.Contains(result.Content, tt.wantContent) {
				t.Errorf("Content = %q, want it to contain %q", result.Content, tt.wantContent)
			}
		})
	}
}

// A caller who knows where to look was reaching for `grep -n pattern one/file.go`
// because this tool answered for the whole project: 106 of 278 shell calls in
// the thread logs were a search the shell could scope and this could not.
func TestSearchNarrowsToAPath(t *testing.T) {
	t.Parallel()

	indexer := openTestIndex(t, map[string]string{
		"a/keep.go": "package a\n\nfunc Target() string { return \"a\" }\n",
		"b/drop.go": "package b\n\nfunc Target() string { return \"b\" }\n",
	})
	search := tools.NewSearch(indexer)

	whole, err := search.Run(t.Context(), []byte(`{"mode":"fuzzy","query":"Target"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(whole.Content, "a/keep.go") || !strings.Contains(whole.Content, "b/drop.go") {
		t.Fatalf("an unscoped search should find both:\n%s", whole.Content)
	}

	scoped, err := search.Run(t.Context(), []byte(`{"mode":"fuzzy","query":"Target","path":"a"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(scoped.Content, "a/keep.go") {
		t.Errorf("the scoped search lost the match inside the path:\n%s", scoped.Content)
	}

	if strings.Contains(scoped.Content, "b/drop.go") {
		t.Errorf("the scoped search kept a match outside the path:\n%s", scoped.Content)
	}
}

// Alternation already worked and the schema never said so, which is why the
// model wrote `grep "A\|B"` instead: six of fourteen sampled grep calls were
// alternations.
func TestSearchFindsEitherOfTwoNames(t *testing.T) {
	t.Parallel()

	indexer := openTestIndex(t, map[string]string{
		"a/alpha.go": "package a\n\nfunc Alpha() string { return \"a\" }\n",
		"b/beta.go":  "package b\n\nfunc Beta() string { return \"b\" }\n",
	})

	res, err := tools.NewSearch(indexer).Run(t.Context(), []byte(`{"mode":"fuzzy","query":"Alpha OR Beta"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, want := range []string{"a/alpha.go", "b/beta.go"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("OR did not find %s:\n%s", want, res.Content)
		}
	}
}

// The trigram index answers `edit.ApplyToFile` and `ApplyToFile` alike,
// because the fuzzy path splits its query on non-word characters and ORs the
// halves. Literal mode is what a caller reaches for when the characters
// themselves are the question, and it was the last of the three things the
// sampled grep calls wanted that this tool could not do.
func TestSearchLiteralHoldsPunctuationAndCase(t *testing.T) {
	t.Parallel()

	indexer := openTestIndex(t, map[string]string{
		"a/call.go":  "package a\n\nfunc run() { edit.ApplyToFile(p) }\n",
		"b/other.go": "package b\n\nfunc ApplyToFile(p string) {}\n",
		"c/case.go":  "package c\n\nfunc applytofile(p string) {}\n",
	})
	search := tools.NewSearch(indexer)

	cases := []struct {
		name  string
		input string
		want  []string
		deny  []string
	}{
		{
			name:  "fuzzy splits on the dot",
			input: `{"mode":"fuzzy","query":"edit.ApplyToFile"}`,
			want:  []string{"a/call.go", "b/other.go"},
		},
		{
			name:  "literal keeps the dot",
			input: `{"mode":"literal","query":"edit.ApplyToFile"}`,
			want:  []string{"a/call.go"},
			deny:  []string{"b/other.go"},
		},
		{
			name:  "literal keeps the case",
			input: `{"mode":"literal","query":"ApplyToFile("}`,
			want:  []string{"a/call.go", "b/other.go"},
			deny:  []string{"c/case.go"},
		},
		{
			name:  "too short to answer",
			input: `{"mode":"literal","query":"ap"}`,
			want:  []string{"too short"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := search.Run(t.Context(), []byte(tc.input))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			for _, w := range tc.want {
				if !strings.Contains(got.Content, w) {
					t.Errorf("missing %q:\n%s", w, got.Content)
				}
			}

			for _, d := range tc.deny {
				if strings.Contains(got.Content, d) {
					t.Errorf("should not have matched %q:\n%s", d, got.Content)
				}
			}
		})
	}
}

// The model reaches for literal mode and then writes a description into it.
// Across the h6 lanes 6 of 9 searches did exactly that and got nothing back
// while the symbol sat in the index, and a run that cannot retrieve spends
// the rest of itself guessing.
func TestSearchRetriesAWordyLiteralAsFuzzy(t *testing.T) {
	t.Parallel()

	search := tools.NewSearch(openTestIndex(t, map[string]string{
		"thread/thread.go": "package thread\n\nfunc truncate(s string, limit int) string { return s }\n",
	}))

	result, err := search.Run(context.Background(),
		json.RawMessage(`{"mode":"literal","query":"truncate function in thread/thread.go"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.IsError {
		t.Fatalf("IsError = true: %q", result.Content)
	}

	if !strings.Contains(result.Content, "thread/thread.go") {
		t.Errorf("Content = %q, want the fuzzy retry to find the symbol", result.Content)
	}

	if !strings.Contains(result.Content, "no literal match") {
		t.Errorf("Content = %q, want it to say the literal search was retried", result.Content)
	}

	// A literal query naming one identifier is answered as asked, with no
	// retry to explain.
	exact, err := search.Run(context.Background(),
		json.RawMessage(`{"mode":"literal","query":"truncate"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.Contains(exact.Content, "no literal match") {
		t.Errorf("Content = %q, want a single-word literal answered directly", exact.Content)
	}
}

// A name the index does not hold under that spelling is the same problem as
// an edit anchor that misses, and the same answer applies: name what is
// there. One run searched `maxLines`, was told only that nothing matched,
// and answered the task from unrelated constants.
func TestSearchNamesTheClosestNamesOnALiteralMiss(t *testing.T) {
	t.Parallel()

	search := tools.NewSearch(openTestIndex(t, map[string]string{
		"tools/read.go": "package tools\n\nconst maxReadLines = 400\n",
	}))

	result, err := search.Run(context.Background(),
		json.RawMessage(`{"mode":"literal","query":"maxLines"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(result.Content, "closest names the index holds") {
		t.Errorf("Content = %q, want the miss to offer what is there", result.Content)
	}
	if !strings.Contains(result.Content, "maxReadLines") {
		t.Errorf("Content = %q, want the name the index actually holds", result.Content)
	}
}
