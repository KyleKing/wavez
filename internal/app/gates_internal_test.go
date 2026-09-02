package app

import (
	"testing"

	"github.com/kyleking/wavez/internal/gate"
)

// A project that declares its own formatter gets that one and not both: two
// formatters rewriting the same files in one round is the shape the worktree
// resource exists to prevent, and the built-in one speaks only Go anyway.
func TestReplaced_AProjectCheckTakesOverTheGateOfItsName(t *testing.T) {
	t.Parallel()

	builtin := []gate.Gate{gate.NewFormatGate("/repo"), gate.NewLintGate("/repo")}

	tests := map[string]struct {
		checks []gate.CommandCheck
		want   []string
	}{
		"none declared": {
			want: []string{"format", "lint"},
		},
		"a formatter of its own": {
			checks: []gate.CommandCheck{{Name: "format", Command: "ruff format .", Rewrites: true}},
			want:   []string{"lint"},
		},
		"a check of another name leaves the built-ins alone": {
			checks: []gate.CommandCheck{{Name: "types", Command: "ty check src"}},
			want:   []string{"format", "lint"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			kept := replaced(builtin, gate.NewCommandGates("/repo", tt.checks))

			got := make([]string, 0, len(kept))
			for _, g := range kept {
				got = append(got, g.Name())
			}

			if len(got) != len(tt.want) {
				t.Fatalf("kept %v, want %v", got, tt.want)
			}

			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("kept %v, want %v", got, tt.want)
				}
			}
		})
	}
}
