package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/kyleking/wavez/internal/edit"
	"github.com/kyleking/wavez/internal/tool"
)

var moveSchema = buildSchema(map[string]schemaProperty{
	propSymbol: {
		Type: schemaTypeString,
		Description: "The name of the declaration to move, exactly as it is declared, or " +
			"several separated by commas to move them all in one call.",
	},
	"to": {
		Type: schemaTypeString,
		Description: "The file to move it into, relative to the project root. It is created " +
			"if it does not exist, and it must be in the same package as the declaration.",
	},
	propPath: {
		Type: schemaTypeString,
		Description: "The file or directory declaring it, needed only when several places " +
			"declare one by that name; the error lists them when it does.",
	},
}, "symbol", "to")

// ErrCrossPackageMove reports a move between packages, which is a different
// operation: every use of the declaration has to change with it.
var ErrCrossPackageMove = errors.New("that moves the declaration to another package")

// ErrNowhereToMove reports a destination this tool will not write to.
var ErrNowhereToMove = errors.New("that is not somewhere this can move a declaration")

// Move relocates whole declarations between files of one package.
//
// It is a Modifier for the reason rename and delete are: the call carries
// names and a destination, so no source crosses the wire inside a JSON
// string, which is the emission that keeps the local tier off editing work.
// Splitting an overgrown file is otherwise a block rewrite in both
// directions, and block rewrites are 35% of this project's logged edits and
// half of their bytes.
//
// The move is within one package on purpose. Across packages every use of
// the declaration has to change too, which is rename's kind of work and not
// a text move, so this refuses it rather than half-doing it.
type Move struct {
	index SymbolSearch
	scope *Scope
	deps  deps
	root  string
}

// NewMove builds a Move tool rooted at root.
func NewMove(root string, index SymbolSearch, scope *Scope, opts ...Option) *Move {
	return &Move{root: root, index: index, scope: scope, deps: newDeps(opts)}
}

// Name implements tool.Tool.
func (*Move) Name() string { return "move" }

// Description implements tool.Tool.
func (*Move) Description() string {
	return "Move whole declarations to another file in the same package, with the doc " +
		"comment above each. Name the declarations and the destination file; the file is " +
		"created if it does not exist. Use this to split a file rather than rewriting both ends."
}

// Schema implements tool.Tool.
func (*Move) Schema() json.RawMessage { return moveSchema }

type moveInput struct {
	Symbol string `json:"symbol"`
	To     string `json:"to"`
	Path   string `json:"path"`
}

// Run implements tool.Tool.
func (m *Move) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("move: %w", err)
	}

	var in moveInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Errorf("invalid input: %v", err), nil
	}

	names := splitNames(in.Symbol)
	if len(names) == 0 {
		return tool.Errorf("symbol is required"), nil
	}

	if in.To == "" {
		return tool.Errorf("to is required"), nil
	}

	dest, err := m.destination(in.To)
	if err != nil {
		return tool.Errorf("%v", err), nil
	}

	var (
		changes []tool.Change
		done    []string
	)

	// One at a time and in order: each cut moves the lines under it, and the
	// next lookup reads the file as the last one left it.
	for _, name := range names {
		moved, err := m.one(ctx, name, in.Path, dest)
		if err != nil {
			return tool.Errorf("%v%s", err, alreadyDone(done)), nil
		}

		changes = append(changes, moved...)
		done = append(done, name)
	}

	return tool.Result{
		Content: fmt.Sprintf("moved %s to %s", strings.Join(done, ", "), relativeTo(m.root, dest)),
		Changes: changes,
	}, nil
}

