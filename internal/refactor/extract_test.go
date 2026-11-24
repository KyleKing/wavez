package refactor_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/refactor"
)

// calcSource is the extract fixture. The tests name its lines by number.
const calcSource = `package lib

import "strings"

// Sum writes a label and the sum of the squares.
func Sum(xs []int, label string) string {
	var b strings.Builder
	b.WriteString(label)
	total := 0
	for _, x := range xs {
		total += x * x
	}
	b.WriteString(strings.Repeat("!", total))
	return b.String()
}

// Count stops at the first negative.
func Count(xs []int) int {
	n := 0
	for _, x := range xs {
		if x < 0 {
			break
		}
		n++
	}
	return n
}

// Later defers.
func Later(xs []int) (n int) {
	defer func() { n++ }()
	n = len(xs)
	return n
}

// Counter counts.
type Counter struct{ n int }

// Add adds k twice.
func (c *Counter) Add(k int) int {
	c.n += k
	c.n += k
	return c.n
}
`

func calcFixture(t *testing.T) string {
	t.Helper()

	return fixture(t, map[string]string{"lib/calc.go": calcSource})
}

func TestExtract(t *testing.T) {
	t.Parallel()
	needGopls(t)

	cases := []struct {
		// wantSource lists text lib/calc.go must hold afterwards.
		wantSource            []string
		name, target, newName string
		want                  refactor.ExtractResult
	}{
		{
			name: "statements with a loop into a function", target: "lib/calc.go:9-12", newName: "sumSquares",
			want: refactor.ExtractResult{
				At: "lib/calc.go:9-12", From: "Sum", Signature: "func sumSquares(xs []int) int",
			},
			wantSource: []string{"\ttotal := sumSquares(xs)\n", "func sumSquares(xs []int) int {\n\ttotal := 0\n"},
		},
		{
			name:   "a whole loop whose break stays inside the range",
			target: "lib/calc.go:20-25", newName: "countUntilNegative",
			want: refactor.ExtractResult{
				At: "lib/calc.go:20-25", From: "Count", Signature: "func countUntilNegative(xs []int, n int) int",
			},
			wantSource: []string{"n = countUntilNegative(xs, n)"},
		},
		{
			name: "one line inside a loop body", target: "lib/calc.go:24", newName: "bump",
			want:       refactor.ExtractResult{At: "lib/calc.go:24", From: "Count", Signature: "func bump(n int) int"},
			wantSource: []string{"n = bump(n)"},
		},
		{
			name: "lines that use the receiver into a method", target: "lib/calc.go:41-42", newName: "addTwice",
			want: refactor.ExtractResult{
				At: "lib/calc.go:41-42", From: "Counter.Add", Signature: "func (c *Counter) addTwice(k int)",
			},
			wantSource: []string{"\tc.addTwice(k)\n"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := calcFixture(t)

			res, err := newRefactorer(t, root).Extract(t.Context(), tc.target, tc.newName)
			require.NoError(t, err)

			assert.Equal(t, tc.want, res)

			src := read(t, root, "lib/calc.go")
			for _, want := range tc.wantSource {
				assert.Contains(t, src, want)
			}

			assert.NotContains(t, src, "newFunction")
			assert.NotContains(t, src, "newMethod")
			builds(t, root)
		})
	}
}

func TestExtract_DryRun(t *testing.T) {
	t.Parallel()
	needGopls(t)

	root := calcFixture(t)
	before := contents(t, root)

	res, err := newRefactorer(t, root, refactor.WithDryRun(true)).Extract(t.Context(), "lib/calc.go:9-12", "sumSquares")
	require.NoError(t, err)

	assert.Equal(t, "dry run, nothing was changed; a real run prints:\n"+
		"extracted lib/calc.go:9-12 from Sum into func sumSquares(xs []int) int\n", res.Summary())
	assert.Equal(t, before, contents(t, root))
}

