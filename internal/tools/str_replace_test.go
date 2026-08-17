package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/tools"
)

func TestStrReplace_Success(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	if err := os.WriteFile(path, []byte("package foo\n\nfunc a() {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir)
	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "file.go", "old_string": "func a() {}", "new_string": "func b() {}",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError = true, want false: %q", result.Content)
	}
	if strings.Contains(result.Content, "func b") {
		t.Errorf("Content = %q, want terse line-count summary, not the diff", result.Content)
	}
	if !strings.Contains(result.Content, "+1") || !strings.Contains(result.Content, "-1") {
		t.Errorf("Content = %q, want it to report +1 -1 lines", result.Content)
	}
	if len(result.Changes) != 1 || result.Changes[0].Added != 1 || result.Changes[0].Removed != 1 {
		t.Errorf("Changes = %+v, want one change with Added=1 Removed=1", result.Changes)
	}
}

func TestStrReplace_NonUniqueErrorCarriesLineNumbers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	content := "a := 1\nb := a + 1\na := 1\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir)
	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "file.go", "old_string": "a := 1", "new_string": "a := 2",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError {
		t.Fatalf("IsError = false, want true for a non-unique match")
	}
	if !strings.Contains(result.Content, "2 matches") {
		t.Errorf("Content = %q, want it to report 2 matches", result.Content)
	}
	if !strings.Contains(result.Content, "[1 3]") {
		t.Errorf("Content = %q, want it to name lines 1 and 3", result.Content)
	}
}

func TestStrReplace_RefusesPathOutsideRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s := tools.NewStrReplace(dir)

	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "/etc/passwd", "old_string": "a", "new_string": "b",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError {
		t.Errorf("IsError = false, want true for a path outside the root")
	}
}
