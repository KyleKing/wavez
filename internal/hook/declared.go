package hook

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"strings"
)

// The bounds on one search for a declaration. A hook waits on it, so a
// module past them is searched in part and a name not found in that part is
// not a rename the handler speaks up about.
const (
	maxScannedFiles = 5000
	maxSourceBytes  = 1 << 20
	maxTargets      = 3
)

// site is one place a Go module declares a name.
type site struct {
	// file is the declaring file, relative to the module root.
	file string
	// name is how `wavez rename` takes it: Name for a function, type,
	// constant, or variable, and Type.Name for a method, a struct field, or
	// an interface method.
	name string
}

// target is the site as the argument `wavez rename` takes.
func (s site) target() string { return s.file + ":" + s.name }

// member reports whether the site is a method or a field, which code
// reaches through a selector and never by its bare name.
func (s site) member() bool { return strings.Contains(s.name, ".") }

// dir is the package directory the site is in.
func (s site) dir() string { return path.Dir(s.file) }

// declarations finds where the Go module at root declares name. It stops
// after maxTargets sites, and when ctx ends.
func declarations(ctx context.Context, root, name string) []site {
	var (
		sites   []site
		scanned int
		tree    = os.DirFS(root)
	)

	err := fs.WalkDir(tree, ".", func(file string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			// A directory that cannot be read declares nothing.
			return fs.SkipDir
		case ctx.Err() != nil || scanned >= maxScannedFiles || len(sites) >= maxTargets:
			return fs.SkipAll
		case entry.IsDir():
			if file != "." && skippedDir(tree, file, entry.Name()) {
				return fs.SkipDir
			}

			return nil
		case !strings.HasSuffix(entry.Name(), ".go"):
			return nil
		}

		scanned++

		for _, declared := range declaredIn(tree, file, name) {
			if len(sites) < maxTargets {
				sites = append(sites, site{file: file, name: declared})
			}
		}

		return nil
	})
	if err != nil {
		return nil
	}

	return sites
}

// declaredInDir finds where the package directory dir of the module at
// root declares name, in the files directly in it.
func declaredInDir(ctx context.Context, root, dir, name string) []site {
	tree := os.DirFS(root)

	entries, err := fs.ReadDir(tree, dir)
	if err != nil {
		return nil
	}

	var sites []site

	for _, entry := range entries {
		if ctx.Err() != nil || len(sites) >= maxTargets {
			break
		}

		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}

		file := path.Join(dir, entry.Name())
		for _, declared := range declaredIn(tree, file, name) {
			sites = append(sites, site{file: file, name: declared})
		}
	}

	return sites
}

// skippedDir reports a directory the go tool does not build as part of the
// module: hidden or underscored, vendored, test data, or a module of its
// own.
func skippedDir(tree fs.FS, dir, name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}

	if name == "vendor" || name == "testdata" || name == "node_modules" {
		return true
	}

	_, err := fs.Stat(tree, path.Join(dir, moduleFile))

	return err == nil
}

// parsed reads one Go file that mentions name. It returns nil for a file
// that does not, that is too large, or that does not parse.
func parsed(tree fs.FS, file, name string) *ast.File {
	src, err := fs.ReadFile(tree, file)
	if err != nil || len(src) > maxSourceBytes || !bytes.Contains(src, []byte(name)) {
		return nil
	}

	syntax, err := parser.ParseFile(token.NewFileSet(), file, src, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}

	return syntax
}

// declaredIn returns the names under which one file declares name: the
// bare name, or Type.name for a member.
func declaredIn(tree fs.FS, file, name string) []string {
	syntax := parsed(tree, file, name)
	if syntax == nil {
		return nil
	}

	var out []string

	for _, decl := range syntax.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.Name != name {
				continue
			}

			if owner := receiver(d); owner != "" {
				out = append(out, owner+"."+name)

				continue
			}

			out = append(out, name)
		case *ast.GenDecl:
			out = append(out, declaredBy(d, name)...)
		}
	}

	return out
}

// mentions reports whether one file uses name the way the site is reached:
// through a selector for a member, as an identifier for anything else. A
// local variable of the same name counts as a use, since telling them apart
// takes the type checker.
func mentions(tree fs.FS, file, name string, member bool) bool {
	syntax := parsed(tree, file, name)
	if syntax == nil {
		return false
	}

	found := false

	ast.Inspect(syntax, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.SelectorExpr:
			found = found || (member && n.Sel.Name == name)

			// The selected name is not a bare use of it.
			ast.Inspect(n.X, func(inner ast.Node) bool {
				ident, ok := inner.(*ast.Ident)
				found = found || (ok && !member && ident.Name == name)

				return !found
			})

			return false
		case *ast.Ident:
			found = found || (!member && n.Name == name)
		}

		return !found
	})

	return found
}

// receiver returns the type a method is declared on, and "" for a function.
func receiver(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return ""
	}

	expr := d.Recv.List[0].Type

	for {
		switch t := expr.(type) {
		case *ast.StarExpr:
			expr = t.X
		case *ast.IndexExpr:
			expr = t.X
		case *ast.IndexListExpr:
			expr = t.X
		case *ast.Ident:
			return t.Name
		default:
			return ""
		}
	}
}

// declaredBy reads one const, var, or type declaration.
func declaredBy(d *ast.GenDecl, name string) []string {
	var out []string

	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.ValueSpec:
			for _, ident := range s.Names {
				if ident.Name == name {
					out = append(out, name)
				}
			}
		case *ast.TypeSpec:
			if s.Name.Name == name {
				out = append(out, name)
			}

			if hasMember(s.Type, name) {
				out = append(out, s.Name.Name+"."+name)
			}
		}
	}

	return out
}

// hasMember reports whether a struct or interface type has a field or
// method of the given name.
func hasMember(expr ast.Expr, name string) bool {
	var fields *ast.FieldList

	switch t := expr.(type) {
	case *ast.StructType:
		fields = t.Fields
	case *ast.InterfaceType:
		fields = t.Methods
	}

	if fields == nil {
		return false
	}

	for _, field := range fields.List {
		for _, ident := range field.Names {
			if ident.Name == name {
				return true
			}
		}
	}

	return false
}
