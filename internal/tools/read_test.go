package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/tools"
)

func TestRead_CacheReturnsReferenceThenFullContentAfterChange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	if err := os.WriteFile(path, []byte("package foo\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := tools.NewRead(dir, nil)
	ctx := context.Background()

	first, err := r.Run(ctx, mustJSON(t, map[string]any{"path": "file.go"}))
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if !strings.Contains(first.Content, "package foo") {
		t.Errorf("first read content = %q, want file content", first.Content)
	}

	second, err := r.Run(ctx, mustJSON(t, map[string]any{"path": "file.go"}))
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if strings.Contains(second.Content, "package foo") {
		t.Errorf("second read of unchanged file returned content, want a short reference: %q", second.Content)
	}
	if !strings.Contains(second.Content, "unchanged") {
		t.Errorf("second read content = %q, want it to say unchanged", second.Content)
	}

	if err := os.WriteFile(path, []byte("package bar\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	third, err := r.Run(ctx, mustJSON(t, map[string]any{"path": "file.go"}))
	if err != nil {
		t.Fatalf("third Run: %v", err)
	}
	if !strings.Contains(third.Content, "package bar") {
		t.Errorf("read after change content = %q, want new file content", third.Content)
	}
}

func TestRead_RefusesPathOutsideRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	r := tools.NewRead(dir, nil)

	result, err := r.Run(context.Background(), mustJSON(t, map[string]any{"path": "../outside.go"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.IsError {
		t.Errorf("IsError = false, want true for a path outside the root")
	}
	if !strings.Contains(result.Content, "outside the project root") {
		t.Errorf("Content = %q, want it to mention the project root", result.Content)
	}
}

func TestRead_LineRange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := tools.NewRead(dir, nil)
	result, err := r.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "file.go", "start_line": 2, "end_line": 3,
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError = true, want false: %q", result.Content)
	}
	if !strings.Contains(result.Content, "two") || !strings.Contains(result.Content, "three") {
		t.Errorf("Content = %q, want lines two and three", result.Content)
	}
	if strings.Contains(result.Content, "one") || strings.Contains(result.Content, "four") {
		t.Errorf("Content = %q, want lines outside the range excluded", result.Content)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	return data
}

// The model routinely sends start_line alone. Rejecting that cost a whole turn.
func TestRead_OmittedEndLineReadsToEndOfFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	res, err := tools.NewRead(root, nil).Run(t.Context(), json.RawMessage(`{"path":"a.txt","start_line":3}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("omitting end_line was rejected: %s", res.Content)
	}
	if !strings.Contains(res.Content, "three") || !strings.Contains(res.Content, "four") {
		t.Fatalf("did not read to end of file: %s", res.Content)
	}
	if strings.Contains(res.Content, "two") {
		t.Fatalf("read started before start_line: %s", res.Content)
	}
}
