package app_test

import (
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/tool"
)

// The other bounds abstain on a nil index, coverage map, differ, and scope,
// which is what leaves the fixture bound as the one thing under test here.
func TestFinishCheckerReportsAnUnmentionedFixture(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	changes := []tool.Change{{Path: "tests/__snapshots__/main.raw"}, {Path: "src/ui/screen.py"}}

	tests := []struct {
		name    string
		answer  string
		wantHit bool
	}{
		{name: "silence about the fixture", answer: "Moved the input. Tests pass.", wantHit: true},
		{name: "the answer names it", answer: "Regenerated main.raw after reading the frames."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checker := app.NewFinishChecker(root, nil, nil, nil, nil, []string{"**/__snapshots__/**"})

			findings, err := checker.Check(t.Context(), agent.Finish{
				Task: "move the input", Answer: tt.answer, Changes: changes,
			})
			if err != nil {
				t.Fatalf("Check: %v", err)
			}

			var got bool

			for _, f := range findings {
				if strings.Contains(f, "golden fixture") {
					got = true
				}
			}

			if got != tt.wantHit {
				t.Errorf("fixture finding = %v, want %v (findings=%v)", got, tt.wantHit, findings)
			}
		})
	}
}
