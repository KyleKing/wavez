package gofix_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/gofix"
)

// The project requires parallel tests and the linter cannot fix a missing
// call, so 18 of the 96 non-compile lint findings logged against a model
// were this one line. It is mechanical and semantics-preserving, which is
// what puts it beside gofmt rather than in front of a model.
func TestAddParallelCalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
		skip []string
	}{
		{
			name: "a plain test gains the call",
			src:  "package a\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {\n\t_ = 1\n}\n",
			want: []string{"t.Parallel()"},
		},
		{
			name: "a subtest is called on its own receiver",
			src: "package a\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {\n\tt.Parallel()\n" +
				"\tt.Run(\"c\", func(sub *testing.T) {\n\t\t_ = 1\n\t})\n}\n",
			want: []string{"sub.Parallel()"},
		},
		{
			name: "t.Setenv would panic beside it",
			src:  "package a\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {\n\tt.Setenv(\"K\", \"v\")\n}\n",
			skip: []string{"Parallel"},
		},
		{
			name: "a subtest's t.Setenv holds the parent back too",
			src: "package a\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {\n" +
				"\tt.Run(\"c\", func(t *testing.T) {\n\t\tt.Setenv(\"K\", \"v\")\n\t})\n}\n",
			skip: []string{"Parallel"},
		},
		{
			name: "a nolint directive is a decision, not an oversight",
			src: "package a\n\nimport \"testing\"\n\n//nolint:paralleltest // one server, one model\n" +
				"func TestX(t *testing.T) {\n\t_ = 1\n}\n",
			skip: []string{"Parallel"},
		},
		{
			name: "TestMain owns the process",
			src:  "package a\n\nimport \"testing\"\n\nfunc TestMain(m *testing.M) {\n\tm.Run()\n}\n",
			skip: []string{"Parallel"},
		},
		{
			name: "a blank receiver has nothing to call",
			src:  "package a\n\nimport \"testing\"\n\nfunc TestX(_ *testing.T) {\n\t_ = 1\n}\n",
			skip: []string{"Parallel"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := gofix.AddParallelCalls("a_test.go", []byte(tt.src))
			if err != nil {
				t.Fatalf("AddParallelCalls: %v", err)
			}

			got := tt.src
			if out != nil {
				got = string(out)
			}

			assertContains(t, got, tt.want, tt.skip)
		})
	}
}

// A fixture is data a test asserts against, not a test that runs, so
// rewriting one changes what the assertion means.
func TestAddParallelCallsLeavesFixturesAlone(t *testing.T) {
	t.Parallel()

	src := []byte("package a\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {\n\t_ = 1\n}\n")

	for _, path := range []string{filepath.Join("testdata", "f", "a_test.go"), "a.go"} {
		out, err := gofix.AddParallelCalls(path, src)
		if err != nil {
			t.Fatalf("AddParallelCalls(%s): %v", path, err)
		}
		if out != nil {
			t.Errorf("%s was rewritten", path)
		}
	}
}

// Every test file in this repository already lints clean, so the pass must
// find nothing to do in any of them. It is the check that catches a rule
// written too broadly, which the first cut was twice: once by reading a
// subtest's t.Parallel as the parent's, once by ignoring a nolint directive.
func TestAddParallelCallsIsQuietOnACleanTree(t *testing.T) {
	t.Parallel()

	var rewritten []string

	for _, path := range goTestFiles(t, "../..") {
		src, err := os.ReadFile(path) //nolint:gosec // walking this repository's own tree
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}

		out, err := gofix.AddParallelCalls(path, src)
		if err != nil {
			t.Fatalf("AddParallelCalls(%s): %v", path, err)
		}
		if out != nil {
			rewritten = append(rewritten, path)
		}
	}

	if len(rewritten) != 0 {
		t.Errorf("would rewrite %d file(s) that already lint clean: %v", len(rewritten), rewritten)
	}
}

func goTestFiles(t *testing.T, root string) []string {
	t.Helper()

	var out []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(path, "_test.go") {
			out = append(out, path)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	return out
}

func assertContains(t *testing.T, got string, want, skip []string) {
	t.Helper()

	for _, fragment := range want {
		if !strings.Contains(got, fragment) {
			t.Errorf("output missing %q:\n%s", fragment, got)
		}
	}

	for _, fragment := range skip {
		if strings.Contains(got, fragment) {
			t.Errorf("output should not contain %q:\n%s", fragment, got)
		}
	}
}
