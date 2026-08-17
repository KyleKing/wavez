package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/tools"
)

func TestWrite_CreatesNewFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	w := tools.NewWrite(dir)

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

	w := tools.NewWrite(dir)
	result, err := w.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "existing.go", "content": "package bar\n",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError {
		t.Errorf("IsError = false, want true for an existing file")
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
	w := tools.NewWrite(dir)

	result, err := w.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "../escape.go", "content": "x",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError {
		t.Errorf("IsError = false, want true for a path outside the root")
	}
}
