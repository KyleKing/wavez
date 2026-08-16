package stakes_test

import (
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/guard"
	"github.com/kyleking/wavez/internal/stakes"
)

func TestCompute_CapabilityDelta(t *testing.T) {
	t.Parallel()

	tests := []struct {
		before string
		after  string
		want   stakes.Capability
		name   string
	}{
		{
			name:   "subprocess",
			before: `func run() { fmt.Println("hi") }`,
			after:  `func run() { exec.Command("rm", "-rf", "/").Run() }`,
			want:   stakes.CapabilitySubprocess,
		},
		{
			name:   "network",
			before: `func doRequest() { return nil }`,
			after:  `func doRequest() { resp, _ := http.Get(url) }`,
			want:   stakes.CapabilityNetwork,
		},
		{
			name:   "sql",
			before: `func q() { return nil }`,
			after:  `func q() { db.Exec("SELECT * FROM users WHERE id = " + id) }`,
			want:   stakes.CapabilitySQL,
		},
		{
			name:   "auth",
			before: `func handler() { serve() }`,
			after:  `func handler() { if !is_authorized(user) { return } }`,
			want:   stakes.CapabilityAuth,
		},
		{
			name: "import",
			before: `import (
	"fmt"
)`,
			after: `import (
	"fmt"
	"github.com/some/new-dep"
)`,
			want: stakes.CapabilityImport,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			edit := stakes.Edit{Path: "f.go", Before: tt.before, After: tt.after}
			got := stakes.Compute(stakes.Input{Edits: []stakes.Edit{edit}})

			if !containsCapability(got.Capabilities, tt.want) {
				t.Fatalf("Compute() capabilities = %v, want to contain %s", got.Capabilities, tt.want)
			}

			if got.Band != stakes.BandHigh {
				t.Fatalf("Compute() band = %s, want %s for a new %s capability", got.Band, stakes.BandHigh, tt.want)
			}
		})
	}
}

func TestCompute_CapabilityAlreadyPresentIsNotFlagged(t *testing.T) {
	t.Parallel()

	before := `func run() { exec.Command("ls").Run() }`
	after := `func run() { exec.Command("ls", "-la").Run() }`

	got := stakes.Compute(stakes.Input{Edits: []stakes.Edit{{Path: "f.go", Before: before, After: after}}})

	if containsCapability(got.Capabilities, stakes.CapabilitySubprocess) {
		t.Fatalf("Compute() flagged subprocess as new, but it was already present in before content")
	}
}

func TestCompute_FormattingOnlyChangeIsLowStakes(t *testing.T) {
	t.Parallel()

	before := "func run() {\n\tfmt.Println(\"hi\")\n}\n"
	after := "func run() {\n\t// says hello\n\tfmt.Println(\"hi\")\n}\n"

	got := stakes.Compute(stakes.Input{Edits: []stakes.Edit{{Path: "f.go", Before: before, After: after}}})

	if got.Band != stakes.BandLow {
		t.Fatalf("Compute() band = %s, want %s for a comment-only change", got.Band, stakes.BandLow)
	}

	if len(got.Capabilities) != 0 {
		t.Fatalf("Compute() capabilities = %v, want none for a comment-only change", got.Capabilities)
	}
}

func TestCompute_GuardVerdictRaisesBand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		verdict guard.Verdict
		want    stakes.Band
	}{
		{verdict: guard.Allow, want: stakes.BandLow},
		{verdict: guard.NeedsApproval, want: stakes.BandModerate},
		{verdict: guard.Refuse, want: stakes.BandHigh},
	}

	for _, tt := range tests {
		t.Run(string(tt.verdict), func(t *testing.T) {
			t.Parallel()

			v := tt.verdict
			got := stakes.Compute(stakes.Input{Guard: &v})

			if got.Band != tt.want {
				t.Fatalf("Compute() band = %s, want %s for guard verdict %s", got.Band, tt.want, tt.verdict)
			}
		})
	}
}

func TestCompute_PathOutsideRootIsIrreversible(t *testing.T) {
	t.Parallel()

	got := stakes.Compute(stakes.Input{ProjectRoot: "/home/user/project", Paths: []string{"../../etc/passwd"}})

	if got.Reversibility != stakes.Irreversible {
		t.Fatalf("Compute() reversibility = %s, want %s", got.Reversibility, stakes.Irreversible)
	}

	if got.Band != stakes.BandHigh {
		t.Fatalf("Compute() band = %s, want %s for an irreversible path", got.Band, stakes.BandHigh)
	}
}

func TestCompute_PathInsideRootIsReversible(t *testing.T) {
	t.Parallel()

	got := stakes.Compute(stakes.Input{ProjectRoot: "/home/user/project", Paths: []string{"internal/stakes/score.go"}})

	if got.Reversibility != stakes.Reversible {
		t.Fatalf("Compute() reversibility = %s, want %s", got.Reversibility, stakes.Reversible)
	}
}

func TestCompute_UncomputableSignalsReportUnknownNotSafe(t *testing.T) {
	t.Parallel()

	got := stakes.Compute(stakes.Input{})

	if got.CapsChecked {
		t.Fatalf("Compute() with no edits reported CapsChecked = true, want false")
	}

	if got.Reversibility != stakes.ReversibilityUnknown {
		t.Fatalf("Compute() reversibility = %s, want %s with no paths", got.Reversibility, stakes.ReversibilityUnknown)
	}

	if got.BlastKnown {
		t.Fatalf("Compute() BlastKnown = true, want false: blast radius has no writer yet")
	}

	render := got.Render()
	for _, want := range []string{"caps:unknown", "revert:unknown", "blast:unknown"} {
		if !strings.Contains(render, want) {
			t.Fatalf("Render() = %q, want it to contain %q rather than a safe-looking default", render, want)
		}
	}
}

func TestScore_RenderFitsTerminalWidth(t *testing.T) {
	t.Parallel()

	refuse := guard.Refuse
	worst := stakes.Compute(stakes.Input{
		ProjectRoot: "/home/user/project",
		Paths:       []string{"/etc/passwd"},
		Guard:       &refuse,
		Edits: []stakes.Edit{{
			Path:   "f.go",
			Before: `func f() {}`,
			After: `func f() {
				exec.Command("x").Run()
				http.Get(url)
				db.Exec("SELECT * FROM x")
				is_authorized(u)
				import "github.com/new/dep"
			}`,
		}},
	})

	const maxTerminalWidth = 100

	render := worst.Render()
	if len(render) > maxTerminalWidth {
		t.Fatalf("Render() = %q (%d chars), want at most %d for a terminal row", render, len(render), maxTerminalWidth)
	}

	if worst.Band != stakes.BandHigh {
		t.Fatalf("Compute() band = %s, want %s for the worst case", worst.Band, stakes.BandHigh)
	}
}

func containsCapability(caps []stakes.Capability, want stakes.Capability) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}

	return false
}
