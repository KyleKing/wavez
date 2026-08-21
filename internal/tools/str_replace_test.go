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

	s := tools.NewStrReplace(dir, nil)
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

	s := tools.NewStrReplace(dir, nil)
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
	s := tools.NewStrReplace(dir, nil)

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

// A batch of edits with no path is a missing field, and reporting it as a
// containment failure sent one run looking for a path problem it did not
// have.
func TestStrReplace_NamesAMissingPath(t *testing.T) {
	t.Parallel()

	s := tools.NewStrReplace(t.TempDir(), nil)

	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"edits": []map[string]string{{"old_string": "a", "new_string": "b"}},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "path is required") {
		t.Errorf("result = %+v, want an error naming the missing path", result)
	}
}

// One call per replacement costs a turn each, and the turns are what a run
// spends its budget on.
func TestStrReplace_AppliesSeveralEditsInOneCall(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	if err := os.WriteFile(path, []byte("package p\n\nconst A = 1\nconst B = 2\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := tools.NewStrReplace(dir, nil)
	result, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "f.go",
		"edits": []map[string]string{
			{"old_string": "const A = 1", "new_string": "const A = 10"},
			{"old_string": "const B = 2", "new_string": "const B = 20"},
		},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError = true: %q", result.Content)
	}
	if !strings.Contains(result.Content, "across 2 edits") {
		t.Errorf("Content = %q, want it to name the batch", result.Content)
	}

	data, err := os.ReadFile(path) //nolint:gosec // dir is a t.TempDir() fixture
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := string(data); !strings.Contains(got, "A = 10") || !strings.Contains(got, "B = 20") {
		t.Errorf("file = %q, want both edits applied", got)
	}

	both, err := s.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "f.go", "old_string": "x", "new_string": "y",
		"edits": []map[string]string{{"old_string": "a", "new_string": "b"}},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !both.IsError || !strings.Contains(both.Content, "not both") {
		t.Errorf("Content = %q (IsError=%v), want the two shapes refused together", both.Content, both.IsError)
	}
}
