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
		return tool.Fail(tool.CauseBadInput, "invalid input: %v", err), nil
	}

	names := splitNames(in.Symbol)
	if len(names) == 0 {
		return tool.Fail(tool.CauseBadInput, "symbol is required"), nil
	}

	if in.To == "" {
		return tool.Fail(tool.CauseBadInput, "to is required"), nil
	}

	dest, err := m.destination(in.To)
	if err != nil {
		return failWith(err), nil
	}

	// Everything is located before anything is written, and each file is
	// written once. A move that cut and appended per symbol left the tree in
	// a state where a declaration was in neither file, and anything reading
	// the tree in that window (the gates, the language server, a coverage
	// sweep) saw a package that does not build: measured on `h5`, one
	// correct move call was followed by a gate failure it could not
	// attribute and fourteen turns of the model hunting for a defect that
	// was no longer there.
	decls := make([]declaration, 0, len(names))

	for _, name := range names {
		decl, err := m.plan(ctx, name, in.Path, dest)
		if err != nil {
			return failWith(err), nil
		}

		decls = append(decls, decl)
	}

	release, err := m.hold(ctx, decls, dest)
	if err != nil {
		return failWith(err), nil
	}
	defer release()

	changes, err := m.apply(decls, dest)
	if err != nil {
		return failWith(err), nil
	}

	return tool.Result{
		Content: fmt.Sprintf("moved %s to %s", strings.Join(names, ", "), relativeTo(m.root, dest)),
		Changes: changes,
	}, nil
}

// plan resolves one name and refuses everything about the move that can be
// refused before a byte is written.
func (m *Move) plan(ctx context.Context, name, path, dest string) (declaration, error) {
	decl, err := locate(ctx, m.index, m.root, name, path)
	if err != nil {
		return declaration{}, err
	}

	if decl.path == dest {
		return declaration{}, fmt.Errorf("%w: %s is already in %s", ErrNowhereToMove, name, relativeTo(m.root, dest))
	}

	if err := samePackage(decl.path, dest); err != nil {
		return declaration{}, err
	}

	for _, p := range []string{decl.path, dest} {
		if err := m.scope.Edit(p); err != nil {
			return declaration{}, err
		}
	}

	return decl, nil
}

// hold takes the lease covering every file the move writes, and gives them
// all back together.
func (m *Move) hold(ctx context.Context, decls []declaration, dest string) (func(), error) {
	var releases []func()

	release := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}

	for _, path := range append(sourcePaths(decls), dest) {
		r, err := m.deps.hold(ctx, path)
		if err != nil {
			release()

			return nil, err
		}

		releases = append(releases, r)
	}

	return release, nil
}

// apply cuts every declaration from its file in one write per file, then
// appends them all to the destination in one more.
func (m *Move) apply(decls []declaration, dest string) ([]tool.Change, error) {
	var (
		changes []tool.Change
		moved   []string
	)

	for _, src := range sourcePaths(decls) {
		spans, text, err := cutPlan(src, decls)
		if err != nil {
			return nil, err
		}

		change, err := edit.ApplySpansToFile(src, spans)
		if err != nil {
			return nil, fmt.Errorf("moving out of %s: %w", relativeTo(m.root, src), err)
		}

		change.Path = relativeTo(m.root, src)
		change.Added, change.Removed = 0, removedLines(spans)
		changes = append(changes, change)
		moved = append(moved, text...)
	}

	added, err := appendDecls(dest, moved)
	if err != nil {
		return nil, fmt.Errorf("moving into %s: %w", relativeTo(m.root, dest), err)
	}

	added.Path = relativeTo(m.root, dest)

	return append(changes, added), nil
}

// sourcePaths is every file the move cuts from, in a stable order, since one
// call may name declarations that live in different files.
func sourcePaths(decls []declaration) []string {
	var out []string

	seen := make(map[string]bool, len(decls))

	for _, d := range decls {
		if seen[d.path] {
			continue
		}

		seen[d.path] = true

		out = append(out, d.path)
	}

	return out
}

// cutPlan is the spans to remove from one file and the text each holds.
func cutPlan(src string, decls []declaration) ([]edit.Span, []string, error) {
	body, err := os.ReadFile(src) //nolint:gosec // a path the index resolved under the project root
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", filepath.Base(src), err)
	}

	lines := strings.Split(string(body), "\n")

	var (
		spans []edit.Span
		text  []string
	)

	for _, d := range decls {
		if d.path != src {
			continue
		}

		from, to := declSpan(lines, d)
		if from < 0 {
			return nil, nil, fmt.Errorf("%w: lines %d-%d of %s",
				ErrDeclarationMoved, d.start, d.end, filepath.Base(src))
		}

		spans = append(spans, edit.Span{Line: from, EndLine: to})
		text = append(text, strings.Join(lines[from:to], "\n"))
	}

	return spans, text, nil
}

func removedLines(spans []edit.Span) int {
	total := 0
	for _, s := range spans {
		total += s.EndLine - s.Line
	}

	return total
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

// appendDecls adds every moved declaration to the end of dest in one write,
// creating the file with the package clause its neighbors use when it is not
// there yet. Imports are deliberately not touched: the format gate runs
// goimports over every change this tool makes, so an import the moved code
// needs arrives without this call reasoning about it.
func appendDecls(dest string, texts []string) (tool.Change, error) {
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

	var out strings.Builder

	out.WriteString(strings.TrimRight(string(body), "\n"))

	added := 0

	for _, text := range texts {
		trimmed := strings.Trim(text, "\n")
		out.WriteString("\n\n" + trimmed)

		added += strings.Count(trimmed, "\n") + 1
	}

	out.WriteString("\n")

	if err := os.WriteFile(dest, []byte(out.String()), newFilePerm); err != nil {
		return tool.Change{}, fmt.Errorf("writing %s: %w", filepath.Base(dest), err)
	}

	return tool.Change{Added: added}, nil
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
