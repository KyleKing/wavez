package mention_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/codeintel/lang"
	"github.com/kyleking/wavez/internal/mention"
)

const goFixture = `// Package pkgone is a fixture for mention tests.
package pkgone

// Greeter says hello to someone by name.
type Greeter struct {
	Prefix string
}

// NewGreeter builds a Greeter with the given prefix.
func NewGreeter(prefix string) *Greeter {
	return &Greeter{Prefix: prefix}
}
`

const pyFixture = `"""Fixture for mention tests."""


class Greeter:
    """Says hello to someone by name."""
`

// indexedRoot builds a real store and indexer over a temp tree, so symbol
// mentions resolve through the same query path the tools use.
func indexedRoot(t *testing.T, files map[string]string) (string, *codeintel.Indexer) {
	t.Helper()

	root := t.TempDir()
	for rel, content := range files {
		writeFile(t, root, rel, content)
	}

	ctx := context.Background()
	store, err := codeintel.Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	return root, codeintel.NewIndexer(store, root, lang.NewDefaultRegistry())
}

func TestExpand_SymbolMention(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"pkgone/greeter.go": goFixture,
		"pysrc/greeter.py":  pyFixture,
	}

	tests := []struct {
		name     string
		prompt   string
		wantKind mention.Kind
		absent   string
		want     []string
	}{
		{
			name:     "exact name carries location and signature",
			prompt:   "explain @NewGreeter",
			wantKind: mention.KindSymbol,
			want: []string{
				"@NewGreeter (symbol):",
				"func NewGreeter pkgone/greeter.go:10-12",
				"func NewGreeter(prefix string) *Greeter",
				"doc: NewGreeter builds a Greeter with the given prefix.",
			},
			absent: "return &Greeter{Prefix: prefix}",
		},
		{
			name:     "ambiguity lists candidates and chooses none",
			prompt:   "explain @Greeter",
			wantKind: mention.KindSymbol,
			want: []string{
				"@Greeter (symbol, 2 matches, none chosen",
				"pkgone/greeter.go:5-7",
				"pysrc/greeter.py",
			},
		},
		{
			name:     "a qualifier disambiguates",
			prompt:   "explain @pkgone.Greeter",
			wantKind: mention.KindSymbol,
			want:     []string{"@pkgone.Greeter (symbol):", "type Greeter pkgone/greeter.go:5-7"},
			absent:   "pysrc/greeter.py",
		},
		{
			name:     "a miss names the reference and the index size",
			prompt:   "explain @Nonexistent",
			wantKind: mention.KindUnresolved,
			want:     []string{"no indexed symbol named Nonexistent across 2 indexed files", "left as literal text"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root, index := indexedRoot(t, files)
			result, err := mention.New(root, index).Expand(context.Background(), tc.prompt)
			if err != nil {
				t.Fatalf("Expand: %v", err)
			}

			if len(result.Mentions) != 1 || result.Mentions[0].Kind != tc.wantKind {
				t.Fatalf("mentions = %+v", result.Mentions)
			}
			for _, want := range tc.want {
				if !strings.Contains(result.Prompt, want) {
					t.Errorf("prompt is missing %q:\n%s", want, result.Prompt)
				}
			}
			if tc.absent != "" && strings.Contains(result.Prompt, tc.absent) {
				t.Errorf("prompt should not contain %q:\n%s", tc.absent, result.Prompt)
			}
		})
	}
}

// TestExpand_SymbolOnEmptyIndex holds the distinction DESIGN.md asks for: a
// reference that missed and a tree the index covers nothing of are different
// kinds of empty and must not report the same way.
func TestExpand_SymbolOnEmptyIndex(t *testing.T) {
	t.Parallel()

	root, index := indexedRoot(t, map[string]string{"notes.txt": "no code here\n"})
	result, err := mention.New(root, index).Expand(context.Background(), "explain @Greeter")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	if len(result.Unresolved()) != 1 {
		t.Fatalf("mentions = %+v", result.Mentions)
	}
	if !strings.Contains(result.Prompt, "the code index covers no files in this project") {
		t.Errorf("an unindexed tree must say so:\n%s", result.Prompt)
	}
}

// TestExpand_FileWinsOverSymbol pins the resolution order: a reference that
// names a real file never turns into a symbol guess.
func TestExpand_FileWinsOverSymbol(t *testing.T) {
	t.Parallel()

	root, index := indexedRoot(t, map[string]string{"pkgone/greeter.go": goFixture})
	result, err := mention.New(root, index).Expand(context.Background(), "read @pkgone/greeter.go")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}

	if len(result.Mentions) != 1 || result.Mentions[0].Kind != mention.KindFile {
		t.Fatalf("mentions = %+v", result.Mentions)
	}
}
