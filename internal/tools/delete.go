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
		Type:        schemaTypeString,
		Description: "The name of the declaration to remove, exactly as it is declared.",
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
	return "Delete a whole declaration and the doc comment above it, naming only the symbol. " +
		"Prefer this over replacing its text with nothing: it removes exactly what the " +
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

	if in.Symbol == "" {
		return tool.Errorf("symbol is required"), nil
	}

	decl, err := locate(ctx, d.index, d.root, in.Symbol, in.Path)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}

	if err := d.scope.Edit(decl.path); err != nil {
		return tool.Errorf("%v", err), nil
	}

	release, err := d.deps.hold(ctx, decl.path)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	defer release()

	body, err := os.ReadFile(decl.path)
	if err != nil {
		return tool.Errorf("reading %s: %v", in.Symbol, err), nil
	}

	lines := strings.Split(string(body), "\n")

	from, to := declSpan(lines, decl)
	if from < 0 {
		return tool.Errorf("%s is indexed at lines %d-%d, which %s does not have",
			in.Symbol, decl.start, decl.end, relativeTo(d.root, decl.path)), nil
	}

	span := edit.Span{Line: from, Column: 0, EndLine: to, EndColumn: 0}

	change, err := edit.ApplySpansToFile(decl.path, []edit.Span{span})
	if err != nil {
		return tool.Errorf("deleting %s: %v", in.Symbol, err), nil
	}

	change.Path = relativeTo(d.root, decl.path)
	change.Added = 0
	change.Removed = to - from

	return tool.Result{
		Content: fmt.Sprintf("deleted %s from %s: %s removed", in.Symbol, change.Path, plural(to-from, "line")),
		Changes: []tool.Change{change},
	}, nil
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
