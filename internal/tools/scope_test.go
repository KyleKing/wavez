package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/tools"
)

// scopeFixture is one run's world: a root, the file under it, and the Scope
// that run holds.
type scopeFixture struct {
	scope *tools.Scope
	root  string
	path  string
}

// newScopeFixture writes one editable file under a fresh root, having read
// it first when readFirst.
func newScopeFixture(t *testing.T, strict, readFirst bool) scopeFixture {
	t.Helper()

	root := t.TempDir()
	path := filepath.Join(root, "greet.go")

	if err := os.WriteFile(path, []byte("package greet\n\nconst Greeting = \"hi\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	scope := tools.NewScope(strict)
	if readFirst {
		read := tools.NewRead(root, scope)
		if _, err := read.Run(context.Background(), mustJSON(t, map[string]any{"path": "greet.go"})); err != nil {
			t.Fatalf("read: %v", err)
		}
	}

	return scopeFixture{root: root, path: path, scope: scope}
}

func TestScopeTracksEditsAgainstWhatARunOpened(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		strict      bool
		readFirst   bool
		wantIsError bool
		wantStrayed bool
	}{
		{name: "an edit to a file the run read is in scope", readFirst: true},
		{name: "an edit to a file the run never read is recorded and allowed", wantStrayed: true},
		{name: "strict refuses the same edit", strict: true, wantIsError: true, wantStrayed: true},
		{name: "strict allows an edit to a file the run read", strict: true, readFirst: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fx := newScopeFixture(t, tt.strict, tt.readFirst)

			replace := tools.NewStrReplace(fx.root, fx.scope)
			result, err := replace.Run(context.Background(), mustJSON(t, map[string]any{
				"path": "greet.go", "old_string": `"hi"`, "new_string": `"hello"`,
			}))
			if err != nil {
				t.Fatalf("str_replace: %v", err)
			}

			if result.IsError != tt.wantIsError {
				t.Errorf("IsError = %v, want %v (content=%q)", result.IsError, tt.wantIsError, result.Content)
			}

			if got := len(fx.scope.Strayed()) > 0; got != tt.wantStrayed {
				t.Errorf("strayed = %v, want %v (%v)", got, tt.wantStrayed, fx.scope.Strayed())
			}

			after, err := os.ReadFile(fx.path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}

			if edited := strings.Contains(string(after), "hello"); edited == tt.wantIsError {
				t.Errorf("file edited = %v, want %v", edited, !tt.wantIsError)
			}
		})
	}
}

func TestScopeBringsAFileItCreatedIntoScope(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scope := tools.NewScope(true)
	ctx := context.Background()

	write := tools.NewWrite(root, scope)
	if _, err := write.Run(ctx, mustJSON(t, map[string]any{"path": "new.go", "content": "package new\n"})); err != nil {
		t.Fatalf("write: %v", err)
	}

	replace := tools.NewStrReplace(root, scope)
	result, err := replace.Run(ctx, mustJSON(t, map[string]any{
		"path": "new.go", "old_string": "package new", "new_string": "package fresh",
	}))
	if err != nil {
		t.Fatalf("str_replace: %v", err)
	}

	if result.IsError {
		t.Errorf("editing a file this run created was refused: %s", result.Content)
	}

	if strayed := scope.Strayed(); len(strayed) != 0 {
		t.Errorf("Strayed() = %v, want none", strayed)
	}
}

// A model writes its next anchor from the file it read, and after its own
// edit that file no longer exists anywhere. Measured on `e2`: a run that
// had just edited a file spent its remaining turns guessing anchors against
// the version it remembered, and the tool could only say "not found".
func TestScopeTellsAStaleAnchorFromAWrongOne(t *testing.T) {
	t.Parallel()

	s := tools.NewScope(false)
	const path = "/repo/a.go"

	if s.Stale(path) {
		t.Error("a file nobody touched reads as stale")
	}

	s.Observe(path)

	if s.Stale(path) {
		t.Error("a file that was only read reads as stale")
	}

	s.Wrote(path)

	if !s.Stale(path) {
		t.Error("a file written after its last read does not read as stale")
	}

	s.Observe(path)

	if s.Stale(path) {
		t.Error("reading the file again did not clear it")
	}
}
