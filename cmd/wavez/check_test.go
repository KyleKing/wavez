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
	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/session"
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

// checkRun runs the command and returns what it printed and the status the
// process would exit with.
func checkRun(ctx context.Context, args ...string) (string, string, int) {
	var out, errOut bytes.Buffer

	code := status(runCheck(ctx, args, stdio{in: strings.NewReader(""), out: &out, err: &errOut}), "check", &errOut)

	return out.String(), errOut.String(), code
}

func TestRunCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		out    string
		errOut string
		args   []string
		code   int
	}{
		{name: "a clean change prints one line", source: gateClean, out: "wavez check: passed build, test\n"},
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

			out, errOut, code := checkRun(t.Context(), args...)
			if out != tt.out || code != tt.code || !strings.Contains(errOut, tt.errOut) {
				t.Errorf("got status %d\nstdout: %q\nstderr: %q", code, out, errOut)
			}

			if tt.errOut == "" && errOut != "" {
				t.Errorf("unexpected stderr: %q", errOut)
			}
		})
	}
}

func TestRunCheck_JSON(t *testing.T) {
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

			out, _, code := checkRun(t.Context(), "--json", "--root", root, filepath.Join(root, tt.file))
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

func TestRunCheck_RejectsAFileOutsideTheRoot(t *testing.T) {
	t.Parallel()

	root := gateProject(t, gateClean)

	_, errOut, code := checkRun(t.Context(), "--root", root, filepath.Join(t.TempDir(), "other.go"))
	if code != 1 || !strings.Contains(errOut, "outside the root") {
		t.Errorf("status %d, stderr %q", code, errOut)
	}
}

func TestRunCheck_AsksGitWhenNoFileIsNamed(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("NOT TESTED: git is not on PATH, so nothing that needs a repository ran")
	}

	root := gateProject(t, gateBroken)

	if _, err := gate.Changed(t.Context(), root); !errors.Is(err, gate.ErrNotRepository) {
		t.Skip("the temp dir is inside a repository on this machine")
	}

	out, errOut, code := checkRun(t.Context(), "--root", root)
	if code != 0 || !strings.Contains(out, "not a repository") || errOut != "" {
		t.Errorf("outside a repository: status %d, stdout %q, stderr %q", code, out, errOut)
	}

	init := exec.CommandContext(t.Context(), "git", "init", "-q")
	init.Dir = root

	if msg, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, msg)
	}

	out, _, code = checkRun(t.Context(), "--root", root)
	if code != 1 || !strings.HasPrefix(out, "FAIL build\n") {
		t.Errorf("inside a repository: status %d, stdout %q", code, out)
	}
}

func TestRunCheck_ExitStatusAndPassLine(t *testing.T) {
	t.Parallel()

	const cached = "[[check]]\nname = \"notes\"\nglobs = [\"*.md\"]\ncommand = \"true\"\ninputs = []\n"

	tests := []struct {
		name   string
		config string
		file   string
		out    []string
		errOut string
		codes  []int
	}{
		{
			name:   "a check that could not run exits 3 and tells stderr",
			config: "[[check]]\nname = \"gone\"\nglobs = [\"*.md\"]\ncommand = \"wavez-no-such-tool {files}\"\n",
			file:   "notes.md", out: []string{""}, errOut: "NOT CHECKED gone", codes: []int{3},
		},
		{
			name: "a second run on the same bytes says the pass was reused", config: cached, file: "notes.md",
			out:   []string{"wavez check: passed notes\n", "wavez check: passed notes (unchanged: notes)\n"},
			codes: []int{0, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			for name, body := range map[string]string{".wavez.toml": tt.config, tt.file: "x\n"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
					t.Fatalf("writing %s: %v", name, err)
				}
			}

			for i := range tt.codes {
				out, errOut, code := checkRun(t.Context(), "--root", root, filepath.Join(root, tt.file))
				if out != tt.out[i] || code != tt.codes[i] || !strings.Contains(errOut, tt.errOut) {
					t.Errorf("run %d: status %d\nstdout: %q\nstderr: %q", i, code, out, errOut)
				}
			}
		})
	}
}

func TestRunGate_IsCheckUnderItsOlderName(t *testing.T) {
	t.Parallel()

	root := gateProject(t, gateBroken)

	var out, errOut bytes.Buffer

	err := runGate(t.Context(), []string{"--root", root, filepath.Join(root, "one.go")},
		stdio{in: strings.NewReader(""), out: &out, err: &errOut})
	if status(err, "gate", &errOut) != 1 || !strings.HasPrefix(out.String(), "FAIL build\n") {
		t.Errorf("got %v, stdout %q, stderr %q", err, out.String(), errOut.String())
	}
}

// The session variable is process-wide, so this test does not run in
// parallel with the others.
func TestRunCheck_ChecksWhatTheSessionChanged(t *testing.T) {
	root := gateProject(t, gateBroken)

	if _, err := gate.Changed(t.Context(), root); !errors.Is(err, gate.ErrNotRepository) {
		t.Skip("the temp dir is inside a repository on this machine")
	}

	_, err := session.NewStore(root).Update(t.Context(), "s1", func(state *session.State) error {
		state.Record("one.go")

		return nil
	})
	if err != nil {
		t.Fatalf("recording the session: %v", err)
	}

	t.Setenv(hook.SessionEnv, "s1")

	out, errOut, code := checkRun(t.Context(), "--root", root)
	if code != 1 || !strings.HasPrefix(out, "FAIL build\n") {
		t.Errorf("status %d, stdout %q, stderr %q", code, out, errOut)
	}

	t.Setenv(hook.SessionEnv, "unknown")

	out, _, code = checkRun(t.Context(), "--root", root)
	if code != 0 || !strings.Contains(out, "not a repository") {
		t.Errorf("an unknown session falls back to the working tree: status %d, stdout %q", code, out)
	}
}

func TestRunCheck_RemembersAPassInTheSession(t *testing.T) {
	tests := []struct {
		name   string
		source string
		passed bool
	}{
		{name: "a pass is what the session last passed on", source: gateClean, passed: true},
		{name: "a failure is left for the stop to count", source: gateBroken},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := gateProject(t, tt.source)
			store := session.NewStore(root)

			_, err := store.Update(t.Context(), "s1", func(state *session.State) error {
				state.Record("one.go")

				return nil
			})
			if err != nil {
				t.Fatalf("recording the session: %v", err)
			}

			t.Setenv(hook.SessionEnv, "s1")
			checkRun(t.Context(), "--root", root)

			want, err := session.Fingerprint(root, []string{"one.go"})
			if err != nil {
				t.Fatalf("fingerprinting: %v", err)
			}

			state, _ := store.Read("s1")
			if got := state.Passed == want; got != tt.passed {
				t.Errorf("passed %q, change set %q, want remembered = %v", state.Passed, want, tt.passed)
			}

			if state.Failure.Rounds != 0 {
				t.Errorf("a check run by hand counted %d failing rounds", state.Failure.Rounds)
			}
		})
	}
}
