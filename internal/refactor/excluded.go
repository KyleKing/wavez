package refactor

import (
	"bytes"
	"context"
	"go/ast"
	"go/token"
	"path"
	"sort"
	"strconv"
	"strings"
)

// usage is what a use of a renamed symbol looks like to syntax alone, which
// is all there is for a file the build leaves out: gopls never loaded it,
// so nothing ties its identifiers to the symbol.
type usage struct {
	old        string
	recv       string
	pkg        string
	dir        string
	importPath string
	member     bool
	field      bool
}

func usageOf(decl *declaration, module string) usage {
	u := usage{
		old: decl.base(), recv: decl.recv, pkg: decl.file.ast.Name.Name, dir: path.Dir(decl.file.rel),
		member: decl.kind == kindMethod || decl.kind == kindField, field: decl.kind == kindField,
	}

	// A _test package and package main are imported by nobody.
	if !strings.HasSuffix(u.pkg, "_test") && u.pkg != mainName {
		u.importPath = path.Join(module, u.dir)
	}

	return u
}

// useFinder walks one file for uses of a symbol. A package-level name
// counts bare in its own package and qualified in a file that imports it.
// A method or field counts wherever it is selected in either, which no
// syntax can narrow to its type.
type useFinder struct {
	// qualifiers are the names the file imports the symbol's package
	// under, and packages the names of everything it imports.
	qualifiers map[string]bool
	packages   map[string]bool
	// skip holds identifiers that are never a package-level name: selected
	// members, method names, fields, parameters, and literal keys.
	skip map[*ast.Ident]bool
	hits []token.Pos
	usage
	// own is set for a file of the symbol's own package, or one that dot
	// imports it.
	own bool
}

func (f *useFinder) visit(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.SelectorExpr:
		f.skip[n.Sel] = true

		if x, isIdent := n.X.(*ast.Ident); n.Sel.Name == f.old && f.selects(x, isIdent) {
			f.hits = append(f.hits, n.Sel.Pos())
		}
	case *ast.FuncDecl:
		f.method(n)
	case *ast.Field:
		for _, name := range n.Names {
			f.skip[name] = true
		}
	case *ast.KeyValueExpr:
		f.key(n)
	case *ast.Ident:
		if !f.member && f.own && n.Name == f.old && !f.skip[n] {
			f.hits = append(f.hits, n.Pos())
		}
	}

	return true
}

// method counts a method declaration that is the renamed method on another
// build, and keeps any method's name from counting as a package-level one.
func (f *useFinder) method(fn *ast.FuncDecl) {
	if fn.Recv == nil {
		return
	}

	f.skip[fn.Name] = true

	if f.member && f.own && fn.Name.Name == f.old && receiverName(fn) == f.recv {
		f.hits = append(f.hits, fn.Name.Pos())
	}
}

// key counts a composite literal key that names a renamed field.
func (f *useFinder) key(pair *ast.KeyValueExpr) {
	key, ok := pair.Key.(*ast.Ident)
	if !ok {
		return
	}

	f.skip[key] = true

	if f.field && key.Name == f.old {
		f.hits = append(f.hits, key.Pos())
	}
}

// selects reports whether a selector on x could be the symbol: x names the
// symbol's package for a package-level name, and is anything but a package
// for a member.
func (f *useFinder) selects(x *ast.Ident, isIdent bool) bool {
	if f.member {
		return !isIdent || !f.packages[x.Name]
	}

	return isIdent && f.qualifiers[x.Name]
}

// lines returns the lines of a file that use the symbol by its old name.
func (u usage) lines(pf *parsedFile) []int {
	f := &useFinder{usage: u, qualifiers: map[string]bool{}, packages: map[string]bool{}, skip: map[*ast.Ident]bool{}}
	f.own = path.Dir(pf.rel) == u.dir && pf.ast.Name.Name == u.pkg

	for _, spec := range pf.ast.Imports {
		f.addImport(spec)
	}

	if !f.own && len(f.qualifiers) == 0 {
		return nil
	}

	ast.Inspect(pf.ast, f.visit)

	var out []int

	for _, pos := range f.hits {
		if line := pf.line(pos); len(out) == 0 || out[len(out)-1] != line {
			out = append(out, line)
		}
	}

	return out
}

// addImport records how one import of the file names its package.
func (f *useFinder) addImport(spec *ast.ImportSpec) {
	importPath, err := strconv.Unquote(spec.Path.Value)
	if err != nil {
		return
	}

	name := path.Base(importPath)
	if importPath == f.importPath {
		name = f.pkg
	}

	if spec.Name != nil {
		name = spec.Name.Name
	}

	f.packages[name] = true

	switch {
	case importPath != f.importPath || name == "_":
	case name == ".":
		f.own = true
	default:
		f.qualifiers[name] = true
	}
}

// excluded finds the uses of a renamed symbol in files the build leaves
// out: another GOOS or GOARCH, a build tag that is off, cgo. A rename
// reaches only what gopls loads, so these keep the old name. It reports
// false when the go tool could not say which files the build holds, and
// asks it only once some file is a candidate.
func (s *session) excluded(ctx context.Context, files []string, decl *declaration) ([]Mention, bool) {
	u := usageOf(decl, s.tree.modulePath())

	var found []Mention

	for _, file := range files {
		src, err := s.tree.read(file)
		if err != nil || !bytes.Contains(src, []byte(u.old)) {
			continue
		}

		pf, err := parseSource(file, src)
		if err != nil {
			continue
		}

		for _, line := range u.lines(pf) {
			found = append(found, Mention{File: file, Line: line})
		}
	}

	if len(found) == 0 {
		return nil, true
	}

	built, err := s.tools.builtFiles(ctx)
	if err != nil {
		return nil, false
	}

	var out []Mention

	for _, m := range found {
		if !built[m.File] {
			out = append(out, m)
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].File < out[j].File })

	return out, true
}
