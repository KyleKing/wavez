package deadcode_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/deadcode"
)

// fakeAnalyzer writes a script printing stdout verbatim and exiting with
// code, so the suite needs no analyzer installed.
func fakeAnalyzer(t *testing.T, stdout string, code int) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "deadcode")
	script := fmt.Sprintf("#!/bin/sh\ncat <<'EOF'\n%s\nEOF\nexit %d\n", stdout, code)

	//nolint:gosec // a fixture the test executes has to be executable
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("writing fake analyzer: %v", err)
	}

	return path
}

const twoFuncs = `[
	{"Path":"github.com/x/p/internal/agent","Funcs":[
		{"Name":"WithClock","Position":{"File":"internal/agent/loop.go","Line":242,"Col":6}},
		{"Name":"Orphan","Position":{"File":"internal/agent/loop.go","Line":10,"Col":6}}
	]}
]`

func TestRunSeparatesDeliberateFromActionable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		allow          []string
		wantActionable []string
	}{
		{
			name:           "no allowlist makes everything actionable",
			wantActionable: []string{"Orphan", "WithClock"},
		},
		{
			name:           "a bare-name glob covers functional options everywhere",
			allow:          []string{"With*"},
			wantActionable: []string{"Orphan"},
		},
		{
			name:           "a package-qualified pattern covers one package",
			allow:          []string{"agent.With*"},
			wantActionable: []string{"Orphan"},
		},
		{
			name:           "a full import path works too",
			allow:          []string{"github.com/x/p/internal/agent.*"},
			wantActionable: nil,
		},
		{
			name:           "a pattern matching nothing changes nothing",
			allow:          []string{"nosuch.*"},
			wantActionable: []string{"Orphan", "WithClock"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := deadcode.New(t.TempDir(),
				deadcode.WithBinary(fakeAnalyzer(t, twoFuncs, 0)),
				deadcode.WithAllow(tt.allow))

			report, err := a.Run(context.Background(), "./...")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if len(report.Unreached) != 2 {
				t.Fatalf("Unreached = %d, want 2; the full picture is always reported", len(report.Unreached))
			}

			got := make([]string, 0, len(report.Actionable()))
			for _, f := range report.Actionable() {
				got = append(got, f.Name)
			}

			if len(got) != len(tt.wantActionable) {
				t.Fatalf("Actionable() = %v, want %v", got, tt.wantActionable)
			}

			for i := range got {
				if got[i] != tt.wantActionable[i] {
					t.Errorf("Actionable()[%d] = %q, want %q", i, got[i], tt.wantActionable[i])
				}
			}
		})
	}
}

// A report that could not run must never read as a clean one.
func TestRunReportsAMissingAnalyzer(t *testing.T) {
	t.Parallel()

	a := deadcode.New(t.TempDir(), deadcode.WithBinary("wavez-no-such-analyzer"))

	_, err := a.Run(context.Background(), "./...")
	if !errors.Is(err, deadcode.ErrUnavailable) {
		t.Fatalf("Run error = %v, want ErrUnavailable", err)
	}
}

func TestRunReportsAFailingAnalyzer(t *testing.T) {
	t.Parallel()

	a := deadcode.New(t.TempDir(), deadcode.WithBinary(fakeAnalyzer(t, "not json", 1)))

	if _, err := a.Run(context.Background(), "./..."); err == nil {
		t.Fatal("Run returned no error for an analyzer that exited nonzero")
	}
}
