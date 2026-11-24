package gate

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// shapeBudget bounds reading the files a change touched as they were.
const shapeBudget = 5 * time.Second

// headRef is what a change is compared with when no session says where it
// started.
const headRef = "HEAD"

// shape is a Go source file with its imports and the inside of every
// function body removed: the declarations, and every comment and directive
// outside a body, byte for byte. Two versions of a file with one shape give
// every importer the same thing to compile against, so a package that
// imports one and compiled before the change compiles after it. An import
// the file gained can break only its own package, by a cycle or a name that
// does not resolve, and that package is always compiled. It reports false
// for a file that does not parse.
func shape(src []byte) ([]byte, bool) {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}

	var (
		out  bytes.Buffer
		from int
	)

	// cut drops src[open:shut] from what is kept.
	cut := func(open, shut int) bool {
		if open < from || shut < open || shut > len(src) {
			return false
		}

		out.Write(src[from:open])
		from = shut

		return true
	}

	for _, decl := range file.Decls {
		open, shut := 0, 0

		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok != token.IMPORT {
				continue
			}

			open, shut = fset.Position(d.Pos()).Offset, fset.Position(d.End()).Offset
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}

			open, shut = fset.Position(d.Body.Lbrace).Offset+1, fset.Position(d.Body.Rbrace).Offset
		}

		if !cut(open, shut) {
			return nil, false
		}
	}

	out.Write(src[from:])

	return out.Bytes(), true
}

// bodiesOnly reports whether the change left every importer of the changed
// packages the same thing to compile against: each changed Go file that is
// not a test exists on both sides with one shape. Test files and files that
// are not Go are never part of what an importer compiles. Anything that
// cannot be read, a baseline outside git included, is a change that might
// reach an importer.
func (r *runner) bodiesOnly(ctx context.Context, root string, changed []string) bool {
	git, err := r.lookPath("git")
	if err != nil {
		return false
	}

	ref := headRef
	if base, ok := baselineFrom(ctx); ok {
		ref = base.ref()
	}

	return !slices.ContainsFunc(goFiles(changed), func(file string) bool {
		if strings.HasSuffix(file, "_test.go") {
			return false
		}

		//nolint:gosec // a changed file of the project
		now, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return true
		}

		// The ./ reads the path from root, which need not be the top of the
		// repository.
		was := run(ctx, invocation{
			dir: root, name: git, timeout: shapeBudget, args: []string{"show", ref + ":./" + file},
		})
		if was.reason != "" || was.exit != 0 {
			return true
		}

		before, ok := shape([]byte(was.output))
		after, parsed := shape(now)

		return !ok || !parsed || !bytes.Equal(before, after)
	})
}

// narrowed is what has to be compiled for a change that only rewrote
// function bodies: the changed packages themselves. Their importers compiled
// against the same declarations before and still do.
func narrowed(buildable, dirs []string) []string {
	var own []string

	for _, dir := range dirs {
		if slices.Contains(buildable, pattern(dir)) {
			own = append(own, pattern(dir))
		}
	}

	return own
}