func (m *Move) one(ctx context.Context, name, path, dest string) ([]tool.Change, error) {
	decl, err := locate(ctx, m.index, m.root, name, path)
	if err != nil {
		return nil, err
	}

	if decl.path == dest {
		return nil, fmt.Errorf("%w: %s is already in %s", ErrNowhereToMove, name, relativeTo(m.root, dest))
	}

	if err := samePackage(decl.path, dest); err != nil {
		return nil, err
	}

	for _, p := range []string{decl.path, dest} {
		if err := m.scope.Edit(p); err != nil {
			return nil, err
		}
	}

	release, err := m.deps.hold(ctx, decl.path)
	if err != nil {
		return nil, err
	}
	defer release()

	body, err := os.ReadFile(decl.path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}

	lines := strings.Split(string(body), "\n")

	from, to := declSpan(lines, decl)
	if from < 0 {
		return nil, fmt.Errorf("%w: %s at lines %d-%d of %s",
			ErrDeclarationMoved, name, decl.start, decl.end, relativeTo(m.root, decl.path))
	}

	text := strings.Join(lines[from:to], "\n")

	cut, err := edit.ApplySpansToFile(decl.path, []edit.Span{{Line: from, EndLine: to}})
	if err != nil {
		return nil, fmt.Errorf("moving %s out of %s: %w", name, relativeTo(m.root, decl.path), err)
	}

	cut.Path = relativeTo(m.root, decl.path)
	cut.Added, cut.Removed = 0, to-from

	added, err := appendDecl(dest, text)
	if err != nil {
		return nil, fmt.Errorf("moving %s into %s: %w", name, relativeTo(m.root, dest), err)
	}

	added.Path = relativeTo(m.root, dest)

	return []tool.Change{cut, added}, nil
}

// destination resolves the target file, which need not exist yet.
func (m *Move) destination(to string) (string, error) {
	if filepath.IsAbs(to) {
		return "", fmt.Errorf("%w: to must be relative to the project root, not %s", ErrNowhereToMove, to)
	}

	dest := filepath.Join(m.root, filepath.Clean(to))
	if !strings.HasPrefix(dest, m.root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: to must stay inside the project, not %s", ErrNowhereToMove, to)
	}

	return dest, nil
}

// samePackage refuses a move that would cross a package boundary, naming
// both packages: a caller told only "no" cannot tell whether it picked the
// wrong file or the wrong operation.
func samePackage(src, dest string) error {
	from, err := packageOf(src)
	if err != nil {
		return err
	}

	into, err := packageOf(dest)
	if err != nil {
		return err
	}

	if into == "" || into == from {
		return nil
	}

	return fmt.Errorf("%w: %s is in package %s and the destination is package %s. "+
		"Moving between packages changes every use of it, so do that with rename and str_replace",
		ErrCrossPackageMove, filepath.Base(src), from, into)
}

// packageOf is the package a Go file declares, empty when the file does not
// exist yet, since a file this call is about to create belongs to whatever
// package it lands beside.
func packageOf(path string) (string, error) {
	body, err := os.ReadFile(path) //nolint:gosec // a path already resolved under the project root
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}

	file, err := parser.ParseFile(token.NewFileSet(), path, body, parser.PackageClauseOnly)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
	}

	return file.Name.Name, nil
}

// appendDecl adds text to the end of dest, creating the file with the
// package clause its neighbors use when it is not there yet. Imports are
// deliberately not touched: the format gate runs goimports over every change
// this tool makes, so an import the moved code needs arrives without this
// call reasoning about it.
func appendDecl(dest, text string) (tool.Change, error) {
	body, err := os.ReadFile(dest) //nolint:gosec // a path already resolved under the project root
	switch {
	case errors.Is(err, os.ErrNotExist):
		pkg, perr := neighbourPackage(dest)
		if perr != nil {
			return tool.Change{}, perr
		}

		body = []byte("package " + pkg + "\n")
	case err != nil:
		return tool.Change{}, fmt.Errorf("reading %s: %w", filepath.Base(dest), err)
	}

	out := strings.TrimRight(string(body), "\n") + "\n\n" + strings.Trim(text, "\n") + "\n"

	//nolint:gosec // dest is checked against the project root in destination
	if err := os.WriteFile(dest, []byte(out), newFilePerm); err != nil {
		return tool.Change{}, fmt.Errorf("writing %s: %w", filepath.Base(dest), err)
	}

	return tool.Change{Added: strings.Count(strings.Trim(text, "\n"), "\n") + 1}, nil
}

// neighbourPackage is the package the destination's directory already
// declares, which is what a new file there has to join.
func neighbourPackage(dest string) (string, error) {
	entries, err := os.ReadDir(filepath.Dir(dest))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", filepath.Dir(dest), err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}

		if pkg, perr := packageOf(filepath.Join(filepath.Dir(dest), e.Name())); perr == nil && pkg != "" {
			return pkg, nil
		}
	}

	return "", fmt.Errorf("%w: nothing in %s says what package a new file there joins",
		ErrCrossPackageMove, filepath.Dir(dest))
}
