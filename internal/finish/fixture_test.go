package finish_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/finish"
)

func TestFixturesAreAccountedFor(t *testing.T) {
	t.Parallel()

	patterns := []string{"*.golden", "*.raw", "**/__snapshots__/**"}

	tests := []struct {
		name     string
		answer   string
		changed  []string
		patterns []string
		want     int
	}{
		{
			name:     "a fixture rewritten in silence is reported",
			answer:   "Moved the filter input into the sidebar. All tests pass.",
			changed:  []string{"src/ui/screen.py", "tests/__snapshots__/main.raw"},
			patterns: patterns,
			want:     1,
		},
		{
			name:     "naming the fixture answers the check",
			answer:   "Regenerated tests/__snapshots__/main.raw after reading the frames.",
			changed:  []string{"src/ui/screen.py", "tests/__snapshots__/main.raw"},
			patterns: patterns,
			want:     0,
		},
		{
			name:     "the base name is enough, since that is how a sentence names it",
			answer:   "main.raw picked up the new header line.",
			changed:  []string{"tests/__snapshots__/main.raw"},
			patterns: patterns,
			want:     0,
		},
		{
			// A directory pattern has to span the depth it sits at: without
			// the leading `**` this matches only a snapshot directory in the
			// repository root, which is where no project puts one.
			name:     "a directory pattern reaches a snapshot dir at any depth",
			answer:   "Moved the input.",
			changed:  []string{"tests/ui/__snapshots__/screen.txt"},
			patterns: []string{"**/__snapshots__/**"},
			want:     1,
		},
		{
			name:     "an ordinary source file is not a fixture",
			answer:   "Rewrote the loader.",
			changed:  []string{"internal/config/loader.go"},
			patterns: patterns,
			want:     0,
		},
		{
			// A project that declares no fixtures has nothing to account for,
			// and firing on every changed file would be the wrong reading.
			name:     "no declared patterns abstains",
			answer:   "Done.",
			changed:  []string{"internal/gate/testdata/lint.golden"},
			patterns: nil,
			want:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			report := finish.FixturesAreAccountedFor(tt.answer, tt.changed, tt.patterns)
			if len(report.Findings) != tt.want {
				t.Errorf("Findings = %v, want %d", report.Findings, tt.want)
			}
		})
	}
}
