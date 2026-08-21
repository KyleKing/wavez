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

// A repeat read returns the content again, whole file or range. Answering
// with a reference sent the model to the shell for the same file four times
// out of four on a dogfood run; keeping the repeat out of the history is
// compaction's DedupeToolReads, which the model never sees.
func TestRead_RepeatReadReturnsTheContentAgain(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	if err := os.WriteFile(path, []byte("package foo\nfunc A() {}\nfunc B() {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := tools.NewRead(dir, nil)
	ctx := context.Background()

	read := func(args map[string]any) string {
		t.Helper()
		res, err := r.Run(ctx, mustJSON(t, args))
		if err != nil {
			t.Fatalf("Run(%v): %v", args, err)
		}

		return res.Content
	}

	whole := map[string]any{"path": "file.go"}
	if first, second := read(whole), read(whole); first != second {
		t.Errorf("second whole-file read = %q, want the same content as the first %q", second, first)
	}

	ranged := map[string]any{"path": "file.go", "start_line": 2, "end_line": 3}
	if first, second := read(ranged), read(ranged); first != second || !strings.Contains(second, "func A() {}") {
		t.Errorf("second ranged read = %q, want the same content as the first %q", second, first)
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
	if !strings.Contains(result.Content, "2\ttwo\n3\tthree") {
		t.Errorf("Content = %q, want lines two and three carrying their file line numbers", result.Content)
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

// A model that copies a numbered read back into an anchor or a new file
// would otherwise get "not found" with no clue why, or a file holding the
// numbers.
func TestNumberedTextIsRefusedAsFileContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.go")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sr := tools.NewStrReplace(dir, nil)
	result, err := sr.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "file.go", "old_string": "2\ttwo", "new_string": "TWO",
	}))
	if err != nil {
		t.Fatalf("str_replace Run: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "still carries the line numbers") {
		t.Errorf("str_replace Content = %q (IsError=%v), want the line-number hint", result.Content, result.IsError)
	}

	w := tools.NewWrite(dir, nil)
	result, err = w.Run(context.Background(), mustJSON(t, map[string]any{
		"path": "new.go", "content": "1\tpackage x\n2\t\n3\tfunc A() {}\n",
	}))
	if err != nil {
		t.Fatalf("write Run: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "carries the line numbers") {
		t.Errorf("write Content = %q (IsError=%v), want the line-number refusal", result.Content, result.IsError)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "new.go")); statErr == nil {
		t.Error("write created the file despite refusing the content")
	}
}
