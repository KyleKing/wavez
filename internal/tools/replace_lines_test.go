package tools_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/tools"
)

func readBack(t *testing.T, root, name string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(root, name)) //nolint:gosec // this test's own temp file
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}

	return string(body)
}

const linesSource = "package sysinfo\n\nfunc Free() uint64 {\n\treturn 1\n}\n"

type replaceLinesCase struct {
	name      string
	mutate    string
	want      string
	wantErr   string
	from      int
	to        int
	readFirst bool
}

// The anchor `str_replace` needs is a copy of text the file already holds and
// the run has already been sent. Two integers say the same thing, and the hash
// of what `read` returned is what proves the caller is looking at the file as
// it now stands.
func TestReplaceLinesEditsWhatTheRunWasShown(t *testing.T) {
	t.Parallel()

	tests := []replaceLinesCase{
		{
			name: "replaces a range the run read", readFirst: true, from: 3, to: 5,
			want: "func Free() uint64 { return 2 }\n",
		},
		{
			name: "refuses a file the run never read", from: 3, to: 5,
			wantErr: "not the file you were shown",
		},
		{
			name: "refuses a file that moved since the read", readFirst: true, from: 3, to: 5,
			mutate:  "package sysinfo\n\nvar x = 1\n\nfunc Free() uint64 {\n\treturn 1\n}\n",
			wantErr: "not the file you were shown",
		},
		{
			name: "refuses a range past the end", readFirst: true, from: 3, to: 40,
			wantErr: "has 5 lines and to_line is 40",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runReplaceLinesCase(t, tt)
		})
	}
}

func runReplaceLinesCase(t *testing.T, tt replaceLinesCase) {
	t.Helper()

	root := t.TempDir()
	writeFile(t, root, "memory.go", linesSource)

	scope := tools.NewScope(root, false)
	seen := tools.WithSeen(tools.NewSeenFiles())

	if tt.readFirst {
		if _, err := tools.NewRead(root, scope, seen).Run(t.Context(),
			mustJSON(t, map[string]any{"path": "memory.go", "start_line": 1, "end_line": 5})); err != nil {
			t.Fatalf("read: %v", err)
		}
	}

	if tt.mutate != "" {
		writeFile(t, root, "memory.go", tt.mutate)
	}

	res, err := tools.NewReplaceLines(root, scope, seen).Run(t.Context(), mustJSON(t, map[string]any{
		"path": "memory.go", "from_line": tt.from, "to_line": tt.to,
		"new_string": "func Free() uint64 { return 2 }",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if tt.wantErr != "" {
		if !res.IsError || !strings.Contains(res.Content, tt.wantErr) {
			t.Fatalf("want a failure saying %q, got IsError=%v %q", tt.wantErr, res.IsError, res.Content)
		}

		return
	}

	if res.IsError {
		t.Fatalf("replace_lines failed: %s", res.Content)
	}

	assertText(t, readBack(t, root, "memory.go"), []string{tt.want}, nil)

	// A range off by ten lines is otherwise invisible until a gate.
	if !strings.Contains(res.Content, "return 1") {
		t.Errorf("result does not name the lines it took: %q", res.Content)
	}
}

// A second edit through the same tool must not need a fresh read, or the
// saving is spent on the turn it takes to get one.
func TestReplaceLinesKeepsWritingAfterItsOwnEdit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "memory.go", linesSource)

	scope := tools.NewScope(root, false)
	seen := tools.WithSeen(tools.NewSeenFiles())
	rl := tools.NewReplaceLines(root, scope, seen)

	if _, err := tools.NewRead(root, scope, seen).Run(t.Context(),
		mustJSON(t, map[string]any{"path": "memory.go"})); err != nil {
		t.Fatalf("read: %v", err)
	}

	call := func(from, to int, text string) json.RawMessage {
		return mustJSON(t, map[string]any{
			"path": "memory.go", "from_line": from, "to_line": to, "new_string": text,
		})
	}

	if res, err := rl.Run(t.Context(), call(4, 4, "\treturn 2")); err != nil || res.IsError {
		t.Fatalf("first edit: %v %q", err, res.Content)
	}

	res, err := rl.Run(t.Context(), call(1, 1, "package sys"))
	if err != nil || res.IsError {
		t.Fatalf("second edit without a fresh read: %v %q", err, res.Content)
	}
}
