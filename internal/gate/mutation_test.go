package gate_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

// copyWorkspaces stands in for jj: it makes an isolated copy of the tree so
// the gate's own logic is exercised without a repository. What jj adds over
// this is cheapness and cleanup, not different semantics.
type copyWorkspaces struct{}

func (copyWorkspaces) AddWorkspace(_ context.Context, repoRoot, _, dir string) error {
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return fmt.Errorf("relativizing %s: %w", path, err)
		}

		target := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}

		data, err := os.ReadFile(path) // #nosec G304 G122 -- a test fixture tree this test owns
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		return os.WriteFile(target, data, 0o600) // #nosec G703 -- target is under the temp dir this test owns
	})
	if err != nil {
		return fmt.Errorf("copying %s to %s: %w", repoRoot, dir, err)
	}

	return nil
}

func (copyWorkspaces) ForgetWorkspace(context.Context, string, string) error { return nil }

// The gate's whole claim is that it separates a test that checks a changed
// line from one that merely runs it, which coverage cannot do. Both
// packages here have full statement coverage of the same function.
func TestMutationGateSeparatesCheckedFromMerelyCovered(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		pkg         string
		wantPass    bool
		wantSurvive bool
	}{
		{name: "a test that asserts the boundary kills every mutant", pkg: "strong", wantPass: true},
		{name: "a test that only executes the line lets them live", pkg: "weak", wantSurvive: true},
	}

	root, err := filepath.Abs(filepath.Join("testdata", "fixture", "mutmod"))
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := gate.NewMutationGate(root, copyWorkspaces{})
			result, err := g.Run(context.Background(), gate.RunContext{
				RepoRoot: root,
				Changes: []tool.Change{{
					Path:   filepath.Join(tt.pkg, "grade.go"),
					Ranges: []tool.LineRange{{Start: 4, End: 4}},
				}},
				Selection: gate.Selection{Level: gate.LevelPackage, Packages: []string{"./" + tt.pkg}},
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if result.Pass != tt.wantPass {
				t.Errorf("Pass = %v, want %v (failures %v)", result.Pass, tt.wantPass, result.Failures)
			}

			if result.Examined == 0 {
				t.Error("Examined = 0: a gate that mutated nothing has not checked anything")
			}

			if got := survivedMutants(result); got != tt.wantSurvive {
				t.Errorf("survivors = %v, want %v", got, tt.wantSurvive)
			}
		})
	}
}

// A change set with no mutable expression on its changed lines is
// unverifiable by this gate, which is not the same as verified.
func TestMutationGateAbstainsRatherThanPassing(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("testdata", "fixture", "mutmod"))
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}

	g := gate.NewMutationGate(root, copyWorkspaces{})
	result, err := g.Run(context.Background(), gate.RunContext{
		RepoRoot: root,
		Changes: []tool.Change{{
			Path:   filepath.Join("strong", "grade.go"),
			Ranges: []tool.LineRange{{Start: 1, End: 1}},
		}},
		Selection: gate.Selection{Level: gate.LevelPackage, Packages: []string{"./strong"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Pass {
		t.Error("Pass = true after mutating nothing")
	}

	if len(result.Failures) == 0 || result.Failures[0].Test != gate.ExaminedNothingTest {
		t.Errorf("Failures = %v, want %s", result.Failures, gate.ExaminedNothingTest)
	}
}

func survivedMutants(result gate.Result) bool {
	for _, f := range result.Failures {
		if strings.Contains(f.Test, "survived") {
			return true
		}
	}

	return false
}
