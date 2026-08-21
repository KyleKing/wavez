package tools_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/codeintel/lang"
	"github.com/kyleking/wavez/internal/tools"
)

// deleteProject indexes one file holding three declarations, so a deletion
// has neighbors on both sides to leave alone.
func deleteProject(t *testing.T) (string, *tools.Delete) {
	t.Helper()

	const src = `package a

// Alpha names the first lane.
func Alpha() string { return "alpha" }

// Beta names the second lane.
// It has two comment lines.
func Beta() string { return "beta" }

// Gamma names the third lane.
func Gamma() string { return "gamma" }
`

	root := t.TempDir()
	writeFile(t, root, "a.go", src)

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

	return root, tools.NewDelete(root, indexer, tools.NewScope(false))
}

func TestDeleteTakesTheDocCommentWithIt(t *testing.T) {
	t.Parallel()

	root, del := deleteProject(t)

	res, err := del.Run(t.Context(), []byte(`{"symbol":"Beta"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.IsError {
		t.Fatalf("delete failed: %s", res.Content)
	}

	got := read(t, root, "a.go")

	for _, gone := range []string{"func Beta()", "Beta names the second lane", "It has two comment lines"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived the deletion:\n%s", gone, got)
		}
	}

	// The neighbors on both sides, and their comments, are the point.
	kept := []string{
		"Alpha names the first lane", "func Alpha()",
		"Gamma names the third lane", "func Gamma()",
	}
	for _, kept := range kept {
		if !strings.Contains(got, kept) {
			t.Errorf("%q was taken with it:\n%s", kept, got)
		}
	}

	if strings.Contains(got, "\n\n\n") {
		t.Errorf("the deletion left a double blank line:\n%s", got)
	}

	if len(res.Changes) != 1 || res.Changes[0].Path != "a.go" {
		t.Errorf("reported changes %v, want one for a.go", res.Changes)
	}
}

func TestDeleteRefusalsNameWhatToDoNext(t *testing.T) {
	t.Parallel()

	root, del := deleteProject(t)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "missing symbol", input: `{}`, want: "symbol is required"},
		{name: "unknown symbol", input: `{"symbol":"Nowhere"}`, want: "Nowhere"},
		{name: "narrowed to the wrong place", input: `{"symbol":"Beta","path":"nowhere"}`, want: "a.go"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := del.Run(t.Context(), []byte(tc.input))
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

	if got := read(t, root, "a.go"); !strings.Contains(got, "func Beta()") {
		t.Errorf("a refused delete changed the file:\n%s", got)
	}
}

func writeFile(t *testing.T, root, name, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}
