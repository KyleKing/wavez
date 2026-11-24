package main

import (
	"bytes"
	"context"
	"os"
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
