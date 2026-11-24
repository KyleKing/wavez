package refactor_test

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/refactor"
)

const (
	paramsSource = `package lib

// Join joins parts with sep.
func Join(sep string, verbose bool, parts ...string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// Pick returns its first argument.
func Pick(a, _ int) int { return a }

// Ref is used as a value.
func Ref(a, b int) int { return a }

var refValue = Ref

// Shape has an area.
type Shape interface{ Area(scale int, unused bool) int }

// Sq is a square.
type Sq struct{ side int }

// Area implements Shape.
func (q Sq) Area(scale int, unused bool) int { return q.side * scale }

var _ Shape = Sq{}
`
	paramsCaller = `package app

import "example.com/m/lib"

// Joined calls lib.Join.
func Joined() string { return lib.Join(",", false, "a", "b") + lib.Join("-", len("x") > 0) }

// Picked calls lib.Pick.
func Picked() int { return lib.Pick(1, 2) }
`
	effectCaller = `package app

import (
	"os"

	"example.com/m/lib"
)

// Loud passes a call where Join wants verbose.
func Loud() string { return lib.Join(",", os.Getenv("V") != "", "a") }
`
	excludedCaller = `//go:build windows

package app

import "example.com/m/lib"

// Win calls Join on another platform.
func Win() string { return lib.Join(",", true, "w") }
`
)

func paramsFixture(t *testing.T, extra map[string]string) string {
	t.Helper()

	files := map[string]string{"lib/params.go": paramsSource, "app/params.go": paramsCaller}
	for name, src := range extra {
		files[name] = src
	}

	return fixture(t, files)
}

func TestParams(t *testing.T) {
	t.Parallel()
	needGopls(t)

	cases := []struct {
		// wantSource maps a file to text it must hold afterwards.
		wantSource          map[string]string
		name, target, names string
		want                refactor.ParamsResult
	}{
		{
			name: "remove an unused parameter at every call", target: "lib/params.go:Join", names: "sep,parts",
			want: refactor.ParamsResult{
				Name: "Join", Old: "(sep string, verbose bool, parts ...string)", New: "(sep string, parts ...string)",
				Files: []string{"app/params.go", "lib/params.go"},
			},
			wantSource: map[string]string{
				"lib/params.go": "func Join(sep string, parts ...string) string {",
				"app/params.go": `lib.Join(",", "a", "b") + lib.Join("-")`,
			},
		},
		{
			name: "reorder", target: "./lib:Join", names: "verbose, sep, parts",
			want: refactor.ParamsResult{
				Name: "Join", Old: "(sep string, verbose bool, parts ...string)",
				New:   "(verbose bool, sep string, parts ...string)",
				Files: []string{"app/params.go", "lib/params.go"},
			},
			wantSource: map[string]string{"app/params.go": `lib.Join(false, ",", "a", "b")`},
		},
		{
			name: "remove an unnamed parameter by the name gopls gives it", target: "lib/params.go:Pick", names: "a",
			want: refactor.ParamsResult{
				Name: "Pick", Old: "(a int, _0 int)", New: "(a int)", Files: []string{"app/params.go", "lib/params.go"},
			},
			wantSource: map[string]string{"app/params.go": "lib.Pick(1)"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := paramsFixture(t, nil)

			res, err := newRefactorer(t, root).Params(t.Context(), tc.target, tc.names)
			require.NoError(t, err)

			assert.Equal(t, tc.want, res)

			for file, want := range tc.wantSource {
				assert.Contains(t, read(t, root, file), want)
			}

			builds(t, root)
		})
	}
}