func TestExtract_Refusals(t *testing.T) {
	t.Parallel()
	needGopls(t)

	cases := []struct {
		check                          func(t *testing.T, err error)
		name, target, newName, wantMsg string
	}{
		{
			name: "a range that cuts a loop", target: "lib/calc.go:10-11", newName: "x",
			check: isType[*refactor.ExtractError], wantMsg: "cut a statement in two; extract lines 10-12",
		},
		{
			name: "a range outside any function", target: "lib/calc.go:3", newName: "x",
			check: isType[*refactor.ExtractError], wantMsg: "not inside a function body",
		},
		{
			name: "a range that starts on the func line", target: "lib/calc.go:6-14", newName: "x",
			check: isType[*refactor.ExtractError], wantMsg: "not inside a function body",
		},
		{
			name: "a break out of the range", target: "lib/calc.go:21-23", newName: "x",
			check: isType[*refactor.ExtractError], wantMsg: "line 22 breaks out of a loop",
		},
		{
			name: "a defer", target: "lib/calc.go:31-32", newName: "x",
			check: isType[*refactor.ExtractError], wantMsg: "line 31 defers a call",
		},
		{
			name: "a name the package already declares", target: "lib/calc.go:9-12", newName: "Sum",
			check: isType[*refactor.GoplsError], wantMsg: "conflicts with func in same block",
		},
		{
			name: "a keyword", target: "lib/calc.go:9-12", newName: "func",
			check: isType[*refactor.InvalidNameError], wantMsg: "not a Go identifier",
		},
		{
			name: "lines past the end", target: "lib/calc.go:40-90", newName: "x",
			check: isType[*refactor.TargetError], wantMsg: "lib/calc.go has 44 lines",
		},
		{
			name: "lines backwards", target: "lib/calc.go:12-9", newName: "x",
			check: isType[*refactor.TargetError], wantMsg: "want FROM-TO",
		},
		{
			name: "a target with no lines", target: "lib/calc.go:Sum", newName: "x",
			check: isType[*refactor.TargetError], wantMsg: "want FROM-TO",
		},
		{
			name: "a directory", target: "./lib:9-12", newName: "x",
			check: isType[*refactor.TargetError], wantMsg: "must be a .go file",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := calcFixture(t)
			before := contents(t, root)

			_, err := newRefactorer(t, root).Extract(t.Context(), tc.target, tc.newName)
			refused(t, root, before, err, tc.check, tc.wantMsg)
		})
	}
}

// fakeExtractor writes a program that answers any code action by
// appending a function that does not compile, which is the broken result
// the build check exists to undo.
func fakeExtractor(t *testing.T) string {
	t.Helper()

	script := "#!/bin/sh\nprintf '\\nfunc newFunction() int { return missing }\\n' >> lib/calc.go\n"
	path := filepath.Join(t.TempDir(), "gopls")

	require.NoError(t, os.WriteFile(path, []byte(script), 0o700)) //nolint:gosec // a test script has to be executable

	return path
}

func TestExtract_RollsBackWhenItAddsAnError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		extra map[string]string
		name  string
	}{
		{name: "to a tree that built"},
		{name: "to a package that already failed to build", extra: map[string]string{"lib/broken.go": brokenSource}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			extra := map[string]string{"lib/calc.go": calcSource}
			for name, src := range tc.extra {
				extra[name] = src
			}

			root := fixture(t, extra)
			before := contents(t, root)

			_, err := newRefactorer(t, root, refactor.WithGopls(fakeExtractor(t))).
				Extract(t.Context(), "lib/calc.go:9-12", "newFunction")

			var build *refactor.BuildError

			require.ErrorAs(t, err, &build)
			assert.Contains(t, build.Output, "undefined: missing")
			assert.NotContains(t, build.Output, "broken.go")
			assert.Contains(t, err.Error(), "try a different range, or edit by hand")
			assert.Equal(t, before, contents(t, root), "a rolled-back extraction must leave every byte as it was")
		})
	}
}

func TestExtract_GoplsMissing(t *testing.T) {
	t.Parallel()

	root := calcFixture(t)
	before := contents(t, root)

	_, err := newRefactorer(t, root, refactor.WithGopls("gopls-is-not-installed")).
		Extract(t.Context(), "lib/calc.go:9-12", "sumSquares")

	var missing *refactor.ToolMissingError

	require.ErrorAs(t, err, &missing)
	assert.Equal(t, before, contents(t, root))
}
