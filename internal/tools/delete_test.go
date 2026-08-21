package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/codeintel/lang"
	"github.com/kyleking/wavez/internal/lsp"
	"github.com/kyleking/wavez/internal/tools"
)

// deleteProject indexes one file holding three declarations, so a deletion
// has neighbors on both sides to leave alone.
func deleteProject(t *testing.T) (string, *tools.Delete) {
	t.Helper()

	const src = `package a

// Alpha names the first lane.
func Alpha() string { return "alpha" }

// Beta names the second lane.
// It has two comment lines.
func Beta() string { return "beta" }

// Gamma names the third lane.
func Gamma() string { return "gamma" }
`

	root := t.TempDir()
	writeFile(t, root, "a.go", src)

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

	return root, tools.NewDelete(root, indexer, nil, tools.NewScope(false))
}

func TestDeleteTakesTheDocCommentWithIt(t *testing.T) {
	t.Parallel()

	root, del := deleteProject(t)

	res, err := del.Run(t.Context(), []byte(`{"symbol":"Beta"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.IsError {
		t.Fatalf("delete failed: %s", res.Content)
	}

	got := read(t, root, "a.go")

	for _, gone := range []string{"func Beta()", "Beta names the second lane", "It has two comment lines"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived the deletion:\n%s", gone, got)
		}
	}

	// The neighbors on both sides, and their comments, are the point.
	kept := []string{
		"Alpha names the first lane", "func Alpha()",
		"Gamma names the third lane", "func Gamma()",
	}
	for _, kept := range kept {
		if !strings.Contains(got, kept) {
			t.Errorf("%q was taken with it:\n%s", kept, got)
		}
	}

	if strings.Contains(got, "\n\n\n") {
		t.Errorf("the deletion left a double blank line:\n%s", got)
	}

	if len(res.Changes) != 1 || res.Changes[0].Path != "a.go" {
		t.Errorf("reported changes %v, want one for a.go", res.Changes)
	}
}

func TestDeleteRefusalsNameWhatToDoNext(t *testing.T) {
	t.Parallel()

	root, del := deleteProject(t)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "missing symbol", input: `{}`, want: "symbol is required"},
		{name: "unknown symbol", input: `{"symbol":"Nowhere"}`, want: "Nowhere"},
		{name: "narrowed to the wrong place", input: `{"symbol":"Beta","path":"nowhere"}`, want: "a.go"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := del.Run(t.Context(), []byte(tc.input))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if !res.IsError {
				t.Fatalf("want a refusal, got: %s", res.Content)
			}

			if !strings.Contains(res.Content, tc.want) {
				t.Errorf("refusal %q does not say %q", res.Content, tc.want)
			}
		})
	}

	if got := read(t, root, "a.go"); !strings.Contains(got, "func Beta()") {
		t.Errorf("a refused delete changed the file:\n%s", got)
	}
}

func writeFile(t *testing.T, root, name, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// A refusal that only says "not indexed" costs a turn to recover from. The
// candidates are already in the search the refusal was built from, and a run
// that guessed a test's name stalled for want of them.
func TestDeleteMissNamesWhatIsClose(t *testing.T) {
	t.Parallel()

	_, del := deleteProject(t)

	// "BetaHelper" is indexed nowhere; widening to "Beta" finds what is.
	res, err := del.Run(t.Context(), []byte(`{"symbol":"BetaHelper"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !res.IsError {
		t.Fatalf("want a refusal, got: %s", res.Content)
	}

	if !strings.Contains(res.Content, "Beta") || !strings.Contains(res.Content, "a.go") {
		t.Errorf("the refusal does not point at the near match: %s", res.Content)
	}
}

// Six test functions cover one deleted helper in this project's own tree, so
// a delete that takes one name at a time is six calls. `read` already takes
// several comma-separated paths, and this follows it.
func TestDeleteTakesSeveralSymbols(t *testing.T) {
	t.Parallel()

	root, del := deleteProject(t)

	res, err := del.Run(t.Context(), []byte(`{"symbol":"Alpha, Gamma"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.IsError {
		t.Fatalf("delete failed: %s", res.Content)
	}

	got := read(t, root, "a.go")
	for _, gone := range []string{"func Alpha()", "func Gamma()", "Alpha names", "Gamma names"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived:\n%s", gone, got)
		}
	}

	if !strings.Contains(got, "func Beta()") {
		t.Errorf("the symbol between them was taken too:\n%s", got)
	}
}

// A list that fails part way must say what it already did, or a caller that
// reruns the whole list is told the first names are not indexed.
func TestDeletePartialFailureSaysWhatItDid(t *testing.T) {
	t.Parallel()

	root, del := deleteProject(t)

	res, err := del.Run(t.Context(), []byte(`{"symbol":"Alpha, Nowhere"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !res.IsError {
		t.Fatalf("want a refusal, got: %s", res.Content)
	}

	if !strings.Contains(res.Content, "Alpha already deleted") {
		t.Errorf("the refusal does not say what it already did: %s", res.Content)
	}

	if strings.Contains(read(t, root, "a.go"), "func Alpha()") {
		t.Errorf("Alpha was reported deleted but survived")
	}
}

// A guessed name is usually a real one with something appended, so the
// lookup widens. Two ways of getting that wrong, both measured on `h4`:
// judging plausibility against the original name never trips (TestApplyToFile
// is nothing like ApplyToFileTest), and suggesting a near match from another
// package is worse than suggesting nothing, because a run that follows it has
// been sent away from the file it named.
func TestDeleteWideningStaysInTheNamedPath(t *testing.T) {
	t.Parallel()

	root, del := deleteProject(t)
	writeFile(t, root, "other.go", "package a\n\n// Zeta is in the other file.\nfunc Zeta() {}\n")

	res, err := del.Run(t.Context(), []byte(`{"symbol":"ZetaHelper","path":"a.go"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !res.IsError {
		t.Fatalf("want a refusal, got: %s", res.Content)
	}

	if strings.Contains(res.Content, "other.go") {
		t.Errorf("the refusal suggests a symbol outside the path it was given: %s", res.Content)
	}
}

// The exact-name case is the opposite: a caller who narrowed to the wrong
// file is told where the symbol really is, which is the whole point of
// saying anything at all.
func TestDeleteNamesTheRightFileForAnExactMatch(t *testing.T) {
	t.Parallel()

	root, del := deleteProject(t)
	writeFile(t, root, "other.go", "package a\n\n// Zeta is in the other file.\nfunc Zeta() {}\n")

	res, err := del.Run(t.Context(), []byte(`{"symbol":"Zeta","path":"a.go"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !res.IsError {
		t.Fatalf("want a refusal, got: %s", res.Content)
	}

	if !strings.Contains(res.Content, "other.go") {
		t.Errorf("the refusal does not say where Zeta actually is: %s", res.Content)
	}
}

// A Modifier makes removing a declaration one short call, so a misread task
// costs a build rather than a bad diff. Measured on `h4`: a run told to leave
// ApplyAllToFile alone deleted it, broke the build, and never recovered.
func TestDeleteRefusesWhatIsStillUsed(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not on PATH")
	}

	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	writeFile(t, root, "a.go", "package m\n\n// Alpha names the lane.\nfunc Alpha() string { return \"alpha\" }\n")
	writeFile(t, root, "b.go", "package m\n\n// Use calls it.\nfunc Use() string { return Alpha() }\n")

	store, err := codeintel.Open(t.Context(), filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("codeintel.Open: %v", err)
	}

	t.Cleanup(func() {
		if cerr := store.Close(); cerr != nil {
			t.Errorf("closing the store: %v", cerr)
		}
	})

	pool := lsp.NewPool(root)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), renameBudget)
		defer cancel()

		if cerr := pool.Close(ctx); cerr != nil {
			t.Errorf("closing the pool: %v", cerr)
		}
	})

	indexer := codeintel.NewIndexer(store, root, lang.NewDefaultRegistry())
	del := tools.NewDelete(root, indexer, pool, tools.NewScope(false))

	ctx, cancel := context.WithTimeout(t.Context(), renameBudget)
	defer cancel()

	res, err := del.Run(ctx, []byte(`{"symbol":"Alpha"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !res.IsError {
		t.Fatalf("a used declaration was deleted: %s", res.Content)
	}

	if !strings.Contains(res.Content, "b.go") {
		t.Errorf("the refusal does not name the use: %s", res.Content)
	}

	if !strings.Contains(read(t, root, "a.go"), "func Alpha()") {
		t.Errorf("the refused declaration was removed anyway")
	}

	// Removing the caller in the same call is the ordinary case and must work.
	res, err = del.Run(ctx, []byte(`{"symbol":"Use, Alpha"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.IsError {
		t.Fatalf("deleting a function and its only caller together was refused: %s", res.Content)
	}
}
