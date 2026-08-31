package tools_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/codeintel/lang"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

// declareProject indexes one package so a declaration has somewhere to be
// found and somewhere to be added.
func declareProject(t *testing.T) (string, *tools.Declare) {
	t.Helper()

	root := t.TempDir()
	writeFile(t, root, "memory.go", `package sysinfo

// Free is what is left.
func Free() uint64 { return 1 }
`)
	writeFile(t, root, "memory_test.go", "package sysinfo\n")

	store, err := codeintel.Open(t.Context(), filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("codeintel.Open: %v", err)
	}

	t.Cleanup(func() {
		if cerr := store.Close(); cerr != nil {
			t.Errorf("closing the store: %v", cerr)
		}
	})

	indexer := codeintel.NewIndexer(store, root, lang.NewDefaultRegistry())

	return root, tools.NewDeclare(root, indexer, tools.NewScope(root, false))
}

// Replacing a declaration through str_replace costs its text twice, once as
// the anchor and once as the replacement, against a file the model is
// recalling rather than reading. Measured on `e2`, that produced
// ~12,000-character arguments cut off mid-string at normal entropy. Here
// the source is sent once and nothing has to match.
func TestDeclareReplacesWithoutAnAnchor(t *testing.T) {
	t.Parallel()

	root, d := declareProject(t)

	res, err := d.Run(t.Context(), mustJSON(t, map[string]any{
		"symbol": "Free",
		"source": "func Free() uint64 { return 2 }",
		"doc":    "Free is what is left, in bytes.",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.IsError {
		t.Fatalf("declare failed: %s", res.Content)
	}

	got := readSeeded(t, root, "memory.go")
	for _, want := range []string{"// Free is what is left, in bytes.", "return 2"} {
		if !strings.Contains(got, want) {
			t.Errorf("file missing %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "return 1") {
		t.Errorf("the old declaration survived:\n%s", got)
	}

	if strings.Contains(got, "// Free is what is left.\n// Free is") {
		t.Errorf("the old doc comment survived beside the new one:\n%s", got)
	}

	// The index answers with absolute paths. A gate builds a `go test`
	// pattern by prefixing "./", so an absolute change path is resolved
	// against the root a second time and the run chases a directory that
	// was never missing.
	if len(res.Changes) != 1 || filepath.IsAbs(res.Changes[0].Path) {
		t.Errorf("Changes = %+v, want one repo-relative path", res.Changes)
	}
	if strings.Contains(res.Content, root) {
		t.Errorf("Content = %q, want it to name the file the way the project does", res.Content)
	}
}

// A name the index does not hold is an addition, and an addition has to be
// told where it goes. This is the `e2` shape: a method in one file and a
// test in another, neither of which exists yet.
func TestDeclareAddsWhatIsNotThereYet(t *testing.T) {
	t.Parallel()

	root, d := declareProject(t)

	missing, err := d.Run(t.Context(), mustJSON(t, map[string]any{
		"symbol": "UsedFraction", "source": "func UsedFraction() float64 { return 0 }",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !missing.IsError || missing.Cause != tool.CauseBadInput {
		t.Fatalf("an addition with no path was accepted: %+v", missing)
	}

	added, err := d.Run(t.Context(), mustJSON(t, map[string]any{
		"symbol": "UsedFraction",
		"source": "func UsedFraction() float64 { return 0 }",
		"path":   "memory.go",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if added.IsError {
		t.Fatalf("declare failed: %s", added.Content)
	}

	if got := readSeeded(t, root, "memory.go"); !strings.Contains(got, "UsedFraction") {
		t.Errorf("the declaration was not added:\n%s", got)
	}

	if len(added.Changes) != 1 || added.Changes[0].Path != "memory.go" {
		t.Errorf("Changes = %+v, want one naming the file it wrote", added.Changes)
	}

	// The package the declaration landed in is what a caller needs to know
	// to qualify the names it uses. Measured on `e2`, every remaining
	// failure was a test written against the wrong package clause.
	if !strings.Contains(added.Content, "package sysinfo") {
		t.Errorf("Content = %q, want it to name the package it added into", added.Content)
	}
}

func readSeeded(t *testing.T, root, rel string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // this test's own temp file
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}

	return string(body)
}

// A caller writing a new test sends the import it needs alongside the
// function, because that is what the source looks like in a file.
// Appending that whole thing puts a second import block after existing
// declarations, which Go rejects: measured on `e2`, one run produced
// exactly that and the file would not compile.
func TestDeclareMergesTheImportsItIsSent(t *testing.T) {
	t.Parallel()

	root, d := declareProject(t)
	writeFile(t, root, "memory_test.go",
		"package sysinfo_test\n\nimport (\n\t\"testing\"\n)\n\nfunc TestOld(t *testing.T) {}\n")

	res, err := d.Run(t.Context(), mustJSON(t, map[string]any{
		"symbol": "TestUsedFraction",
		"path":   "memory_test.go",
		"source": "import (\n\t\"fmt\"\n)\n\nfunc TestUsedFraction(t *testing.T) { fmt.Println(1) }",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.IsError {
		t.Fatalf("declare failed: %s", res.Content)
	}

	got := readSeeded(t, root, "memory_test.go")

	if strings.Count(got, "import (") != 1 {
		t.Errorf("want one import block, got:\n%s", got)
	}

	if !strings.Contains(got, "\t\"fmt\"") || !strings.Contains(got, "\t\"testing\"") {
		t.Errorf("both imports should survive the merge:\n%s", got)
	}

	if strings.Index(got, "import (") > strings.Index(got, "func TestOld") {
		t.Errorf("the imports must stay above the declarations:\n%s", got)
	}

	// An external test package is the one thing about the destination a
	// caller cannot see and keeps getting wrong.
	if !strings.Contains(res.Content, "external test package") {
		t.Errorf("Content = %q, want it to say names need their qualifier", res.Content)
	}
}
