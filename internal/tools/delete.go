package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kyleking/wavez/internal/edit"
	"github.com/kyleking/wavez/internal/lsp"
	"github.com/kyleking/wavez/internal/tool"
)

var deleteSchema = buildSchema(map[string]schemaProperty{
	propSymbol: {
		Type: schemaTypeString,
		Description: "The name of the declaration to remove, exactly as it is declared, or " +
			"several separated by commas to remove them in one call.",
	},
	propPath: {
		Type: schemaTypeString,
		Description: "The file or directory declaring it, needed only when several places " +
			"declare one by that name.",
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
	index   SymbolSearch
	servers Servers
	scope   *Scope
	root    string
	deps    deps
}

// NewDelete builds a Delete tool rooted at root. Servers is what it asks
// whether anything still uses a declaration; a nil one, or a file no server
// handles, deletes without that check.
func NewDelete(root string, index SymbolSearch, servers Servers, scope *Scope, opts ...Option) *Delete {
	return &Delete{root: root, index: index, servers: servers, scope: scope, deps: newDeps(opts)}
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
		return tool.Fail(tool.CauseBadInput, "invalid input: %v", err), nil
	}

	names := splitNames(in.Symbol)
	if len(names) == 0 {
		return tool.Fail(tool.CauseBadInput, "symbol is required"), nil
	}

	// Where every named declaration sits, taken before anything moves: the
	// reference check needs to know which uses belong to the other symbols
	// this same call removes, and by the time it asks, they are gone.
	together := d.rangesOf(ctx, names, in.Path)

	var (
		changes []tool.Change
		done    []string
	)

	// One at a time and in order, because each deletion moves the lines under
	// the ones after it and the index is what the next lookup reads.
	for _, name := range names {
		change, err := d.one(ctx, name, in.Path, together)
		if err != nil {
			return tool.Fail(causeOf(err), "%v%s", err, alreadyDone(done)), nil
		}

		changes = append(changes, change)
		done = append(done, name)
	}

	return tool.Result{Content: deleted(changes, done), Changes: changes}, nil
}

// one removes a single declaration, reporting the change it made.
func (d *Delete) one(ctx context.Context, name, path string, together []declaration) (tool.Change, error) {
	decl, err := locate(ctx, d.index, d.root, name, path)
	if err != nil {
		return tool.Change{}, err
	}

	if err := d.stillUsed(ctx, name, decl, together); err != nil {
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

// ErrStillUsed reports a declaration something else still refers to.
var ErrStillUsed = errors.New("something still uses that declaration")

// stillUsed refuses a deletion the rest of the tree depends on. A Modifier
// makes removing a declaration one short call, so the blast radius of a
// misread task goes up exactly as the token cost goes down: measured on `h4`,
// a run told to leave `ApplyAllToFile` alone deleted it in one call, broke
// the build, and spent the rest of itself failing to put it back.
//
// References the same call is already removing do not count, since deleting a
// function and its tests together is the ordinary case.
func (d *Delete) stillUsed(ctx context.Context, name string, decl declaration, together []declaration) error {
	if d.servers == nil || !d.servers.Handles(decl.path) {
		return nil
	}

	client, err := d.servers.Client(ctx, decl.path)
	if err != nil {
		return fmt.Errorf("asking what uses %s: %w", name, err)
	}

	if _, err := client.Sync(ctx, decl.path); err != nil {
		return fmt.Errorf("syncing %s: %w", relativeTo(d.root, decl.path), err)
	}

	refs, err := client.References(ctx, decl.path, decl.line, decl.column)
	if err != nil {
		return fmt.Errorf("asking what uses %s: %w", name, err)
	}

	held := d.outside(refs, decl, together)
	if len(held) == 0 {
		return nil
	}

	return fmt.Errorf("%w: %s is used at %s. Name the declarations holding those uses "+
		"alongside it in one call, as \"symbol\": \"%s, ...\", or leave it alone",
		ErrStillUsed, name, strings.Join(held, ", "), name)
}

// outside lists the uses that are neither inside the declaration itself nor
// inside something this call is also removing.
func (d *Delete) outside(refs []lsp.Reference, decl declaration, together []declaration) []string {
	const most = 3

	out := make([]string, 0, most)

	for _, ref := range refs {
		if ref.Path == decl.path && ref.Line >= decl.start-1 && ref.Line < decl.end {
			continue
		}

		if inAnyOf(ref, together) {
			continue
		}

		if len(out) == most {
			out = append(out, "and more")

			break
		}

		out = append(out, d.describe(ref))
	}

	return out
}

// rangesOf locates every named declaration, skipping the ones it cannot
// find: a name that does not resolve fails later with its own message, and
// this is only building the set of places whose references do not count.
func (d *Delete) rangesOf(ctx context.Context, names []string, path string) []declaration {
	out := make([]declaration, 0, len(names))

	for _, name := range names {
		if decl, err := locate(ctx, d.index, d.root, name, path); err == nil {
			out = append(out, decl)
		}
	}

	return out
}

// describe names the declaration holding a reference, falling back to the
// bare location when it cannot find one. The name is what the next call
// needs: told only that `ApplyToFile` is used at `apply_test.go:23`, a run
// answered by naming the two declarations the task had told it to keep, and
// stopped. Told `TestApplyToFile (apply_test.go:23)`, it has the argument.
//
// This shapes a message and never an edit, so walking up to the nearest
// column-zero declaration is good enough where the index does not reach.
func (d *Delete) describe(ref lsp.Reference) string {
	at := fmt.Sprintf("%s:%d", relativeTo(d.root, ref.Path), ref.Line+1)

	name := enclosing(ref)
	if name == "" {
		return at
	}

	return fmt.Sprintf("%s (%s)", name, at)
}

// enclosing reads the referencing file and walks up to the declaration the
// reference sits in.
func enclosing(ref lsp.Reference) string {
	body, err := os.ReadFile(ref.Path)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(body), "\n")
	if ref.Line >= len(lines) {
		return ""
	}

	for i := ref.Line; i >= 0; i-- {
		if name := declaredOn(lines[i]); name != "" {
			return name
		}
	}

	return ""
}

// declaredOn returns the name a top-level declaration line declares, or "".
func declaredOn(line string) string {
	for _, keyword := range []string{"func ", "type ", "var ", "const "} {
		if !strings.HasPrefix(line, keyword) {
			continue
		}

		rest := strings.TrimPrefix(line, keyword)
		if strings.HasPrefix(rest, "(") { // a method: skip its receiver
			if i := strings.Index(rest, ")"); i >= 0 {
				rest = strings.TrimSpace(rest[i+1:])
			}
		}

		return strings.TrimSpace(strings.FieldsFunc(rest, func(r rune) bool {
			return r == '(' || r == '[' || r == ' ' || r == '{'
		})[0])
	}

	return ""
}

// inAnyOf reports whether a reference sits inside one of the declarations
// this same call is removing.
func inAnyOf(ref lsp.Reference, together []declaration) bool {
	for _, decl := range together {
		if ref.Path == decl.path && ref.Line >= decl.start-1 && ref.Line < decl.end {
			return true
		}
	}

	return false
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
