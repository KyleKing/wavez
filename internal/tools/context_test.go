package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/codeintel/lang"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

// openTestContextIndex builds a ContextIndex over a temp tree holding
// sources, keyed by relative path, and hands back the store so a test can
// record coverage against it.
func openTestContextIndex(t *testing.T, sources map[string]string) (tools.StoreIndex, *codeintel.Store) {
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

	indexer := codeintel.NewIndexer(store, root, lang.NewDefaultRegistry())

	return tools.StoreIndex{Indexer: indexer, Store: store}, store
}

const greetSource = `package greet

// Hello greets.
func Hello() string { return "hi" }

func Goodbye() string { return "bye" }

type Greeter struct {
	Name   string
	Locale string
}
`

func checkResult(t *testing.T, result tool.Result, wantIsError bool, wantContent, wantAbsent string) {
	t.Helper()

	if result.IsError != wantIsError {
		t.Errorf("IsError = %v, want %v (content=%q)", result.IsError, wantIsError, result.Content)
	}

	if wantContent != "" && !strings.Contains(result.Content, wantContent) {
		t.Errorf("Content = %q, want it to contain %q", result.Content, wantContent)
	}

	if wantAbsent != "" && strings.Contains(result.Content, wantAbsent) {
		t.Errorf("Content = %q, want it to omit %q", result.Content, wantAbsent)
	}
}

func TestContext(t *testing.T) {
	t.Parallel()

	indexed := map[string]string{"greet.go": greetSource}

	tests := []struct {
		sources     map[string]string
		name        string
		files       string
		coveredTest string
		wantContent string
		wantAbsent  string
		tokenBudget int
		wantIsError bool
	}{
		{
			name:        "a tree with nothing to index says so rather than reporting a miss",
			sources:     nil,
			files:       "greet.go",
			wantContent: "covers no files",
		},
		{
			name:        "a file the index holds no symbol of names the file and what was indexed",
			sources:     indexed,
			files:       "missing.go",
			wantContent: "no indexed symbols cover missing.go across 1 indexed files",
		},
		{
			name:        "a whole file bundles every symbol in it",
			sources:     indexed,
			files:       "greet.go",
			wantContent: "Hello greet.go:",
		},
		{
			name:        "a line range keeps the symbols covering it and drops the rest",
			sources:     indexed,
			files:       "greet.go:4-4",
			wantContent: "Hello",
			wantAbsent:  "Goodbye",
		},
		{
			name:        "covering tests come back with the symbols",
			sources:     indexed,
			files:       "greet.go",
			coveredTest: "TestHello",
			wantContent: "tests\n  TestHello",
		},
		{
			name:        "a budget too small to fit anything says so instead of reporting a miss",
			sources:     indexed,
			files:       "greet.go",
			tokenBudget: 1,
			wantContent: "too small",
		},
		{
			name:        "a malformed line range is an error result",
			sources:     indexed,
			files:       "greet.go:ten-twenty",
			wantIsError: true,
		},
		{
			name:        "a multi-line declaration stays on one line",
			sources:     indexed,
			files:       "greet.go",
			wantContent: "type Greeter greet.go:8-11 type Greeter struct { Name string Locale string }",
		},
		{name: "empty files is an error result", sources: indexed, files: "", wantIsError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			index, store := openTestContextIndex(t, tt.sources)
			if tt.coveredTest != "" {
				rows := []codeintel.CoverageRow{{File: "greet.go", Start: 1, End: 10}}
				if err := store.WriteCoverage(context.Background(), tt.coveredTest, "hash", rows); err != nil {
					t.Fatalf("WriteCoverage: %v", err)
				}
			}

			c := tools.NewContext(index)
			result, err := c.Run(context.Background(),
				mustJSON(t, map[string]any{"files": tt.files, "token_budget": tt.tokenBudget}))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			checkResult(t, result, tt.wantIsError, tt.wantContent, tt.wantAbsent)
		})
	}
}
