package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

func TestWrite_CreatesNewFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	w := tools.NewWrite(dir, nil)

	result, err := w.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "new.go", "content": "package foo\n",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError = true, want false: %q", result.Content)
	}

	got, err := os.ReadFile(filepath.Join(dir, "new.go")) //nolint:gosec // dir is a t.TempDir() fixture
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "package foo\n" {
		t.Errorf("file content = %q, want %q", got, "package foo\n")
	}
}

func TestWrite_RefusesExistingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "existing.go")
	if err := os.WriteFile(path, []byte("package foo\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w := tools.NewWrite(dir, nil)
	result, err := w.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "existing.go", "content": "package bar\n",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Recorded as a plain error, a refusal that worked reads as a defect:
	// the corpus counted write at 5 failures in 7 calls when all five were
	// this one and the path check below.
	if !result.IsError || result.Cause != tool.CauseRefused {
		t.Errorf("result = %+v, want a refusal for an existing file", result)
	}

	got, err := os.ReadFile(path) //nolint:gosec // dir is a t.TempDir() fixture
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "package foo\n" {
		t.Errorf("file was overwritten, content = %q", got)
	}
}

func TestWrite_RefusesPathOutsideRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	w := tools.NewWrite(dir, nil)

	result, err := w.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "../escape.go", "content": "x",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError || result.Cause != tool.CauseRefused {
		t.Errorf("result = %+v, want a refusal for a path outside the root", result)
	}
}

// A project may declare directories beyond its root, which is how a lane
// edits the template it was generated from while working in the render. A
// relative path still resolves against the project root, so an extra root is
// reached by naming it absolutely.
func TestWrite_ReachesADeclaredExtraRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sibling := t.TempDir()
	target := filepath.Join(sibling, "hk.pkl")

	refused := tools.NewWrite(root, nil)
	reaching := tools.NewWrite(root, nil, tools.WithExtraRoots([]string{sibling}))

	input := mustJSON(t, map[string]any{"path": target, "content": "amends\n"})

	result, err := refused.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError || result.Cause != tool.CauseRefused {
		t.Fatalf("result = %+v, want a refusal without the directory declared", result)
	}

	result, err = reaching.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("result = %+v, want the write to land in the declared directory", result)
	}

	if _, err := os.Stat(target); err != nil {
		t.Errorf("Stat: %v, want the file written", err)
	}
}
