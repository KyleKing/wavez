package gate_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/gate"
)

func TestShape(t *testing.T) {
	t.Parallel()

	const base = "//go:build linux\n\npackage a\n\nimport \"fmt\"\n\n// T is a thing.\ntype T struct{ n int }\n\n" +
		"var hook = func() int { return 1 }\n\n// Get reads n.\nfunc (t T) Get() int { return t.n }\n\n" +
		"func show() { fmt.Println(1) }\n"

	tests := []struct {
		edit func(string) string
		name string
		same bool
	}{
		{name: "a method body", edit: replace("return t.n", "return t.n + 0"), same: true},
		{
			name: "a function body gaining lines", same: true,
			edit: replace("fmt.Println(1)", "x := 2\n\tfmt.Println(x)"),
		},
		{name: "a signature", edit: replace("Get() int", "Get() int64")},
		{name: "a struct field", edit: replace("n int", "n int64")},
		{name: "a build constraint", edit: replace("linux", "darwin")},
		{name: "a doc comment", edit: replace("reads n", "returns n")},
		{name: "an import", edit: replace(`import "fmt"`, "import (\n\t\"fmt\"\n\t_ \"os\"\n)"), same: true},
		{name: "a variable's initializer", edit: replace("return 1", "return 2")},
		{name: "a new function", edit: func(s string) string { return s + "\nfunc More() {}\n" }},
	}

	was, ok := gate.ShapeOf(base)
	require.True(t, ok)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			edited := tt.edit(base)
			require.NotEqual(t, base, edited)

			now, ok := gate.ShapeOf(edited)
			require.True(t, ok)
			assert.Equal(t, tt.same, was == now)
		})
	}

	_, ok = gate.ShapeOf("package a\n\nfunc (")
	assert.False(t, ok, "a file that does not parse has no shape")
}

func replace(old, with string) func(string) string {
	return func(s string) string { return strings.Replace(s, old, with, 1) }
}

const srcBNeverBuilt = "package b\n\nimport \"example.com/scratch/a\"\n\n" +
	"// B is broken.\nfunc B() int { return a.A() + helper() }\n"

// An importer that never compiled is the measure: it is compiled, and
// fails, exactly when the change is taken to reach it.
func TestRun_BuildSkipsImportersOfAChangeToFunctionBodies(t *testing.T) {
	t.Parallel()

	onlyA := gate.WithSelector(&fakeSelector{sel: gate.Selection{
		Complete: true, Tests: map[string][]string{"./a": {"TestA"}},
	}})

	tests := []struct {
		name  string
		a     string
		wants []want
	}{
		{
			name: "a body is rewritten", a: "package a\n\n// A returns one.\nfunc A() int { return 2 - 1 }\n",
			wants: passed("build", "test"),
		},
		{
			name: "a declaration is added", a: srcA + "\n// More is new.\nfunc More() {}\n",
			wants: []want{{"build", gate.StatusFail, "undefined: helper"}},
		},
		{
			name: "the file does not parse", a: "package a\n\n// A returns one.\nfunc A() int { return 1 \n",
			wants: []want{{"build", gate.StatusFail, "a/a.go"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := committed(t, module(map[string]string{"b/b.go": srcBNeverBuilt}))
			writeFile(t, root, "a/a.go", tt.a)

			expect(t, runGate(t.Context(), t, root, gate.StageStop, []string{"a/a.go"}, onlyA), tt.wants...)
		})
	}
}
