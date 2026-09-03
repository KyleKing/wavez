package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kyleking/wavez/internal/edit"
	"github.com/kyleking/wavez/internal/tool"
)

const propDocs = "docs"

var documentSchema = buildSchema(map[string]schemaProperty{
	propDocs: {
		Type:        schemaTypeArray,
		Description: "One entry per declaration. Send every doc you have in one call.",
		Items: &schemaItems{
			Type: schemaTypeObject,
			Properties: map[string]schemaProperty{
				propSymbol: {
					Type:        schemaTypeString,
					Description: "Name of the declaration, exactly as declared.",
				},
				propDoc: {
					Type:        schemaTypeString,
					Description: "The doc as plain prose. It replaces any doc the declaration has now.",
				},
				propPath: {
					Type:        schemaTypeString,
					Description: "File holding it. Needed only for a name declared twice.",
				},
			},
			Required: []string{propSymbol, propDoc},
		},
	},
}, propDocs)

// Document writes declarations' docs by name and touches nothing else.
//
// It is the insertion `declare` cannot express: `declare` needs the whole
// source, so documenting an existing 30-line method costs those 30 lines to
// leave them unchanged. Measured on the 2026-09-02 ruff lane, 86 docstrings
// went in through `str_replace` at roughly 190 output tokens each, of which
// the anchor and the re-typed body were 55%.
//
// The input is a list rather than one entry with a batch alternative, because
// a top-level `oneOf` is unreachable on the hosted tier and a run answering a
// check's whole findings list has N answers to give at once. Entries are
// independent, so one name the index cannot resolve costs its own entry and
// not the other eighty-five.
type Document struct {
	scope *Scope
	index SymbolSearch
	root  string
	deps  deps
}

// NewDocument builds a Document tool scoped to root, resolving names through
// index.
func NewDocument(root string, index SymbolSearch, scope *Scope, opts ...Option) *Document {
	return &Document{root: root, index: index, scope: scope, deps: newDeps(opts)}
}

// Name implements tool.Tool.
func (*Document) Name() string { return "document" }

// Description implements tool.Tool.
func (*Document) Description() string {
	return "Write declarations' doc comments or docstrings by name, leaving their bodies alone. " +
		"No anchor to match and no body to repeat, and every doc you have goes in one call."
}

// Schema implements tool.Tool.
func (*Document) Schema() json.RawMessage { return documentSchema }

// Risk implements tool.Tool.
func (*Document) Risk() tool.RiskClass { return tool.RiskWriteLocal }

type documentEntry struct {
	Symbol string `json:"symbol"`
	Doc    string `json:"doc"`
	Path   string `json:"path"`
}

type documentInput struct {
	Docs []documentEntry `json:"docs"`
}

// Run implements tool.Tool.
func (d *Document) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("document: %w", err)
	}

	var in documentInput
	if err := decodeInput(input, &in); err != nil {
		return tool.Fail(tool.CauseBadInput, "invalid input: %v", err), nil
	}

	if len(in.Docs) == 0 {
		return tool.Fail(tool.CauseBadInput, "document needs at least one docs entry"), nil
	}

	done := make([]string, 0, len(in.Docs))
	refused := make([]string, 0)
	changes := make([]tool.Change, 0, len(in.Docs))

	for _, entry := range in.Docs {
		change, why := d.one(ctx, entry)
		if why != "" {
			refused = append(refused, entry.Symbol+": "+why)

			continue
		}

		done = append(done, entry.Symbol)
		changes = append(changes, change)
	}

	return documentResult(done, refused, changes), nil
}

