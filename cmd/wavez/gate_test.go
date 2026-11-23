package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
)

const (
	gateGoMod       = "module example.com/scratch\n\ngo 1.24\n"
	gateClean       = "package scratch\n\n// One returns one.\nfunc One() int { return 1 }\n"
	gateBroken      = "package scratch\n\nfunc One() int { return missing }\n"
	gateUnformatted = "package scratch\n\nfunc  One( ) int {return 1}\n"
)

// gateProject writes a one-package module holding source and returns its
// root.
func gateProject(t *testing.T, source string) string {
	t.Helper()

	root := t.TempDir()
	for name, body := range map[string]string{"go.mod": gateGoMod, "one.go": source} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	return root
}

// gateRun runs the command and returns what it printed and the status the
// process would exit with.
func gateRun(ctx context.Context, args ...string) (string, string, int) {
	var out, errOut bytes.Buffer

	code := status(runGate(ctx, args, stdio{in: strings.NewReader(""), out: &out, err: &errOut}), "gate", &errOut)

	return out.String(), errOut.String(), code
}

func TestRunGate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		out    string
		errOut string
		args   []string
		code   int
	}{
		{name: "a clean change prints nothing", source: gateClean},
		{
			name: "a compile error prints the failure and exits 1", source: gateBroken, code: 1,
			out: "FAIL build\n./one.go:3:25: undefined: missing\n",
		},
		{
			name: "the edit stage formats and says so", source: gateUnformatted, args: []string{"--stage", "edit"},
			out: "REWRITTEN by a formatter, re-read before editing: one.go\n",
		},
		{
			name: "an unknown stage is a usage error", source: gateClean, args: []string{"--stage", "commit"},
			errOut: `--stage is "commit"`, code: 1,
		},
		{
			name: "an unknown flag is a usage error", source: gateClean, args: []string{"--nope"},
			errOut: "flag provided but not defined", code: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := gateProject(t, tt.source)
			args := slices.Concat(tt.args, []string{"--root", root, filepath.Join(root, "one.go")})

			out, errOut, code := gateRun(t.Context(), args...)
			if out != tt.out || code != tt.code || !strings.Contains(errOut, tt.errOut) {
				t.Errorf("got status %d\nstdout: %q\nstderr: %q", code, out, errOut)
			}

			if tt.errOut == "" && errOut != "" {
				t.Errorf("unexpected stderr: %q", errOut)
			}
		})
	}
}

func TestRunGate_JSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		file   string
		want   []gate.Status
		code   int
	}{
		{"a failure is reported and exits 1", gateBroken, "one.go", []gate.Status{gate.StatusFail}, 1},
		{"a pass lists every check", gateClean, "one.go", []gate.Status{gate.StatusPass, gate.StatusPass}, 0},
		{"a change that selects nothing is an empty list", gateClean, ".github/notes.md", []gate.Status{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := gateProject(t, tt.source)

			out, _, code := gateRun(t.Context(), "--json", "--root", root, filepath.Join(root, tt.file))
			if code != tt.code {
				t.Errorf("status %d, want %d", code, tt.code)
			}

			var report gate.Report
			if err := json.Unmarshal([]byte(out), &report); err != nil {
				t.Fatalf("stdout is not a report: %v\n%s", err, out)
			}

			if report.Results == nil || len(report.Results) != len(tt.want) {
				t.Fatalf("got %s, want %d results", out, len(tt.want))
			}

			for i, status := range tt.want {
				if report.Results[i].Status != status {
					t.Errorf("result %d is %s, want %s", i, report.Results[i].Status, status)
				}
			}
		})
	}
}

func TestRunGate_RejectsAFileOutsideTheRoot(t *testing.T) {
	t.Parallel()

	root := gateProject(t, gateClean)

	_, errOut, code := gateRun(t.Context(), "--root", root, filepath.Join(t.TempDir(), "other.go"))
	if code != 1 || !strings.Contains(errOut, "outside the root") {
		t.Errorf("status %d, stderr %q", code, errOut)
	}
}

func TestRunGate_AsksGitWhenNoFileIsNamed(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	root := gateProject(t, gateBroken)

	if _, err := gate.Changed(t.Context(), root); !errors.Is(err, gate.ErrNotRepository) {
		t.Skip("the temp dir is inside a repository on this machine")
	}

	out, errOut, code := gateRun(t.Context(), "--root", root)
	if code != 0 || !strings.Contains(out, "not a git repository") || errOut != "" {
		t.Errorf("outside a repository: status %d, stdout %q, stderr %q", code, out, errOut)
	}

	init := exec.CommandContext(t.Context(), "git", "init", "-q")
	init.Dir = root

	if msg, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, msg)
	}

	out, _, code = gateRun(t.Context(), "--root", root)
	if code != 1 || !strings.HasPrefix(out, "FAIL build\n") {
		t.Errorf("inside a repository: status %d, stdout %q", code, out)
	}
}
