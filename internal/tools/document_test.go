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

const pySource = `class Engine:
    def render(self, path):
        return path.read_text()

    def relativize(
        self,
        pattern,
        base,
    ):
        """Old text."""
        return pattern


def load(path):
    """One line that goes away."""
    return path
`

// assertText keeps the table's two lists off the subtest's own complexity.
func assertText(t *testing.T, body string, want, unwant []string) {
	t.Helper()

	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("missing:\n%s\ngot:\n%s", w, body)
		}
	}

	for _, u := range unwant {
		if strings.Contains(body, u) {
			t.Errorf("kept %q:\n%s", u, body)
		}
	}
}

func documentProject(t *testing.T, name, source string) (string, *tools.Document) {
	t.Helper()

	root := t.TempDir()
	writeFile(t, root, name, source)

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

	return root, tools.NewDocument(root, indexer, tools.NewScope(root, false))
}

// A docstring is the body's first statement, so the tool has to find where the
// body starts rather than where the declaration does, and a signature that
// wraps across lines is the case that separates the two.
func TestDocumentWritesPythonDocstrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		symbol string
		doc    string
		want   []string
		unwant []string
	}{
		{
			name:   "inserts where there is none",
			symbol: "render",
			doc:    `"""# Read the file at path.` + "\n" + `"""`,
			want: []string{
				"    def render(self, path):\n" +
					`        """Read the file at path."""` + "\n" +
					"        return path.read_text()\n",
			},
		},
		{
			name:   "replaces one that wraps its signature",
			symbol: "relativize",
			doc:    "Make pattern relative to base.",
			want: []string{
				"    ):\n" + `        """Make pattern relative to base."""` + "\n        return pattern\n",
			},
			unwant: []string{"Old text."},
		},
		{
			name:   "writes several lines at the declaration's indent",
			symbol: "load",
			doc:    "Load a config.\n\nRaises OSError when path is unreadable.",
			want: []string{
				`    """Load a config.` + "\n\n    Raises OSError when path is unreadable.\n" + `    """` + "\n",
			},
			unwant: []string{"One line that goes away."},
		},
		{
			name:   "strips markers the model wrote out of habit",
			symbol: "render",
			doc:    `"""# Read the file at path.` + "\n" + `"""`,
			want: []string{
				`        """Read the file at path."""` + "\n        return path.read_text()\n",
			},
			unwant: []string{`""""`, "# Read"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root, d := documentProject(t, "engine.py", pySource)

			res, err := d.Run(t.Context(), mustJSON(t, map[string]any{"symbol": tt.symbol, "doc": tt.doc}))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if res.IsError {
				t.Fatalf("document failed: %s", res.Content)
			}

			body, rerr := os.ReadFile(filepath.Join(root, "engine.py")) //nolint:gosec // this test's own temp file
			if rerr != nil {
				t.Fatalf("reading back: %v", rerr)
			}

			assertText(t, string(body), tt.want, tt.unwant)
		})
	}
}

// Writing Python's shape into Go would be a syntax error rather than a wrong
// comment, so the language decides the placement.
func TestDocumentWritesGoCommentsAboveTheDeclaration(t *testing.T) {
	t.Parallel()

	root, d := documentProject(t, "memory.go", "package sysinfo\n\n// Old.\nfunc Free() uint64 { return 1 }\n")

	if _, err := d.Run(t.Context(), mustJSON(t, map[string]any{
		"symbol": "Free", "doc": "Free is what is left, in bytes.",
	})); err != nil {
		t.Fatalf("Run: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(root, "memory.go")) //nolint:gosec // this test's own temp file
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}

	want := "// Free is what is left, in bytes.\nfunc Free() uint64 { return 1 }\n"
	if !strings.Contains(string(body), want) || strings.Contains(string(body), "// Old.") {
		t.Errorf("want %q with the old comment gone, got:\n%s", want, body)
	}
}