// documentResult reports the whole call in one line plus the entries that did
// not land, since a run given only a count cannot tell which name to fix.
func documentResult(done, refused []string, changes []tool.Change) tool.Result {
	content := fmt.Sprintf("documented %d of %d: %s",
		len(done), len(done)+len(refused), strings.Join(done, ", "))
	if len(done) == 0 {
		content = fmt.Sprintf("documented none of %d", len(refused))
	}

	if len(refused) > 0 {
		content += "\nnot done:\n  " + strings.Join(refused, "\n  ")
	}

	return tool.Result{
		Content: content,
		Changes: changes,
		IsError: len(done) == 0,
		Cause:   documentCause(done),
	}
}

func documentCause(done []string) tool.Cause {
	if len(done) == 0 {
		return tool.CauseBadInput
	}

	return ""
}

// one writes a single entry, answering with the reason it could not rather
// than an error, because the other entries in the call still apply.
func (d *Document) one(ctx context.Context, entry documentEntry) (tool.Change, string) {
	if entry.Symbol == "" || strings.TrimSpace(entry.Doc) == "" {
		return tool.Change{}, "an entry needs both symbol and doc"
	}

	decl, err := locate(ctx, d.index, d.root, entry.Symbol, entry.Path)
	if err != nil {
		return tool.Change{}, err.Error()
	}

	abs, err := resolvePath(d.root, d.deps.extraRoots, decl.path)
	if err != nil {
		return tool.Change{}, err.Error()
	}

	if err := d.scope.Edit(abs); err != nil {
		return tool.Change{}, err.Error()
	}

	release, err := d.deps.hold(ctx, abs)
	if err != nil {
		return tool.Change{}, err.Error()
	}
	defer release()

	body, err := os.ReadFile(abs) //nolint:gosec // a path already resolved under the project root
	if err != nil {
		return tool.Change{}, err.Error()
	}

	span, why := docSpan(strings.Split(string(body), "\n"), decl, plainDoc(entry.Doc))
	if why != "" {
		return tool.Change{}, why
	}

	change, err := edit.ApplySpansToFile(abs, []edit.Span{span})
	if err != nil {
		return tool.Change{}, err.Error()
	}

	change.Path = relativeTo(d.root, decl.path)
	d.scope.Wrote(abs)

	return change, ""
}

// plainDoc strips the comment markers and quotes a model writes out of habit.
// Left in, they would be rendered a second time, and saying so in the schema
// would cost the reminder on every turn of every thread instead of once here.
func plainDoc(doc string) string {
	trimmed := strings.TrimSpace(doc)

	for _, quote := range []string{`"""`, "'''"} {
		if strings.HasPrefix(trimmed, quote) && strings.HasSuffix(trimmed, quote) && len(trimmed) > 2*len(quote) {
			trimmed = strings.TrimSpace(trimmed[len(quote) : len(trimmed)-len(quote)])

			break
		}
	}

	out := make([]string, 0, 8)

	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)

		for _, marker := range []string{"///", "//", "#"} {
			if strings.HasPrefix(line, marker) {
				line = strings.TrimSpace(line[len(marker):])

				break
			}
		}

		out = append(out, line)
	}

	return strings.TrimSpace(strings.Join(out, "\n"))
}

// docSpan places doc for one declaration. Where the doc goes is the language's
// answer and not a preference: Go reads a comment above the declaration and
// Python reads a string as the body's first statement, so writing one shape in
// the other language's file produces a syntax error rather than a wrong
// comment.
func docSpan(lines []string, decl declaration, doc string) (edit.Span, string) {
	if decl.start-1 < 0 || decl.start > len(lines) {
		return edit.Span{}, decl.path + " moved since the index last read it. Read it again"
	}

	switch ext := strings.ToLower(filepath.Ext(decl.path)); ext {
	case ".go":
		return goDocSpan(lines, decl, doc), ""
	case ".py", ".pyi":
		return pyDocSpan(lines, decl, doc)
	default:
		return edit.Span{}, fmt.Sprintf(
			"document writes Go comments and Python docstrings, and %s is neither. Use str_replace", ext,
		)
	}
}

