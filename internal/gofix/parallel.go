// Package gofix holds the deterministic repairs a harness makes to Go
// source before a model is asked to. Each one is mechanical and
// semantics-preserving, in the same category as gofmt: 94% of every lint
// finding logged against a model was either a duplicate of another gate or
// a fix with no judgment in it, and a fix with no judgment costs a gate
// round for nothing.
package gofix

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

// errBadOffset reports a splice offset that fell outside the source, which
// can only mean the parse and the bytes disagree.
var errBadOffset = errors.New("parallel fix: offset outside source")

// insertion is one splice: where it goes and the receiver it is called on,
// which is the test function's own parameter name and not always `t`.
type insertion struct {
	receiver string
	offset   int
}

// setupCalls are the testing helpers that panic in a parallel test, so a
// function using one is left alone. `t.Setenv` is the one that bites: it
// panics with "test using t.Setenv or t.Chdir can not use t.Parallel", and
// inserting the call would turn a passing test into a panic.
var setupCalls = map[string]bool{"Setenv": true, "Chdir": true}

// AddParallelCalls inserts a missing t.Parallel() into every test function
// and subtest closure in src that can take one. It returns nil when there
// was nothing to add, so a caller writes only what it changed.
//
// It is here for the same reason gofmt and goimports are: the project
// requires parallel tests, the edit is mechanical and semantics-preserving,
// and a model spends a whole gate round on it otherwise. 18 of the 96
// non-compile lint findings logged against a model were this one call.
//
// The splice is textual rather than a printed AST so comments, build tags,
// and formatting survive untouched; the parse only supplies offsets.
func AddParallelCalls(path string, src []byte) ([]byte, error) {
	// A fixture under testdata is data the toolchain ignores on purpose, and
	// rewriting one changes what a test asserts rather than how it runs.
	if !strings.HasSuffix(path, "_test.go") || underTestdata(path) {
		return nil, nil
	}

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		// A test that does not parse is the build gate's report to make.
		return nil, nil //nolint:nilerr // parsing is not this pass's job
	}

	if suppressed(file) {
		return nil, nil
	}

	points := insertionPoints(fset, file)
	if len(points) == 0 {
		return nil, nil
	}

	out := src
	// Applied back to front so an earlier splice cannot move a later offset.
	for i := len(points) - 1; i >= 0; i-- {
		at := points[i].offset
		if at > len(out) {
			return nil, fmt.Errorf("%w: offset %d past end of %s", errBadOffset, at, path)
		}

		call := []byte("\n" + points[i].receiver + ".Parallel()")
		out = append(out[:at:at], append(call, out[at:]...)...)
	}

	return out, nil
}

// suppressedDirectives are the nolint directives that say a human decided
// this file's tests must not be parallel. Honoring them at file scope rather
// than per function is deliberate: the directive sits on whichever line the
// linter pointed at, and a splice made anyway would be an edit against a
// stated decision.
var suppressedDirectives = []string{"nolint:paralleltest", "nolint:tparallel"}

func suppressed(file *ast.File) bool {
	for _, group := range file.Comments {
		for _, c := range group.List {
			for _, directive := range suppressedDirectives {
				if strings.Contains(c.Text, directive) {
					return true
				}
			}
		}
	}

	return false
}

func underTestdata(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "testdata" {
			return true
		}
	}

	return false
}

// insertionPoints are the splices to make, in source order: one just past
// the opening brace of every body that should gain the call.
func insertionPoints(fset *token.FileSet, file *ast.File) []insertion {
	var points []insertion

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		recv, ok := testReceiver(fn)
		if !ok {
			continue
		}

		if wantsParallel(fn.Body, recv) {
			points = append(points, insertion{
				receiver: recv, offset: fset.Position(fn.Body.Lbrace).Offset + 1,
			})
		}

		points = append(points, subtestPoints(fset, fn.Body)...)
	}

	return points
}

// subtestPoints finds the `t.Run(name, func(t *testing.T) { ... })` closures
// inside body that should gain the call.
func subtestPoints(fset *token.FileSet, body *ast.BlockStmt) []insertion {
	var points []insertion

	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isMethodCall(call, "Run") {
			return true
		}

		for _, arg := range call.Args {
			lit, ok := arg.(*ast.FuncLit)
			if !ok {
				continue
			}

			recv, named := paramReceiver(lit.Type.Params)
			if named && wantsParallel(lit.Body, recv) {
				points = append(points, insertion{
					receiver: recv, offset: fset.Position(lit.Body.Lbrace).Offset + 1,
				})
			}
		}

		return true
	})

	return points
}

// wantsParallel reports a body that neither calls recv.Parallel already nor
// uses a helper that would panic beside it.
func wantsParallel(body *ast.BlockStmt, recv string) bool {
	if body == nil {
		return false
	}

	return !callsParallel(body, recv) && !usesSetup(body)
}

// callsParallel looks for recv.Parallel() without descending into a nested
// closure, since a subtest has its own *testing.T and its call says nothing
// about this body.
func callsParallel(body *ast.BlockStmt, recv string) bool {
	found := false

	ast.Inspect(body, func(n ast.Node) bool {
		if _, isClosure := n.(*ast.FuncLit); isClosure {
			return false
		}

		if sel, ok := selectorOn(n, recv); ok && sel == "Parallel" {
			found = true
		}

		return true
	})

	return found
}

// usesSetup walks the whole subtree, closures included. Whether a subtest's
// t.Setenv really forbids a parallel parent is subtler than that, and
// under-fixing costs a gate round while over-fixing turns a passing test
// into a panic.
func usesSetup(body *ast.BlockStmt) bool {
	found := false

	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && setupCalls[sel.Sel.Name] {
			found = true
		}

		return true
	})

	return found
}

// selectorOn reports the method name when n is a call on the identifier recv.
func selectorOn(n ast.Node, recv string) (string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return "", false
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}

	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != recv {
		return "", false
	}

	return sel.Sel.Name, true
}

func isMethodCall(call *ast.CallExpr, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)

	return ok && sel.Sel.Name == name
}

// testReceiver names the `*testing.T` parameter of a `func TestXxx` that
// paralleltest asks about, and reports false for anything else. TestMain
// owns the process and never takes the call.
func testReceiver(fn *ast.FuncDecl) (string, bool) {
	if fn.Recv != nil || fn.Body == nil || fn.Name == nil {
		return "", false
	}

	name := fn.Name.Name
	if !strings.HasPrefix(name, "Test") || name == "TestMain" {
		return "", false
	}

	return paramReceiver(fn.Type.Params)
}

// paramReceiver names the sole `*testing.T` parameter of params. A blank or
// absent name has nothing to call the method on.
func paramReceiver(params *ast.FieldList) (string, bool) {
	if params == nil || len(params.List) != 1 || !isTestingT(params.List[0].Type) {
		return "", false
	}

	names := params.List[0].Names
	if len(names) != 1 || names[0].Name == "_" {
		return "", false
	}

	return names[0].Name, true
}

func isTestingT(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}

	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	pkg, ok := sel.X.(*ast.Ident)

	return ok && pkg.Name == "testing" && sel.Sel.Name == "T"
}
