package finish_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/finish"
)

// stubIndex holds the symbol names a case says the tree has.
type stubIndex map[string]bool

func (s stubIndex) Search(
	_ context.Context, q codeintel.SearchQuery,
) ([]codeintel.SearchResult, codeintel.IndexStats, error) {
	if !s[q.Text] {
		return nil, codeintel.IndexStats{}, nil
	}

	return []codeintel.SearchResult{{Symbol: &codeintel.Symbol{Name: q.Text}}}, codeintel.IndexStats{}, nil
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
