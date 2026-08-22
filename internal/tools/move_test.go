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

// moveProject indexes one package of two files, so a move has somewhere to
// come from and somewhere to land.
func moveProject(t *testing.T) (string, *tools.Move) {
	t.Helper()

	root := t.TempDir()
	writeFile(t, root, "a.go", `package a

// Alpha names the first lane.
func Alpha() string { return "alpha" }

// Beta names the second lane.
func Beta() string { return "beta" }
`)
	writeFile(t, root, "keep.go", "package a\n\n// Kept stays put.\nfunc Kept() {}\n")
	if err := os.MkdirAll(filepath.Join(root, "other"), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	writeFile(t, root, "other/other.go", "package other\n\n// Far is elsewhere.\nfunc Far() {}\n")

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

	return root, tools.NewMove(root, indexer, tools.NewScope(false))
}

// Splitting a file is otherwise a block rewrite at both ends, and block
// rewrites are 35% of this project's logged edits and half of their bytes.
// A move carries names and a destination, so no source crosses the wire.
func TestMoveCarriesTheDeclarationAndItsComment(t *testing.T) {
	t.Parallel()

	root, mv := moveProject(t)

	res, err := mv.Run(t.Context(), []byte(`{"symbol":"Beta","to":"b.go"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.IsError {
		t.Fatalf("move failed: %s", res.Content)
	}

	src, dest := read(t, root, "a.go"), read(t, root, "b.go")

	for _, gone := range []string{"func Beta()", "Beta names the second lane"} {
		if strings.Contains(src, gone) {
			t.Errorf("%q stayed behind:\n%s", gone, src)
		}

		if !strings.Contains(dest, gone) {
			t.Errorf("%q never arrived:\n%s", gone, dest)
		}
	}

	if !strings.HasPrefix(dest, "package a\n") {
		t.Errorf("the new file joined no package:\n%s", dest)
	}

	if !strings.Contains(src, "func Alpha()") {
		t.Errorf("the neighbor went with it:\n%s", src)
	}

	if len(res.Changes) != 2 {
		t.Errorf("reported %d change(s), want one per end of the move: %v", len(res.Changes), res.Changes)
	}
}

func TestMoveRefusalsNameWhatToDoNext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "another package is a different operation",
			input: `{"symbol":"Beta","to":"other/other.go"}`,
			want:  "package other",
		},
		{
			name:  "a declaration already there",
			input: `{"symbol":"Kept","to":"keep.go"}`,
			want:  "already in keep.go",
		},
		{
			name:  "outside the project",
			input: `{"symbol":"Beta","to":"../escape.go"}`,
			want:  "stay inside the project",
		},
		{
			name:  "a name nothing declares",
			input: `{"symbol":"Nowhere","to":"b.go"}`,
			want:  "Nowhere",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, mv := moveProject(t)

			res, err := mv.Run(t.Context(), []byte(tt.input))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if !res.IsError {
				t.Fatalf("the move was allowed: %s", res.Content)
			}

			if !strings.Contains(res.Content, tt.want) {
				t.Errorf("refusal = %q, want it to name %q", res.Content, tt.want)
			}
		})
	}
}

// A file split is several declarations at once, so one call takes them all
// and says which ones landed if a later one cannot.
func TestMoveTakesSeveralSymbols(t *testing.T) {
	t.Parallel()

	root, mv := moveProject(t)

	res, err := mv.Run(t.Context(), []byte(`{"symbol":"Alpha, Beta","to":"b.go"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.IsError {
		t.Fatalf("move failed: %s", res.Content)
	}

	src, dest := read(t, root, "a.go"), read(t, root, "b.go")

	for _, moved := range []string{"func Alpha()", "func Beta()"} {
		if strings.Contains(src, moved) {
			t.Errorf("%q stayed behind:\n%s", moved, src)
		}

		if !strings.Contains(dest, moved) {
			t.Errorf("%q never arrived:\n%s", moved, dest)
		}
	}

	if strings.Contains(dest, "\n\n\n") {
		t.Errorf("the arrivals left a double blank line:\n%s", dest)
	}
}

// A move that cut and appended per symbol left the tree in a state where a
// declaration was in neither file, and anything reading the tree in that
// window sees a package that does not build. Measured on `h5`: one correct
// move was followed by a gate build failure the gate could not attribute and
// fourteen turns of the model hunting a defect that was no longer there.
// Each file this call writes, it writes once.
func TestMoveWritesEachFileOnce(t *testing.T) {
	t.Parallel()

	root, mv := moveProject(t)

	res, err := mv.Run(t.Context(), []byte(`{"symbol":"Alpha, Beta","to":"b.go"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.IsError {
		t.Fatalf("move failed: %s", res.Content)
	}

	seen := make(map[string]int, len(res.Changes))
	for _, c := range res.Changes {
		seen[c.Path]++
	}

	for path, writes := range seen {
		if writes != 1 {
			t.Errorf("%s was written %d times in one call, want once", path, writes)
		}
	}

	if len(seen) != 2 {
		t.Errorf("the call touched %d file(s), want the source and the destination: %v", len(seen), seen)
	}

	// The source has to be readable Go after one write, not after two.
	if src := read(t, root, "a.go"); strings.Contains(src, "\n\n\n") {
		t.Errorf("cutting both declarations at once left a double blank line:\n%s", src)
	}
}
