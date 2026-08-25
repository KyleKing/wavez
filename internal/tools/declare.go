package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kyleking/wavez/internal/edit"
	"github.com/kyleking/wavez/internal/tool"
)

const (
	propDoc    = "doc"
	propSource = "source"
)

var declareSchema = buildSchema(map[string]schemaProperty{
	propSymbol: {
		Type: schemaTypeString,
		Description: "Name of the declaration to write, exactly as it is or will be declared. " +
			"An existing one is replaced whole; a new one is appended to path.",
	},
	propSource: {
		Type: schemaTypeString,
		Description: "The declaration's full source, signature included, with no doc comment " +
			"above it. Send it once: there is no anchor to repeat.",
	},
	propPath: {
		Type: schemaTypeString,
		Description: "File the declaration belongs in, relative to the project root. Needed " +
			"for a new one, and for an existing name declared in several places.",
	},
	propDoc: {
		Type: schemaTypeString,
		Description: "Doc comment as plain prose, rendered as // lines above the declaration. " +
			"Omit it to leave an existing comment alone.",
	},
}, propSymbol, propSource)

// Declare writes one whole declaration by name, replacing the existing one
// or appending a new one.
//
// It is the Modifier for the edit shape `str_replace` serves worst. A
// replacement through `str_replace` costs the declaration's text twice,
// once as the anchor and once as the replacement, and the anchor has to
// match a file the model is recalling rather than reading. Measured on the
// `e2` replay task, that produced ~12,000-character tool arguments cut off
// mid-string at normal entropy: the fast tier ran out of window inside one
// call. Here the source is sent once and there is no anchor at all.
type Declare struct {
	scope *Scope
	index SymbolSearch
	root  string
	deps  deps
}

// NewDeclare builds a Declare tool scoped to root, resolving names through
// index.
func NewDeclare(root string, index SymbolSearch, scope *Scope, opts ...Option) *Declare {
	return &Declare{root: root, index: index, scope: scope, deps: newDeps(opts)}
}

// Name implements tool.Tool.
func (*Declare) Name() string { return "declare" }

// Description implements tool.Tool.
func (*Declare) Description() string {
	return "Write one whole declaration by name: replace the existing one or add a new one to " +
		"path. Prefer this over str_replace for a whole function, method, type, or test, " +
		"because the source is sent once and no anchor has to match."
}

// Schema implements tool.Tool.
func (*Declare) Schema() json.RawMessage { return declareSchema }

type declareInput struct {
	Symbol string `json:"symbol"`
	Source string `json:"source"`
	Path   string `json:"path"`
	Doc    string `json:"doc"`
}

// Run implements tool.Tool.
func (d *Declare) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("declare: %w", err)
	}

	var in declareInput
	if err := decodeInput(input, &in); err != nil {
		return tool.Fail(tool.CauseBadInput, "invalid input: %v", err), nil
	}

	if in.Symbol == "" || strings.TrimSpace(in.Source) == "" {
		return tool.Fail(tool.CauseBadInput,
			"declare needs both symbol and source: the name to write and its full text"), nil
	}

	text := withDoc(in.Doc, in.Source)

	decl, err := locate(ctx, d.index, d.root, in.Symbol, in.Path)
	if err != nil {
		if errors.Is(err, ErrSymbolNotIndexed) {
			return d.add(ctx, &in, text)
		}

		return failWith(err), nil
	}

	return d.replace(ctx, decl, text)
}

// replace swaps an existing declaration's whole span, doc comment included,
// for the new text.
func (d *Declare) replace(ctx context.Context, decl declaration, text string) (tool.Result, error) {
	abs, err := resolvePath(d.root, decl.path)
	if err != nil {
		return failWith(err), nil
	}

	if err := d.scope.Edit(abs); err != nil {
		return failWith(err), nil
	}

	release, err := d.deps.hold(ctx, abs)
	if err != nil {
		return failWith(err), nil
	}
	defer release()

	body, err := os.ReadFile(abs) //nolint:gosec // a path already resolved under the project root
	if err != nil {
		return failWith(fmt.Errorf("reading %s: %w", decl.path, err)), nil
	}

	lines := strings.Split(string(body), "\n")

	from, to := declSpan(lines, decl)
	if from < 0 {
		return tool.Fail(tool.CauseConflict,
			"%s moved since the index last read %s; read it again", decl.path, decl.path), nil
	}

	change, err := edit.ApplySpansToFile(abs, []edit.Span{{
		Line: from, EndLine: to, NewText: strings.TrimRight(text, "\n") + "\n\n",
	}})
	if err != nil {
		return failWith(err), nil
	}

	change.Path = decl.path
	d.scope.Wrote(abs)

	return tool.Result{
		Content: fmt.Sprintf("%s: replaced, +%d -%d lines", decl.path, change.Added, change.Removed),
		Changes: []tool.Change{change},
	}, nil
}

// add appends a declaration the index does not hold. It needs a path,
// because a name that exists nowhere cannot say where it belongs.
func (d *Declare) add(ctx context.Context, in *declareInput, text string) (tool.Result, error) {
	if in.Path == "" {
		return tool.Fail(tool.CauseBadInput,
			"no declaration named %s exists, so this would add one, and adding needs a path",
			in.Symbol), nil
	}

	abs, err := resolvePath(d.root, in.Path)
	if err != nil {
		return failWith(err), nil
	}

	if err := d.scope.Edit(abs); err != nil {
		return failWith(err), nil
	}

	release, err := d.deps.hold(ctx, abs)
	if err != nil {
		return failWith(err), nil
	}
	defer release()

	imports, body := splitImports(text)

	change, err := appendDecls(abs, []string{body})
	if err != nil {
		return failWith(err), nil
	}

	if len(imports) > 0 {
		if err := addImports(abs, imports); err != nil {
			return failWith(err), nil
		}
	}

	change.Path = in.Path
	d.scope.Wrote(abs)

	content := fmt.Sprintf("%s: added %s, +%d lines", in.Path, in.Symbol, change.Added)
	if pkg, perr := packageOf(abs); perr == nil {
		content += ", in package " + pkg

		if strings.HasSuffix(pkg, "_test") {
			content += ". That is an external test package, so every name from the package " +
				"under test needs its qualifier"
		}
	}

	return tool.Result{Content: content, Changes: []tool.Change{change}}, nil
}

// addImports merges the import lines a source carried into the file's own
// import block, since a second block after a declaration is not valid Go.
func addImports(abs string, paths []string) error {
	body, err := os.ReadFile(abs) //nolint:gosec // a path already resolved under the project root
	if err != nil {
		return fmt.Errorf("reading %s: %w", filepath.Base(abs), err)
	}

	merged := mergeImports(string(body), paths)
	if merged == string(body) {
		return nil
	}

	//nolint:gosec // abs is resolved under the project root by the caller
	if err := os.WriteFile(abs, []byte(merged), newFilePerm); err != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(abs), err)
	}

	return nil
}

// withDoc renders doc as // lines above source. It is taken as prose rather
// than as comment syntax so the model spends no tokens on the markers, and
// a doc already written as comments is passed through unchanged.
func withDoc(doc, source string) string {
	trimmed := strings.TrimSpace(doc)
	if trimmed == "" {
		return source
	}

	var b strings.Builder

	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//") {
			b.WriteString(line + "\n")

			continue
		}

		b.WriteString(strings.TrimRight("// "+line, " ") + "\n")
	}

	b.WriteString(source)

	return b.String()
}
