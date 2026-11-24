package gate

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
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

	// An import taken out leaves the blank lines around it, and an empty
	// line is nothing an importer compiles against.
	var kept [][]byte

	for line := range bytes.SplitSeq(out.Bytes(), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			kept = append(kept, line)
		}
	}

	return bytes.Join(kept, []byte("\n")), true
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

	// A header or a C file changes the types cgo makes of it, which no Go
	// file's shape shows.
	if slices.ContainsFunc(changed, func(file string) bool { return slices.Contains(foreign, path.Ext(file)) }) {
		return false
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

		return !ok || !parsed || !bytes.Equal(before, after) || repointed([]byte(was.output), now)
	})
}

// foreign is the extensions of the sources a Go package compiles that are
// not Go.
var foreign = []string{".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".m", ".s", ".S"}

// repointed reports whether a name the file imported now means another
// package. The declarations then read the same and are not: `foo.T` in a
// signature is another type, and an importer that held the old one no
// longer compiles. An import that is new, or gone, changes no name the
// declarations already used. A file that does not parse is repointed.
func repointed(was, now []byte) bool {
	before, ok := imported(was)
	after, parsed := imported(now)

	if !ok || !parsed {
		return true
	}

	for name, pkg := range before {
		if moved, kept := after[name]; kept && moved != pkg {
			return true
		}
	}

	return false
}

// imported maps each name a file imports under to the path it imports: the
// name given, or the last element of the path, which is what the package is
// called by every convention this cannot check.
func imported(src []byte) (map[string]string, bool) {
	file, err := parser.ParseFile(token.NewFileSet(), "", src, parser.ImportsOnly)
	if err != nil {
		return nil, false
	}

	names := make(map[string]string, len(file.Imports))

	for _, spec := range file.Imports {
		pkg := strings.Trim(spec.Path.Value, "\"`")

		name := path.Base(pkg)
		if spec.Name != nil {
			name = spec.Name.Name
		}

		// A dot or a blank import gives the file no name to compare, and
		// either one arriving or leaving is a change of its own.
		if name == "." || name == "_" {
			name += pkg
		}

		names[name] = pkg
	}

	return names, true
}

// narrowed is the patterns that are the changed packages themselves. It is
// what has to be compiled for a change that only rewrote function bodies,
// whose importers compiled against the same declarations before and still
// do, and what a run that tests only near the change tests.
func narrowed(buildable, dirs []string) []string {
	var own []string

	for _, dir := range dirs {
		if slices.Contains(buildable, pattern(dir)) {
			own = append(own, pattern(dir))
		}
	}

	return own
}
