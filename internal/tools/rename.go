package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/edit"
	"github.com/kyleking/wavez/internal/lsp"
	"github.com/kyleking/wavez/internal/tool"
)

var renameSchema = buildSchema(map[string]schemaProperty{
	"symbol": {
		Type:        schemaTypeString,
		Description: "The name to rename, exactly as it is declared. Not a path, not a signature.",
	},
	"to": {
		Type:        schemaTypeString,
		Description: "The new name. Must be a valid identifier and must differ from symbol.",
	},
	propPath: {
		Type: schemaTypeString,
		Description: "The file declaring the symbol, needed only when several files declare " +
			"one by that name; the error lists them when it does.",
	},
}, "symbol", "to")

// The failures a caller can act on: narrow the name, re-index, or rename in
// a language something speaks.
var (
	// ErrAmbiguousSymbol reports a name declared in more than one place,
	// which only the caller can narrow.
	ErrAmbiguousSymbol = errors.New("more than one symbol has that name")
	// ErrSymbolNotIndexed reports a name the code index does not hold.
	ErrSymbolNotIndexed = errors.New("no symbol by that name is indexed")
	// ErrDeclarationMoved reports an index entry whose line no longer holds
	// the name, which means the tree changed under the index.
	ErrDeclarationMoved = errors.New("the indexed declaration no longer holds that name")
	// ErrNoServer reports a file no configured language server handles.
	ErrNoServer = errors.New("no language server handles that file")
)

// SymbolSearch is the index Rename resolves a name through. It is Index's
// shape because a stale index would send the server at a line the symbol has
// moved off, and Index refreshes as a side effect of searching.
type SymbolSearch = Index

// Servers hands out the language server that owns a file. It is the pool in
// production and a single client in a test.
type Servers interface {
	Handles(path string) bool
	Client(ctx context.Context, path string) (*lsp.Client, error)
}

// Rename renames a symbol everywhere the language server says it occurs.
//
// It exists because renaming through str_replace asks a model to emit the
// old and new source of every occurrence as JSON strings, and that is the
// one thing the fast tier reliably cannot do: measured across six runs of
// one rename task, `qwen3:8b` escaped the closing quote of old_string
// identically every time and swallowed the rest of the object into the
// string. A rename stated as two identifiers has no source to escape, and
// the server resolves it through type information rather than text, so a
// same-named symbol in another package is left alone.
type Rename struct {
	index   SymbolSearch
	servers Servers
	scope   *Scope
	deps    deps
	root    string
}

// NewRename builds a Rename tool rooted at root.
func NewRename(root string, index SymbolSearch, servers Servers, scope *Scope, opts ...Option) *Rename {
	return &Rename{root: root, index: index, servers: servers, scope: scope, deps: newDeps(opts)}
}

// Name implements tool.Tool.
func (*Rename) Name() string { return "rename" }

// Description implements tool.Tool.
func (*Rename) Description() string {
	return "Rename a symbol and every reference to it across the whole project, in one call. " +
		"Prefer this over editing each occurrence: it follows the language's own definition of " +
		"the symbol, so it never misses a use in another file or touches an unrelated name that " +
		"happens to match. Reports the files it changed."
}

// Schema implements tool.Tool.
func (*Rename) Schema() json.RawMessage { return renameSchema }

type renameInput struct {
	Symbol string `json:"symbol"`
	To     string `json:"to"`
	Path   string `json:"path"`
}

// Run implements tool.Tool.
func (r *Rename) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("rename: %w", err)
	}

	var in renameInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Errorf("invalid input: %v", err), nil
	}

	if msg := checkNames(in); msg != "" {
		return tool.Errorf("%s", msg), nil
	}

	decl, err := r.locate(ctx, in)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}

	edits, err := r.ask(ctx, decl, in.To)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}

	if len(edits) == 0 {
		return tool.Errorf("the language server found nothing to rename for %s", in.Symbol), nil
	}

	return r.apply(ctx, in, edits)
}

func checkNames(in renameInput) string {
	switch {
	case in.Symbol == "":
		return "symbol is required"
	case in.To == "":
		return "to is required"
	case in.To == in.Symbol:
		return "to is the same name as symbol"
	case !isIdentifier(in.To):
		return fmt.Sprintf("%q is not a valid identifier", in.To)
	default:
		return ""
	}
}

func isIdentifier(s string) bool {
	for i, r := range s {
		if r == '_' || unicode.IsLetter(r) || (i > 0 && unicode.IsDigit(r)) {
			continue
		}

		return false
	}

	return s != ""
}

// declaration is where a symbol is declared, in the zero-based line and
// UTF-16 column a language server addresses.
type declaration struct {
	path   string
	line   int
	column int
}

// locate finds the one declaration of in.Symbol. A name declared in several
// places is an error naming them rather than a guess: renaming the wrong one
// is a change the caller then has to find and undo.
func (r *Rename) locate(ctx context.Context, in renameInput) (declaration, error) {
	results, _, err := r.index.Search(ctx, codeintel.SearchQuery{Mode: codeintel.SearchFuzzy, Text: in.Symbol})
	if err != nil {
		return declaration{}, fmt.Errorf("searching for %s: %w", in.Symbol, err)
	}

	var found []codeintel.Symbol

	for i := range results {
		sym := results[i].Symbol
		if sym == nil || sym.Name != in.Symbol {
			continue
		}

		if in.Path != "" && sym.FilePath != in.Path {
			continue
		}

		found = append(found, *sym)
	}

	switch len(found) {
	case 0:
		return declaration{}, fmt.Errorf("%w: %s%s", ErrSymbolNotIndexed, in.Symbol, inFile(in.Path))
	case 1:
		return r.position(found[0], in.Symbol)
	default:
		return declaration{}, fmt.Errorf("%w: %s declares %s; name one with path",
			ErrAmbiguousSymbol, strings.Join(declaringFiles(found), ", "), in.Symbol)
	}
}

