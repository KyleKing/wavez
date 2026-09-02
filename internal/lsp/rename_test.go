package lsp_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/lsp"
)

// TestRenameFollowsTheSymbolAcrossPackages runs against real gopls, because
// what is worth testing here is not the JSON: it is that the rename follows
// the type information into another package and leaves an unrelated
// identifier of the same name alone. A scripted server would prove neither.
func TestRenameFollowsTheSymbolAcrossPackages(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not on PATH")
	}

	root := t.TempDir()
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.26\n")
	write(t, root, "a/a.go", "package a\n\n// Alpha names the lane.\nfunc Alpha() string { return \"alpha\" }\n")
	write(t, root, "b/b.go", "package b\n\nimport \"example.com/m/a\"\n\n"+
		"func Use() string { return a.Alpha() }\n")
	// A local function of the same name in a third package must not move.
	write(t, root, "c/c.go", "package c\n\nfunc Alpha() string { return \"unrelated\" }\n")

	pool := lsp.NewPool(root)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), renameBudget)
		defer cancel()

		if err := pool.Close(ctx); err != nil {
			t.Errorf("closing pool: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), renameBudget)
	defer cancel()

	client, err := pool.Client(ctx, filepath.Join(root, "a", "a.go"))
	if err != nil {
		t.Fatalf("starting gopls: %v", err)
	}

	if _, err := client.Sync(ctx, filepath.Join(root, "a", "a.go")); err != nil {
		t.Fatalf("syncing: %v", err)
	}

	// "func Alpha" on line 4 (zero-based 3), column 5 is the identifier.
	edits, err := client.Rename(ctx, filepath.Join(root, "a", "a.go"), 3, 5, "Beta")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}

	touched := map[string]int{}
	for path, list := range edits {
		touched[filepath.Base(filepath.Dir(path))] = len(list)
	}

	if touched["a"] == 0 {
		t.Errorf("the declaration was not renamed, got %v", touched)
	}

	if touched["b"] == 0 {
		t.Errorf("the use in another package was not renamed, got %v", touched)
	}

	if touched["c"] != 0 {
		t.Errorf("an unrelated function of the same name was renamed, got %v", touched)
	}
}

const renameBudget = 60 * time.Second

func write(t *testing.T, root, name, body string) {
	t.Helper()

	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// TestRenameFollowsThePythonSymbolAcrossModules is the ty counterpart of the
// gopls case above, and answers the same question for the language wavez
// speaks second: whether a rename follows an import into another module and
// leaves an unrelated function of the same name where it is.
func TestRenameFollowsThePythonSymbolAcrossModules(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("ty"); err != nil {
		t.Skip("ty is not on PATH")
	}

	root := t.TempDir()
	write(t, root, "pkg/__init__.py", "")
	write(t, root, "pkg/a.py", "def alpha() -> str:\n    return \"alpha\"\n")
	write(t, root, "pkg/b.py", "from pkg.a import alpha\n\n\ndef use() -> str:\n    return alpha()\n")
	write(t, root, "pkg/c.py", "def alpha() -> str:\n    return \"unrelated\"\n")

	pool := lsp.NewPool(root)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), renameBudget)
		defer cancel()

		if err := pool.Close(ctx); err != nil {
			t.Errorf("closing pool: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), renameBudget)
	defer cancel()

	decl := filepath.Join(root, "pkg", "a.py")

	client, err := pool.Client(ctx, decl)
	if err != nil {
		t.Fatalf("starting ty: %v", err)
	}

	if _, err := client.Sync(ctx, decl); err != nil {
		t.Fatalf("syncing: %v", err)
	}

	// "def alpha" on line 1 (zero-based 0), column 4 is the identifier.
	edits, err := client.Rename(ctx, decl, 0, 4, "beta")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}

	touched := map[string]int{}
	for path, list := range edits {
		touched[filepath.Base(path)] = len(list)
	}

	if touched["a.py"] == 0 {
		t.Errorf("the declaration was not renamed, got %v", touched)
	}

	if touched["b.py"] == 0 {
		t.Errorf("the use in another module was not renamed, got %v", touched)
	}

	if touched["c.py"] != 0 {
		t.Errorf("an unrelated function of the same name was renamed, got %v", touched)
	}
}
