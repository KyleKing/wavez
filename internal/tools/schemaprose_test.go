package tools_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

// The schemas stopped predicting what their own failures would say, because
// that prose is paid on every turn of every thread to prevent a failure
// that pays for itself once. That trade only holds while the failures
// really do say it, so each cut clause is pinned here: a refactor that
// drops one of these messages has to notice that the schema no longer warns
// either.
func TestTheErrorsCarryWhatTheSchemasStoppedSaying(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	seed(t, root, "dup.go", "package a\n\nvar x = 1\nvar y = 2\nvar x = 3\n")
	seed(t, root, "one.go", "package a\n")

	scope := tools.NewScope(false)

	tests := []struct {
		run   func() (tool.Result, error)
		name  string
		wants []string
	}{
		{
			name: "an ambiguous anchor names the lines and says to widen it",
			run: func() (tool.Result, error) {
				return tools.NewStrReplace(root, scope).Run(t.Context(), mustJSON(t, map[string]any{
					"path": "dup.go", "old_string": "var x", "new_string": "var z",
				}))
			},
			wants: []string{"matches at lines", "widen old_string"},
		},
		{
			name: "a path outside the root is refused by name",
			run: func() (tool.Result, error) {
				return tools.NewStrReplace(root, scope).Run(t.Context(), mustJSON(t, map[string]any{
					"path": "../escape.go", "old_string": "a", "new_string": "b",
				}))
			},
			wants: []string{"outside the project root"},
		},
		{
			name: "a line range across several paths says a range reads one file",
			run: func() (tool.Result, error) {
				return tools.NewRead(root, scope).Run(t.Context(), mustJSON(t, map[string]any{
					"path": "one.go,dup.go", "start_line": 1, "end_line": 2,
				}))
			},
			wants: []string{"a line range reads one file"},
		},
		{
			name: "an end line before the start is refused",
			run: func() (tool.Result, error) {
				return tools.NewRead(root, scope).Run(t.Context(), mustJSON(t, map[string]any{
					"path": "one.go", "start_line": 5, "end_line": 2,
				}))
			},
			wants: []string{"start_line", "end_line"},
		},
		{
			name: "writing over an existing file points at str_replace",
			run: func() (tool.Result, error) {
				return tools.NewWrite(root, scope).Run(t.Context(), mustJSON(t, map[string]any{
					"path": "one.go", "content": "package a\n",
				}))
			},
			wants: []string{"str_replace"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res, err := tt.run()
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if !res.IsError {
				t.Fatalf("call succeeded, want the failure the schema stopped describing:\n%s", res.Content)
			}

			for _, want := range tt.wants {
				if !strings.Contains(res.Content, want) {
					t.Errorf("error = %q, want it to carry %q", res.Content, want)
				}
			}
		})
	}
}

// The tool surface is 84% of the preamble and 69% of it is prose, so a new
// description is a cost on every turn of every thread rather than a
// documentation decision.
func TestSchemasCarryNoDescriptionLongerThanItsWorth(t *testing.T) {
	t.Parallel()

	const maxDescription = 260

	for _, spec := range registrySpecs(t) {
		var doc map[string]any
		if err := json.Unmarshal(spec.Schema, &doc); err != nil {
			t.Fatalf("%s: parsing schema: %v", spec.Name, err)
		}

		for field, text := range descriptions(doc) {
			if len(text) > maxDescription {
				t.Errorf("%s.%s description is %d bytes (limit %d); every turn pays it:\n%s",
					spec.Name, field, len(text), maxDescription, text)
			}
		}
	}
}

func descriptions(v any) map[string]string {
	out := map[string]string{}

	doc, isDoc := v.(map[string]any)
	if !isDoc {
		return out
	}

	props, ok := doc["properties"].(map[string]any)
	if !ok {
		for _, branch := range branches(v) {
			for k, text := range descriptions(branch) {
				out[k] = text
			}
		}

		return out
	}

	for name, prop := range props {
		if text, ok := prop.(map[string]any)["description"].(string); ok {
			out[name] = text
		}
	}

	return out
}

func branches(v any) []any {
	doc, ok := v.(map[string]any)
	if !ok {
		return nil
	}

	list, ok := doc["oneOf"].([]any)
	if !ok {
		return nil
	}

	return list
}

func seed(t *testing.T, root, rel, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
		t.Fatalf("seeding %s: %v", rel, err)
	}
}

// registrySpecs is the file-level tool surface, which is every tool whose
// schema can be built without an index or a language server.
func registrySpecs(t *testing.T) []tool.Spec {
	t.Helper()

	root := t.TempDir()
	scope := tools.NewScope(false)

	return tool.NewRegistry(
		tools.NewList(root),
		tools.NewRead(root, scope),
		tools.NewStrReplace(root, scope),
		tools.NewWrite(root, scope),
	).Specs()
}

// A failed anchor has causes the harness can tell apart and the model
// cannot: the anchor is a whole declaration and declare needs none, the
// file changed under it, or it was never read. Measured over six `e2` lanes, `str_replace` failed 30 of 30
// calls while those runs made 2 reads and 36 searches between them.
func TestStrReplaceNamesWhyAnAnchorCouldNotMatch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	seed(t, root, "a.go", "package a\n\nfunc F() int {\n\treturn 1\n}\n")

	scope := tools.NewScope(false)
	sr := tools.NewStrReplace(root, scope)

	unread, err := sr.Run(t.Context(), mustJSON(t, map[string]any{
		"path": "a.go", "old_string": "\treturn 9\n", "new_string": "x",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(unread.Content, "not read a.go this run") {
		t.Errorf("Content = %q, want it to say the file was never read", unread.Content)
	}

	// Reading it, then editing it, then anchoring on the old text is the
	// other case, and it must not be reported as never read.
	if _, err := tools.NewRead(root, scope).Run(t.Context(),
		mustJSON(t, map[string]any{"path": "a.go"})); err != nil {
		t.Fatalf("read: %v", err)
	}

	if _, err := sr.Run(t.Context(), mustJSON(t, map[string]any{
		"path": "a.go", "old_string": "\treturn 1\n", "new_string": "\treturn 2\n",
	})); err != nil {
		t.Fatalf("edit: %v", err)
	}

	stale, err := sr.Run(t.Context(), mustJSON(t, map[string]any{
		"path": "a.go", "old_string": "\treturn 1\n", "new_string": "\treturn 3\n",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(stale.Content, "since you last read it") {
		t.Errorf("Content = %q, want it to say the run's own edit moved the file", stale.Content)
	}
}

// A whole-declaration anchor is the shape declare exists for, and the
// message names it by symbol so the retry needs no thought. Measured over
// six `e2` lanes, 17 of 19 failed anchors were a whole declaration and
// every one of them failed, while declare failed 2 of 22 calls.
func TestStrReplaceSendsAWholeDeclarationToDeclare(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	seed(t, root, "a.go", "package a\n\nfunc (m Memory) Used() int {\n\treturn 1\n}\n")

	res, err := tools.NewStrReplace(root, tools.NewScope(false)).Run(t.Context(),
		mustJSON(t, map[string]any{
			"path":       "a.go",
			"old_string": "func (m Memory) Used() int {\n\treturn 9\n}",
			"new_string": "func (m Memory) Used() int {\n\treturn 2\n}",
		}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !res.IsError {
		t.Fatalf("the call succeeded: %s", res.Content)
	}

	for _, want := range []string{"whole declaration of Used", `symbol "Used"`} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("Content = %q, want %q", res.Content, want)
		}
	}
}
