package gate_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/astgrep"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

// nolintRule mirrors rules/no-new-nolint.yml, the rule this project gates
// its own agent on, so the gate is exercised against a real ast-grep rule
// rather than a fixture written to pass.
const nolintRule = `id: no-bare-nolint
language: go
message: fix the cause instead of silencing the check, or give the nolint a reason
severity: error
files:
  - internal/**
rule:
  kind: comment
  regex: '^//nolint:[a-zA-Z0-9_,-]+\s*$'
`

// sourcePath sits under internal/ so the rule's `files:` scope applies.
const sourcePath = "internal/sub.go"

func conventionFixture(t *testing.T, source string) (string, []astgrep.RuleFile) {
	t.Helper()

	root := t.TempDir()

	rulePath := filepath.Join(root, "no-bare-nolint.yml")
	if err := os.WriteFile(rulePath, []byte(nolintRule), 0o600); err != nil {
		t.Fatalf("writing rule: %v", err)
	}

	srcPath := filepath.Join(root, filepath.FromSlash(sourcePath))
	if err := os.MkdirAll(filepath.Dir(srcPath), 0o750); err != nil {
		t.Fatalf("creating source dir: %v", err)
	}

	if err := os.WriteFile(srcPath, []byte(source), 0o600); err != nil {
		t.Fatalf("writing source: %v", err)
	}

	rf, err := astgrep.LoadRuleFile(rulePath)
	if err != nil {
		t.Fatalf("LoadRuleFile: %v", err)
	}

	return root, []astgrep.RuleFile{rf}
}

func requireAstGrep(t *testing.T) {
	t.Helper()

	if !astgrep.NewRunner().Resolve().Available {
		t.Skip(astgrep.InstallHint)
	}
}

func TestConventionGate_NoRulesMeansNoGate(t *testing.T) {
	t.Parallel()

	if g := gate.NewConventionGate(t.TempDir(), nil, nil); g != nil {
		t.Errorf("NewConventionGate with no rules = %v, want nil so it is never scheduled", g)
	}
}

func TestConventionGate_UnavailableBinaryFailsRatherThanPasses(t *testing.T) {
	t.Parallel()

	root, rules := conventionFixture(t, "package p\n")
	runner := astgrep.NewRunner(astgrep.WithLookPath(func(string) (string, error) {
		return "", os.ErrNotExist
	}))

	g := gate.NewConventionGate(root, rules, runner)

	_, err := g.Run(context.Background(), gate.RunContext{
		RepoRoot: root, Changes: []tool.Change{{Path: sourcePath}},
	})
	if err == nil {
		t.Fatal("Run returned nil error with ast-grep absent; a check that cannot run is not a pass")
	}
}

// TestConventionGate_PassesRepoRelativeTargets pins the argument shape
// rather than the match, because the failure it guards is silent. Measured
// on this repo: `ast-grep scan --rule rules/no-fmt-println.yml` (scoped
// `files: internal/**`) reports the violation for `internal/…/x.go` and
// reports nothing for the same file named absolutely, and dropping the
// `files:` scope makes both forms report it. So an absolute target skips
// every scoped rule and the gate returns a pass it never earned.
func TestConventionGate_PassesRepoRelativeTargets(t *testing.T) {
	t.Parallel()

	root, rules := conventionFixture(t, "package p\n")

	var got []string

	runner := astgrep.NewRunner(
		astgrep.WithLookPath(func(string) (string, error) { return "ast-grep", nil }),
		astgrep.WithCommand(func(_ context.Context, _ string, args []string, _ string) ([]byte, error) {
			got = args

			return []byte("[]"), nil
		}),
	)

	_, err := gate.NewConventionGate(root, rules, runner).Run(context.Background(), gate.RunContext{
		RepoRoot: root,
		Changes:  []tool.Change{{Path: sourcePath}, {Path: filepath.Join(root, "internal", "other.go")}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, arg := range got {
		if filepath.IsAbs(arg) && filepath.Ext(arg) == ".go" {
			t.Errorf("absolute target %q: scoped rules match the path given, so this skips them", arg)
		}
	}

	if len(got) < 2 || got[len(got)-2] != sourcePath {
		t.Errorf("args = %v, want the changed files repo-relative at the end", got)
	}
}

func TestConventionGate_ScansChangedFiles(t *testing.T) {
	t.Parallel()
	requireAstGrep(t)

	tests := []struct {
		name     string
		source   string
		changes  []tool.Change
		wantPass bool
	}{
		{
			name:     "a bare nolint fails",
			source:   "package p\n\n//nolint:gosec\nfunc f() {}\n",
			changes:  []tool.Change{{Path: sourcePath}},
			wantPass: false,
		},
		{
			name:     "a nolint carrying a reason passes",
			source:   "package p\n\n//nolint:gosec // the path is this package's own\nfunc f() {}\n",
			changes:  []tool.Change{{Path: sourcePath}},
			wantPass: true,
		},
		{
			name:     "no changed files means nothing to scan",
			source:   "package p\n\n//nolint:gosec\nfunc f() {}\n",
			changes:  nil,
			wantPass: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root, rules := conventionFixture(t, tt.source)

			res, err := gate.NewConventionGate(root, rules, nil).Run(context.Background(),
				gate.RunContext{RepoRoot: root, Changes: tt.changes})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if res.Pass != tt.wantPass {
				t.Fatalf("Pass = %v, want %v (failures %+v)", res.Pass, tt.wantPass, res.Failures)
			}

			if tt.wantPass {
				return
			}

			if len(res.Failures) != 1 || res.Failures[0].Test != "no-bare-nolint" {
				t.Fatalf("Failures = %+v, want one grouped under the rule id", res.Failures)
			}

			if frames := res.Failures[0].Frames; len(frames) != 1 || !strings.Contains(frames[0], sourcePath+":3") {
				t.Errorf("Frames = %v, want one naming %s:3", frames, sourcePath)
			}
		})
	}
}