func inFile(path string) string {
	if path == "" {
		return ""
	}

	return " in " + path
}

func declaringFiles(syms []codeintel.Symbol) []string {
	seen := make(map[string]bool, len(syms))
	out := make([]string, 0, len(syms))

	for i := range syms {
		if seen[syms[i].FilePath] {
			continue
		}

		seen[syms[i].FilePath] = true

		out = append(out, syms[i].FilePath)
	}

	sort.Strings(out)

	return out
}

// position turns an indexed symbol into the exact spot the name starts. The
// index records the line a declaration begins on, and a server needs the
// identifier itself, so the name is found within that line.
func (r *Rename) position(sym codeintel.Symbol, name string) (declaration, error) {
	abs := filepath.Join(r.root, sym.FilePath)

	body, err := os.ReadFile(abs) //nolint:gosec // the path comes from this project's own index
	if err != nil {
		return declaration{}, fmt.Errorf("reading %s: %w", sym.FilePath, err)
	}

	lines := strings.Split(string(body), "\n")
	for i := sym.StartLine - 1; i < len(lines) && i < sym.EndLine; i++ {
		if i < 0 {
			continue
		}

		if col, ok := identifierColumn(lines[i], name); ok {
			return declaration{path: abs, line: i, column: col}, nil
		}
	}

	return declaration{}, fmt.Errorf("%w: %s at %s:%d", ErrDeclarationMoved, name, sym.FilePath, sym.StartLine)
}

// identifierColumn finds name in line as a whole word and reports its UTF-16
// column, which is what the protocol counts in.
func identifierColumn(line, name string) (int, bool) {
	for at := 0; ; {
		i := strings.Index(line[at:], name)
		if i < 0 {
			return 0, false
		}

		i += at
		if wholeWord(line, i, len(name)) {
			return len(utf16.Encode([]rune(line[:i]))), true
		}

		at = i + len(name)
	}
}

func wholeWord(line string, start, width int) bool {
	if start > 0 && isNameByte(line[start-1]) {
		return false
	}

	end := start + width

	return end >= len(line) || !isNameByte(line[end])
}

func isNameByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func (r *Rename) ask(ctx context.Context, decl declaration, to string) (map[string][]lsp.TextEdit, error) {
	if !r.servers.Handles(decl.path) {
		return nil, fmt.Errorf("%w: %s", ErrNoServer, filepath.Base(decl.path))
	}

	client, err := r.servers.Client(ctx, decl.path)
	if err != nil {
		return nil, fmt.Errorf("starting the language server: %w", err)
	}

	if _, err := client.Sync(ctx, decl.path); err != nil {
		return nil, fmt.Errorf("syncing %s: %w", decl.path, err)
	}

	//nolint:wrapcheck // Client.Rename already names the file and the symbol
	return client.Rename(ctx, decl.path, decl.line, decl.column, to)
}

// apply writes every file the server named, in sorted order so a failure
// part-way leaves a predictable set changed rather than a random one.
func (r *Rename) apply(ctx context.Context, in renameInput, edits map[string][]lsp.TextEdit) (tool.Result, error) {
	paths := make([]string, 0, len(edits))
	for path := range edits {
		paths = append(paths, path)
	}

	sort.Strings(paths)

	changes := make([]tool.Change, 0, len(paths))
	occurrences := 0

	for _, abs := range paths {
		if err := r.scope.Edit(abs); err != nil {
			return tool.Errorf("%v", err), nil
		}

		release, err := r.deps.hold(ctx, abs)
		if err != nil {
			return tool.Errorf("%v", err), nil
		}

		change, err := edit.ApplySpansToFile(abs, spansOf(edits[abs]))

		release()

		if err != nil {
			return tool.Errorf("renaming in %s: %v", r.relative(abs), err), nil
		}

		change.Path = r.relative(abs)
		occurrences += len(edits[abs])

		changes = append(changes, change)
	}

	return tool.Result{
		Content: fmt.Sprintf("renamed %s to %s: %s across %s\n%s",
			in.Symbol, in.To, plural(occurrences, "occurrence"), plural(len(changes), "file"),
			strings.Join(changedPaths(changes), "\n")),
		Changes: changes,
	}, nil
}

func spansOf(edits []lsp.TextEdit) []edit.Span {
	out := make([]edit.Span, 0, len(edits))
	for _, e := range edits {
		out = append(out, edit.Span{
			Line: e.Line, Column: e.Column,
			EndLine: e.EndLine, EndColumn: e.EndColumn,
			NewText: e.NewText,
		})
	}

	return out
}

func changedPaths(changes []tool.Change) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, "  "+c.Path)
	}

	return out
}

func (r *Rename) relative(abs string) string {
	rel, err := filepath.Rel(r.root, abs)
	if err != nil {
		return abs
	}

	return rel
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}

	return fmt.Sprintf("%d %ss", n, noun)
}
