package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/kyleking/wavez/internal/edit"
	"github.com/kyleking/wavez/internal/tool"
)

var deleteSchema = buildSchema(map[string]schemaProperty{
	"symbol": {
		Type: schemaTypeString,
		Description: "The name of the declaration to remove, exactly as it is declared, or " +
			"several separated by commas to remove them in one call.",
	},
	propPath: {
		Type: schemaTypeString,
		Description: "The file or directory declaring it, needed only when several places " +
			"declare one by that name; the error lists them when it does.",
	},
}, "symbol")

// Delete removes a whole declaration by name, with the doc comment above it.
//
// It is a Modifier for the same reason rename is: the call carries a name and
// nothing else, so there is no source to escape inside a JSON string, which
// is the failure that keeps the local tier off editing work. A fifth of every
// edit in this project's thread logs is a deletion, and each one is currently
// spent quoting back the exact text of what goes.
//
// It deletes what the code index says the declaration spans, not what the
// caller believes it spans, and the build gate is what catches a deletion
// something still refers to. The index holds functions, methods, and types,
// so a field, var, or const is not reachable by name and stays str_replace's
// work.
type Delete struct {
	index SymbolSearch
	scope *Scope
	deps  deps
	root  string
}

// NewDelete builds a Delete tool rooted at root.
func NewDelete(root string, index SymbolSearch, scope *Scope, opts ...Option) *Delete {
	return &Delete{root: root, index: index, scope: scope, deps: newDeps(opts)}
}

// Name implements tool.Tool.
func (*Delete) Name() string { return "delete" }

// Description implements tool.Tool.
func (*Delete) Description() string {
	return "Delete whole declarations and the doc comments above them, naming only the symbols. " +
		"Prefer this over replacing their text with nothing: it removes exactly what each " +
		"declaration spans, so a trailing brace or a neighbor cannot be caught in it."
}

// Schema implements tool.Tool.
func (*Delete) Schema() json.RawMessage { return deleteSchema }

type deleteInput struct {
	Symbol string `json:"symbol"`
	Path   string `json:"path"`
}

// Run implements tool.Tool.
func (d *Delete) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("delete: %w", err)
	}

	var in deleteInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Errorf("invalid input: %v", err), nil
	}

	names := splitNames(in.Symbol)
	if len(names) == 0 {
		return tool.Errorf("symbol is required"), nil
	}

	var (
		changes []tool.Change
		done    []string
	)

	// One at a time and in order, because each deletion moves the lines under
	// the ones after it and the index is what the next lookup reads.
	for _, name := range names {
		change, err := d.one(ctx, name, in.Path)
		if err != nil {
			return tool.Errorf("%v%s", err, alreadyDone(done)), nil
		}

		changes = append(changes, change)
		done = append(done, name)
	}

	return tool.Result{Content: deleted(changes, done), Changes: changes}, nil
}

// one removes a single declaration, reporting the change it made.
func (d *Delete) one(ctx context.Context, name, path string) (tool.Change, error) {
	decl, err := locate(ctx, d.index, d.root, name, path)
	if err != nil {
		return tool.Change{}, err
	}

	if err := d.scope.Edit(decl.path); err != nil {
		return tool.Change{}, err
	}

	release, err := d.deps.hold(ctx, decl.path)
	if err != nil {
		return tool.Change{}, err
	}
	defer release()

	body, err := os.ReadFile(decl.path)
	if err != nil {
		return tool.Change{}, fmt.Errorf("reading %s: %w", name, err)
	}

	lines := strings.Split(string(body), "\n")

	from, to := declSpan(lines, decl)
	if from < 0 {
		return tool.Change{}, fmt.Errorf("%w: %s at lines %d-%d of %s",
			ErrDeclarationMoved, name, decl.start, decl.end, relativeTo(d.root, decl.path))
	}

	change, err := edit.ApplySpansToFile(decl.path, []edit.Span{{Line: from, EndLine: to}})
	if err != nil {
		return tool.Change{}, fmt.Errorf("deleting %s: %w", name, err)
	}

	change.Path = relativeTo(d.root, decl.path)
	change.Added = 0
	change.Removed = to - from

	return change, nil
}

// splitNames reads one name or a comma-separated several, dropping the empty
// pieces a trailing comma leaves.
func splitNames(s string) []string {
	out := make([]string, 0, 1)

	for _, part := range strings.Split(s, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}

	return out
}

// alreadyDone reports what a partly-applied call did before it stopped, since
// a caller that reruns the whole list would otherwise be told those names are
// not indexed.
func alreadyDone(done []string) string {
	if len(done) == 0 {
		return ""
	}

	return fmt.Sprintf(" (%s already deleted)", strings.Join(done, ", "))
}

func deleted(changes []tool.Change, names []string) string {
	lines := 0
	for _, c := range changes {
		lines += c.Removed
	}

	return fmt.Sprintf("deleted %s: %s removed from %s",
		strings.Join(names, ", "), plural(lines, "line"), plural(len(changes), "file"))
}

// declSpan widens the indexed declaration to the lines that belong with it:
// the doc comment directly above, and the blank line directly below, so the
// file is left as if the declaration had never been written rather than with
// an orphaned comment and a double blank line the formatter then has to fix.
//
// Both ends are zero-based, and the end is exclusive: it names the first line
// that survives.
//
//nolint:gocritic // nonamedreturns forbids naming these; the sentence above carries their meaning
func declSpan(lines []string, decl declaration) (int, int) {
	from, to := decl.start-1, decl.end

	if from < 0 || to > len(lines) || from >= to {
		return -1, -1
	}

	for from > 0 && isDocComment(lines[from-1]) {
		from--
	}

	if to < len(lines) && strings.TrimSpace(lines[to]) == "" {
		to++
	}

	return from, to
}

func isDocComment(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "//")
}
