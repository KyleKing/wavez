package finish_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/codeintel/lang"
	"github.com/kyleking/wavez/internal/finish"
)

// stubIndex holds the symbol names a case says the tree declares. Search
// answers for the text of the tree, which this stub does not have, so a
// name it does not declare is a name nowhere in the project.
type stubIndex map[string]bool

func (s stubIndex) DeclaresName(_ context.Context, name string) (bool, error) {
	return s[name], nil
}

func (stubIndex) Search(
	_ context.Context, _ codeintel.SearchQuery,
) ([]codeintel.SearchResult, error) {
	return nil, nil
}

// `h1` asked a run to name the file and the function that handle a
// malformed tool call. It answered with both and neither existed, and the
// model reviewer read the answer and had nothing to say.
func TestNamedThingsExistCatchesAnInventedAnswer(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "loop.go"), []byte("package agent\n"), 0o600); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	tests := []struct {
		name   string
		answer string
		want   []string
	}{
		{
			name:   "an answer naming what is there passes",
			answer: "`loop.go` handles it in `recover`.",
		},
		{
			name:   "an invented path is named",
			answer: "It is handled in `internal/agent/toolcall.go` by `recover`.",
			want:   []string{"internal/agent/toolcall.go"},
		},
		{
			name:   "an invented symbol is named, qualified or not",
			answer: "`agent.repairToolCall()` retries it.",
			want:   []string{"agent.repairToolCall"},
		},
		{
			name:   "prose naming a symbol without marking it as code is not guessed at",
			answer: "The loop calls repairToolCall and moves on.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			report, err := finish.NamedThingsExist(t.Context(), root, tt.answer, stubIndex{"recover": true})
			if err != nil {
				t.Fatalf("NamedThingsExist: %v", err)
			}

			if report.OK() != (len(tt.want) == 0) {
				t.Fatalf("OK() = %v, want %v:\n%s", report.OK(), len(tt.want) == 0, report)
			}

			for _, want := range tt.want {
				if !strings.Contains(report.String(), want) {
					t.Errorf("report = %q, want it to name %q", report, want)
				}
			}
		})
	}
}

// The index holds functions, methods, and types, so a run naming a const or
// a struct field was told it had invented the name: `maxReadFiles`,
// `ErrNoChange`, `AllowedCommands`, and `IsError` were each reported that
// way against this project while every one of them is written in it.
func TestNamedThingsExistReadsPastWhatTheIndexDeclares(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := "package p\n\nconst maxReadFiles = 10\n\nfunc Only() int { return maxReadFiles }\n"

	if err := os.WriteFile(filepath.Join(root, "p.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	store, err := codeintel.Open(t.Context(), filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("codeintel.Open: %v", err)
	}

	t.Cleanup(func() {
		if cerr := store.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	})

	if _, err = store.Index(t.Context(), root, lang.NewDefaultRegistry()); err != nil {
		t.Fatalf("Index: %v", err)
	}

	report, err := finish.NamedThingsExist(t.Context(), root,
		"`Only` reads `maxReadFiles`, and `Invented` is not there.", store)
	if err != nil {
		t.Fatalf("NamedThingsExist: %v", err)
	}

	if len(report.Findings) != 1 || report.Findings[0].Detail != "Invented" {
		t.Errorf("Findings = %v, want only the name nothing declares and nothing writes", report.Findings)
	}
}