func TestParams_ListsCallsTheBuildExcludes(t *testing.T) {
	t.Parallel()
	needGopls(t)

	if runtime.GOOS == "windows" {
		t.Skip("the excluded file is the Windows one")
	}

	root := paramsFixture(t, map[string]string{"app/params_windows.go": excludedCaller})

	res, err := newRefactorer(t, root, refactor.WithDryRun(true)).Params(t.Context(), "lib/params.go:Join", "sep,parts")
	require.NoError(t, err)

	assert.Equal(t, []refactor.Mention{{File: "app/params_windows.go", Line: 8}}, res.Excluded)
	assert.Contains(t, res.Summary(), "not rewritten, excluded from this build (GOOS/GOARCH or build tag); "+
		"these may call it with the old parameters:\n  app/params_windows.go:8\n")
	assert.Contains(t, read(t, root, "lib/params.go"), "func Join(sep string, verbose bool, parts ...string)",
		"a dry run puts the tree back")
}

func TestParams_Refusals(t *testing.T) {
	t.Parallel()
	needGopls(t)

	cases := []struct {
		extra                        map[string]string
		check                        func(t *testing.T, err error)
		name, target, names, wantMsg string
	}{
		{
			name: "a parameter the body uses", target: "lib/params.go:Join", names: "verbose,parts",
			check: isType[*refactor.ParamsError], wantMsg: "its body still uses sep on line 8; remove those uses first",
		},
		{
			name: "a name it does not have", target: "lib/params.go:Join", names: "sep,parts,extra",
			check:   isType[*refactor.ParamsError],
			wantMsg: `no parameter "extra"; its parameters are sep, verbose, parts`,
		},
		{
			name: "a name twice", target: "lib/params.go:Join", names: "sep,sep,parts",
			check: isType[*refactor.ParamsError], wantMsg: "sep is named twice",
		},
		{
			name: "the same list", target: "lib/params.go:Join", names: "sep,verbose,parts",
			check: isType[*refactor.ParamsError], wantMsg: "already its parameter list",
		},
		{
			name: "a variadic parameter moved from last", target: "lib/params.go:Join", names: "parts,sep",
			check: isType[*refactor.ParamsError], wantMsg: "parts is variadic, so it has to stay last",
		},
		{
			name: "an argument that holds a call", target: "lib/params.go:Join", names: "sep,parts",
			extra: map[string]string{"app/loud.go": effectCaller},
			check: isType[*refactor.ParamsError],
			wantMsg: "would drop the call; move each to its own statement first:\n" +
				`  app/loud.go:10: os.Getenv("V") != ""`,
		},
		{
			name: "a function used as a value", target: "lib/params.go:Ref", names: "a",
			check: isType[*refactor.ParamsError], wantMsg: "used as a value at lib/params.go:21:16",
		},
		{
			name: "an interface method", target: "lib/params.go:Shape.Area", names: "scale",
			check: isType[*refactor.ParamsError], wantMsg: "it is an interface method",
		},
		{
			name: "a type", target: "lib/params.go:Sq", names: "",
			check: isType[*refactor.ParamsError], wantMsg: "it is a type, not a function or method",
		},
		{
			name: "an implementation alone, caught by the build", target: "lib/params.go:Sq.Area", names: "scale",
			check: isType[*refactor.BuildError], wantMsg: "Sq does not implement Shape",
		},
		{
			name: "two functions", target: "lib/params.go:Join,Pick", names: "sep",
			check: isType[*refactor.TargetError], wantMsg: "params takes exactly one function",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := paramsFixture(t, tc.extra)
			before := contents(t, root)

			_, err := newRefactorer(t, root).Params(t.Context(), tc.target, tc.names)
			refused(t, root, before, err, tc.check, tc.wantMsg)
		})
	}
}

func TestParams_GoplsMissing(t *testing.T) {
	t.Parallel()

	root := paramsFixture(t, nil)
	before := contents(t, root)

	_, err := newRefactorer(t, root, refactor.WithGopls("gopls-is-not-installed")).
		Params(t.Context(), "lib/params.go:Join", "sep,parts")

	var missing *refactor.ToolMissingError

	require.ErrorAs(t, err, &missing)
	assert.Equal(t, before, contents(t, root))
}