func goDocSpan(lines []string, decl declaration, doc string) edit.Span {
	from := decl.start - 1
	for from > 0 && isDocComment(lines[from-1]) {
		from--
	}

	indent := leadingSpace(lines[decl.start-1])

	var b strings.Builder

	for _, line := range strings.Split(strings.TrimSpace(doc), "\n") {
		b.WriteString(strings.TrimRight(indent+"// "+strings.TrimSpace(line), " ") + "\n")
	}

	return edit.Span{Line: from, EndLine: decl.start - 1, NewText: b.String()}
}

func pyDocSpan(lines []string, decl declaration, doc string) (edit.Span, string) {
	if strings.Contains(doc, `"""`) {
		return edit.Span{}, `doc holds a """ that would close the docstring it opens. ` +
			"Write it as prose, or use str_replace"
	}

	body, why := pyBodyStart(lines, decl)
	if why != "" {
		return edit.Span{}, why
	}

	indent := leadingSpace(lines[body])
	end := body

	if open := pyStringDelimiter(strings.TrimSpace(lines[body])); open != "" {
		if end, why = pyStringEnd(lines, body, open); why != "" {
			return edit.Span{}, why
		}
	}

	text := strings.Split(strings.TrimSpace(doc), "\n")
	if len(text) == 1 {
		return edit.Span{Line: body, EndLine: end, NewText: indent + `"""` + text[0] + `"""` + "\n"}, ""
	}

	var b strings.Builder

	b.WriteString(indent + `"""` + strings.TrimSpace(text[0]) + "\n")

	for _, line := range text[1:] {
		b.WriteString(strings.TrimRight(indent+strings.TrimSpace(line), " ") + "\n")
	}

	b.WriteString(indent + `"""` + "\n")

	return edit.Span{Line: body, EndLine: end, NewText: b.String()}, ""
}

// pyBodyStart finds the first line of a declaration's body, following a
// signature that wraps across lines by counting brackets rather than by
// matching a shape.
func pyBodyStart(lines []string, decl declaration) (int, string) {
	depth := 0

	for i := decl.start - 1; i < decl.end && i < len(lines); i++ {
		code := pyCodeOf(lines[i])
		depth += strings.Count(code, "(") + strings.Count(code, "[") + strings.Count(code, "{")
		depth -= strings.Count(code, ")") + strings.Count(code, "]") + strings.Count(code, "}")

		if depth > 0 || !strings.HasSuffix(strings.TrimSpace(code), ":") {
			continue
		}

		if i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) == "" {
			return 0, fmt.Sprintf("%s:%d opens a body with nothing in it", decl.path, i+1)
		}

		return i + 1, ""
	}

	return 0, fmt.Sprintf(
		"%s:%d puts its body on the signature line, so there is nowhere for a docstring. Use str_replace",
		decl.path, decl.start,
	)
}

// pyCodeOf drops a trailing comment so a `#` inside it cannot be read as code.
// A `#` inside a string would be dropped too, which costs a wrapped signature
// holding one a fallback to str_replace rather than a wrong edit.
func pyCodeOf(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		return line[:i]
	}

	return line
}

// pyStringDelimiter answers the quote a line opens a string literal with,
// prefixes included, or "" when it opens none.
func pyStringDelimiter(line string) string {
	line = strings.TrimLeft(line, "rRbBuUfF")

	for _, q := range []string{`"""`, "'''", `"`, "'"} {
		if strings.HasPrefix(line, q) {
			return q
		}
	}

	return ""
}

func pyStringEnd(lines []string, start int, open string) (int, string) {
	rest := strings.TrimSpace(lines[start])
	rest = strings.TrimLeft(rest, "rRbBuUfF")[len(open):]

	for i := start; i < len(lines); i++ {
		if strings.Contains(rest, open) {
			return i + 1, ""
		}

		if i+1 < len(lines) {
			rest = lines[i+1]
		}
	}

	return 0, fmt.Sprintf("the docstring at line %d is never closed", start+1)
}

func leadingSpace(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}
