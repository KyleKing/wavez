package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// applyRun runs the command on edits and returns what it printed and the
// status the process would exit with.
func applyRun(ctx context.Context, edits string, args ...string) (string, string, int) {
	var out, errOut bytes.Buffer

	code := status(runApply(ctx, args, stdio{in: strings.NewReader(edits), out: &out, err: &errOut}), "apply", &errOut)

	return out.String(), errOut.String(), code
}

func TestRunApply(t *testing.T) {
	t.Parallel()

	const (
		toTwo   = "=== one.go\n<<<<<<<\nfunc One() int { return 1 }\n=======\nfunc One() int { return 2 }\n>>>>>>>\n"
		toWrong = "=== one.go\n<<<<<<<\nfunc One() int { return 1 }\n=======\n" +
			"func One() int { return missing }\n>>>>>>>\n"
		noMatch = "=== one.go\n<<<<<<<\nfunc Two() int { return 1 }\n=======\nfunc Two() int { return 2 }\n>>>>>>>\n"
	)

	tests := []struct {
		name   string
		edits  string
		out    string
		errOut string
		source string
		args   []string
		code   int
	}{
		{
			name: "edits that apply are checked", edits: toTwo, out: "wavez apply: passed build, test\n",
			source: "package scratch\n\n// One returns one.\nfunc One() int { return 2 }\n",
		},
		{
			name: "an edit that breaks the build says so", edits: toWrong, code: 1, out: "FAIL build\n",
			source: "package scratch\n\n// One returns one.\nfunc One() int { return missing }\n",
		},
		{
			name: "an edit that does not apply changes nothing", edits: noMatch, code: 2,
			errOut: "the lines to replace are not in the file", source: gateClean,
		},
		{
			name: "no check on request", edits: toWrong, args: []string{"--no-check"},
			source: "package scratch\n\n// One returns one.\nfunc One() int { return missing }\n",
		},
		{name: "nothing on stdin", edits: "", code: 2, errOut: "no edit found", source: gateClean},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := gateProject(t, gateClean)

			out, errOut, code := applyRun(t.Context(), tt.edits, append([]string{"--root", root}, tt.args...)...)
			if code != tt.code || !strings.HasPrefix(out, tt.out) || !strings.Contains(errOut, tt.errOut) {
				t.Errorf("status %d, stdout %q, stderr %q", code, out, errOut)
			}

			got, err := os.ReadFile(filepath.Join(root, "one.go")) //nolint:gosec // a file the test wrote
			if err != nil || string(got) != tt.source {
				t.Errorf("one.go = %q, %v", got, err)
			}
		})
	}
}

// committed makes root a git repository with everything in it committed.
func committed(t *testing.T, root string) {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("NOT TESTED: git is not on PATH")
	}

	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "base"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // fixed program, test arguments
		cmd.Dir = root

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func fileText(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(path) //nolint:gosec // a file the test wrote
	if err != nil {
		t.Fatal(err)
	}

	return string(body)
}

// A fix made at a revision lands in a second copy of the tree and is checked
// there, whatever state the checkout is in.
func TestRunApply_AtARevisionLeavesTheCheckoutAlone(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("HOME", cache)

	root := gateProject(t, gateClean)
	committed(t, root)

	// The checkout has moved on to something that does not build yet.
	if err := os.WriteFile(filepath.Join(root, "one.go"), []byte(gateBroken), 0o600); err != nil {
		t.Fatal(err)
	}

	edits := "=== one.go\n" + strings.Repeat("<", 7) + "\nfunc One() int { return 1 }\n" + strings.Repeat("=", 7) +
		"\nfunc One() int { return 2 }\n" + strings.Repeat(">", 7) + "\n"

	out, errOut, code := applyRun(t.Context(), edits, "--root", root, "--at", "HEAD")
	said := strings.Contains(errOut, "the checkout is left as it is")
	if code != 0 || out != "wavez apply: passed build, test\n" || !said {
		t.Fatalf("status %d, stdout %q, stderr %q", code, out, errOut)
	}

	if got := fileText(t, filepath.Join(root, "one.go")); got != gateBroken {
		t.Errorf("the checkout changed: %q", got)
	}

	// Where the user's cache directory is differs by system, so the lane is
	// found by what it holds.
	var lanes []string

	err := filepath.WalkDir(cache, func(path string, entry os.DirEntry, err error) error {
		if err == nil && entry.Name() == "one.go" && strings.Contains(path, "lanes") {
			lanes = append(lanes, path)
		}

		return err
	})
	if err != nil || len(lanes) != 1 {
		t.Fatalf("lanes under the cache: %v, %v", lanes, err)
	}

	if got := fileText(t, lanes[0]); !strings.Contains(got, "return 2") {
		t.Errorf("the lane holds %q", got)
	}
}
