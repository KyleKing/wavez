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

// declarations finds where the Go module at root declares name and returns
// each place as the target `wavez rename` takes: file.go:Name for a
// function, type, constant, or variable, and file.go:Type.Name for a
// method, a struct field, or an interface method. Paths are relative to
// root. It returns at most maxTargets, and stops when ctx ends.
func declarations(ctx context.Context, root, name string) []string {
	var (
		targets []string
		scanned int
		tree    = os.DirFS(root)
	)

	err := fs.WalkDir(tree, ".", func(path string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			// A directory that cannot be read declares nothing.
			return fs.SkipDir
		case ctx.Err() != nil || scanned >= maxScannedFiles || len(targets) >= maxTargets:
			return fs.SkipAll
		case entry.IsDir():
			if path != "." && skippedDir(tree, path, entry.Name()) {
				return fs.SkipDir
			}

			return nil
		case !strings.HasSuffix(entry.Name(), ".go"):
			return nil
		}

		scanned++

		for _, declared := range declaredIn(tree, path, name) {
			if len(targets) < maxTargets {
				targets = append(targets, path+":"+declared)
			}
		}

		return nil
	})
	if err != nil {
		return nil
	}

	return targets
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

// declaredIn returns the names under which one file declares name: the
// bare name, or Type.name for a member.
func declaredIn(tree fs.FS, file, name string) []string {
	src, err := fs.ReadFile(tree, file)
	if err != nil || len(src) > maxSourceBytes || !bytes.Contains(src, []byte(name)) {
		return nil
	}

	parsed, err := parser.ParseFile(token.NewFileSet(), file, src, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}

	var out []string

	for _, decl := range parsed.Decls {
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
