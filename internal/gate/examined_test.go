package gate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

// TestGoTestGate_RanNoTestsIsNotAPass covers the silent pass this whole
// Examined field exists for. `go test -run <pattern>` exits 0 when the
// pattern matches nothing, so a selection whose test names have drifted
// reports a clean gate having executed nothing. Observed in this project's
// own gate log as `"gate":"go-test","test_count":0,"pass":true`.
func TestGoTestGate_RanNoTestsIsNotAPass(t *testing.T) {
	t.Parallel()

	repoRoot := copyFixtureModule(t, "gotestjson/pass")
	goChange := []tool.Change{{Path: "pass_test.go"}}

	tests := []struct {
		name      string
		wantWhy   string
		changes   []tool.Change
		selection gate.Selection
		wantPass  bool
	}{
		{
			name:      "a -run pattern matching no test fails instead of passing",
			changes:   goChange,
			selection: gate.Selection{Level: gate.LevelLine, Tests: []string{".:TestThatDoesNotExist"}},
			wantPass:  false,
			wantWhy:   "ran 0 tests",
		},
		{
			name:      "an empty selection over changed Go files fails",
			changes:   goChange,
			selection: gate.Selection{Level: gate.LevelPackage},
			wantPass:  false,
			wantWhy:   "no tests or packages",
		},
		{
			name:      "an empty selection over no Go files is a benign abstention",
			changes:   []tool.Change{{Path: "README.md"}},
			selection: gate.Selection{Level: gate.LevelPackage},
			wantPass:  true,
		},
		{
			name:      "a selection that really runs tests passes and counts them",
			changes:   goChange,
			selection: gate.Selection{Level: gate.LevelPackage, Packages: []string{"."}},
			wantPass:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := gate.NewGoTestGate(repoRoot).Run(context.Background(), gate.RunContext{
				RepoRoot: repoRoot, Changes: tt.changes, Selection: tt.selection,
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if result.Pass != tt.wantPass {
				t.Fatalf("Pass = %v, want %v (failures %+v)", result.Pass, tt.wantPass, result.Failures)
			}

			if tt.wantPass {
				return
			}

			if len(result.Failures) != 1 || result.Failures[0].Test != gate.ExaminedNothingTest {
				t.Fatalf("Failures = %+v, want one named %q", result.Failures, gate.ExaminedNothingTest)
			}

			if got := strings.Join(result.Failures[0].Frames, " "); !strings.Contains(got, tt.wantWhy) {
				t.Errorf("frames = %q, want them to say %q", got, tt.wantWhy)
			}

			if result.Examined != 0 {
				t.Errorf("Examined = %d, want 0", result.Examined)
			}
		})
	}
}

// TestGates_ReportWhatTheyExamined keeps every gate honest about its own
// count, since an abstention is only auditable in the log if the number is
// there.
func TestGates_ReportWhatTheyExamined(t *testing.T) {
	t.Parallel()

	repoRoot := copyFixtureModule(t, "gotestjson/pass")
	rc := gate.RunContext{
		RepoRoot: repoRoot,
		Changes:  []tool.Change{{Path: "pass_test.go"}},
		Selection: gate.Selection{
			Level: gate.LevelPackage, Packages: []string{"."},
		},
	}

	gates := []gate.Gate{gate.NewFormatGate(repoRoot), gate.NewBuildGate(repoRoot), gate.NewGoTestGate(repoRoot)}
	for _, g := range gates {
		t.Run(g.Name(), func(t *testing.T) {
			t.Parallel()

			result, err := g.Run(context.Background(), rc)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if !result.Pass {
				t.Fatalf("Pass = false on a clean fixture: %+v", result.Failures)
			}

			if result.Examined == 0 {
				t.Errorf("Examined = 0 on a passing run over one changed Go file; a pass must say what it checked")
			}
		})
	}
}
