package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/codeintel/lang"
	"github.com/kyleking/wavez/internal/lsp"
	"github.com/kyleking/wavez/internal/tools"
)

// renameProject writes a three-package module and indexes it, returning the
// root and the tool under test. Real gopls serves it on purpose: the point of
// the tool is that the server resolves the symbol through type information,
// which no scripted server can stand in for.
func renameProject(t *testing.T) (string, *tools.Rename) {
	t.Helper()

	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not on PATH")
	}

	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.26\n",
		"a/a.go": "package a\n\n// Alpha names the lane.\nfunc Alpha() string { return \"alpha\" }\n",
		"b/b.go": "package b\n\nimport \"example.com/m/a\"\n\nfunc Use() string { return a.Alpha() }\n",
		"c/c.go": "package c\n\nfunc Alpha() string { return \"unrelated\" }\n",
	}

	for rel, src := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}

		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

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

	pool := lsp.NewPool(root)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), renameBudget)
		defer cancel()

		if cerr := pool.Close(ctx); cerr != nil {
			t.Errorf("closing the pool: %v", cerr)
		}
	})

	return root, tools.NewRename(root, indexer, pool, tools.NewScope(false))
}

func TestRenameRewritesEveryPackageThatUsesTheSymbol(t *testing.T) {
	t.Parallel()

	root, rn := renameProject(t)

	ctx, cancel := context.WithTimeout(t.Context(), renameBudget)
	defer cancel()

	res, err := rn.Run(ctx, []byte(`{"symbol":"Alpha","to":"Beta","path":"a/a.go"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.IsError {
		t.Fatalf("rename failed: %s", res.Content)
	}

	if got := read(t, root, "a/a.go"); !strings.Contains(got, "func Beta()") {
		t.Errorf("the declaration was not renamed:\n%s", got)
	}

	if got := read(t, root, "b/b.go"); !strings.Contains(got, "a.Beta()") {
		t.Errorf("the use in another package was not renamed:\n%s", got)
	}

	// The whole reason to go through the server rather than through text.
	if got := read(t, root, "c/c.go"); !strings.Contains(got, "func Alpha()") {
		t.Errorf("an unrelated function of the same name was renamed:\n%s", got)
	}

	if len(res.Changes) != 2 {
		t.Errorf("reported %d changed file(s), want 2: %s", len(res.Changes), res.Content)
	}
}

func TestRenameRefusalsNameWhatToDoNext(t *testing.T) {
	t.Parallel()

	_, rn := renameProject(t)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "same name", input: `{"symbol":"Alpha","to":"Alpha"}`, want: "same name"},
		{name: "not an identifier", input: `{"symbol":"Alpha","to":"Beta Gamma"}`, want: "not a valid identifier"},
		{name: "missing target", input: `{"symbol":"Alpha"}`, want: "to is required"},
		{name: "unknown symbol", input: `{"symbol":"Nowhere","to":"Beta"}`, want: "Nowhere"},
		// Two packages declare Alpha, and picking one would be a change the
		// caller then has to find and undo.
		{name: "ambiguous", input: `{"symbol":"Alpha","to":"Beta"}`, want: "declares Alpha"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(t.Context(), renameBudget)
			defer cancel()

			res, err := rn.Run(ctx, []byte(tc.input))
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
}

func read(t *testing.T, root, rel string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // a fixture path under t.TempDir()
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}

	return string(body)
}

const renameBudget = 60 * time.Second

// A model narrowing a rename writes the package directory, not the file, and
// refusing that costs a turn to discover. Measured: the first dogfood run of
// this tool sent path "internal/bench" and was told nothing is indexed there.
func TestRenameAcceptsADirectoryAsPath(t *testing.T) {
	t.Parallel()

	root, rn := renameProject(t)

	ctx, cancel := context.WithTimeout(t.Context(), renameBudget)
	defer cancel()

	res, err := rn.Run(ctx, []byte(`{"symbol":"Alpha","to":"Beta","path":"a"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.IsError {
		t.Fatalf("a package directory was refused: %s", res.Content)
	}

	if got := read(t, root, "a/a.go"); !strings.Contains(got, "func Beta()") {
		t.Errorf("the declaration was not renamed:\n%s", got)
	}
}

// A path that narrows to the wrong place must say where the symbol is, so
// the caller corrects in one turn rather than searching again.
func TestRenameMissNamesWhereTheSymbolIs(t *testing.T) {
	t.Parallel()

	_, rn := renameProject(t)

	ctx, cancel := context.WithTimeout(t.Context(), renameBudget)
	defer cancel()

	res, err := rn.Run(ctx, []byte(`{"symbol":"Alpha","to":"Beta","path":"nowhere"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !res.IsError {
		t.Fatalf("want a refusal, got: %s", res.Content)
	}

	if !strings.Contains(res.Content, "a/a.go") || !strings.Contains(res.Content, "c/c.go") {
		t.Errorf("the refusal does not name where Alpha is declared: %s", res.Content)
	}
}
