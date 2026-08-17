package app_test

import (
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/gate"
)

// A passing gate says nothing and a failing one always says something. The
// second half is the one that bit: a build failure whose frames did not
// survive trimming reached the model as a bare gate name, and it spent 26
// turns guessing what had failed.
func TestChangeGateFeedback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		results  []gate.Result
		want     []string
		wantNone bool
	}{
		{
			name:     "every gate passed",
			results:  []gate.Result{{Gate: "go-test", Pass: true, Examined: 3}},
			wantNone: true,
		},
		{
			name: "a named test failure carries its frames",
			results: []gate.Result{{Gate: "go-test", Failures: []gate.TrimmedFailure{{
				Test: "TestTTL", Package: "lease", Frames: []string{"lease.go:12: want 30s"},
			}}}},
			want: []string{"go-test TestTTL", "lease.go:12: want 30s"},
		},
		{
			name: "a build failure has no test name and is named by package",
			results: []gate.Result{{Gate: "go-test", Failures: []gate.TrimmedFailure{{
				Package: "tmp/calc", Frames: []string{"calc.go:5:9: cannot use \"sum\""},
			}}}},
			want: []string{"go-test build tmp/calc", "cannot use"},
		},
		{
			name:    "a failure the gate could not describe still names the gate",
			results: []gate.Result{{Gate: "go-test", Failures: []gate.TrimmedFailure{{}}}},
			want:    []string{"go-test failed without reporting which check"},
		},
		{
			name:    "a failing gate with no failures at all still says so",
			results: []gate.Result{{Gate: "lsp"}},
			want:    []string{"lsp failed without reporting which check"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := app.NewChangeGate(nil)
			g.Collect(gate.RunResult{Gates: tt.results})

			got := g.TakeFeedback()

			if tt.wantNone {
				if got != "" {
					t.Errorf("TakeFeedback() = %q, want empty; a passing gate spends no tokens", got)
				}

				return
			}

			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("TakeFeedback() = %q, want it to contain %q", got, want)
				}
			}

			if g.TakeFeedback() != "" {
				t.Error("TakeFeedback() did not clear; the same failure would repeat every turn")
			}
		})
	}
}
