package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/imports"
)

// pkgFile is one parsed non-test source file of a package.
type pkgFile struct {
	path string
	fset *token.FileSet
	file *ast.File
	src  string
}

// loadPackage parses every top-level .go file in dir (non-recursive).
func loadPackage(dir string) (pkgName string, files []pkgFile, testFiles []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if strings.HasSuffix(e.Name(), "_test.go") {
			testFiles = append(testFiles, full)
			continue
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return "", nil, nil, err
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, full, b, parser.ParseComments)
		if err != nil {
			return "", nil, nil, fmt.Errorf("parse %s: %w", full, err)
		}
		if pkgName == "" {
			pkgName = f.Name.Name
		}
		files = append(files, pkgFile{path: full, fset: fset, file: f, src: string(b)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	sort.Strings(testFiles)
	if pkgName == "" {
		return "", nil, nil, fmt.Errorf("no .go files found in %s", dir)
	}
	return pkgName, files, testFiles, nil
}

// mainFile is the fallback target when an intent gives no `near` symbol:
// the file named after the package, else the first file alphabetically.
func mainFile(pkgName string, files []pkgFile) pkgFile {
	for _, f := range files {
		if filepath.Base(f.path) == pkgName+".go" {
			return f
		}
	}
	return files[0]
}

// findSymbolFile returns the file containing a top-level decl (func, method,
// type, or var/const) named name, and that decl's end byte offset in source.
func findSymbolFile(files []pkgFile, name string) (pf pkgFile, endOffset int, doc string, found bool) {
	for _, f := range files {
		for _, d := range f.file.Decls {
			switch decl := d.(type) {
			case *ast.FuncDecl:
				if decl.Name.Name == name {
					return f, f.fset.Position(decl.End()).Offset, declDocText(decl.Doc), true
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.Name == name {
							return f, f.fset.Position(decl.End()).Offset, declDocText(decl.Doc), true
						}
					case *ast.ValueSpec:
						for _, id := range s.Names {
							if id.Name == name {
								return f, f.fset.Position(decl.End()).Offset, declDocText(decl.Doc), true
							}
						}
					}
				}
			}
		}
	}
	return pkgFile{}, 0, "", false
}

func declDocText(g *ast.CommentGroup) string {
	if g == nil {
		return ""
	}
	return g.Text()
}

// insertAfter splices insertText into src right after offset, wrapped in
// blank lines, then gofmt's the result.
func insertAfter(src string, offset int, insertText string) (string, error) {
	out := src[:offset] + "\n\n" + strings.TrimRight(insertText, "\n") + "\n" + src[offset:]
	formatted, err := format.Source([]byte(out))
	if err != nil {
		return "", fmt.Errorf("format after insert: %w\n---\n%s", err, out)
	}
	return string(formatted), nil
}

// appendAtEOF splices insertText at the end of src (before trailing
// whitespace), then gofmt's the result.
func appendAtEOF(src, insertText string) (string, error) {
	trimmed := strings.TrimRight(src, "\n")
	out := trimmed + "\n\n" + strings.TrimRight(insertText, "\n") + "\n"
	formatted, err := format.Source([]byte(out))
	if err != nil {
		return "", fmt.Errorf("format after append: %w\n---\n%s", err, out)
	}
	return string(formatted), nil
}

// runImports applies goimports (import add/sort) and returns the result
// plus the set of import paths present after that weren't present before.
func runImports(path, before, after string) (string, []string, error) {
	fixed, err := imports.Process(path, []byte(after), nil)
	if err != nil {
		return "", nil, fmt.Errorf("goimports: %w", err)
	}
	return string(fixed), diffImports(before, string(fixed)), nil
}

func diffImports(before, after string) []string {
	b := importSet(before)
	a := importSet(after)
	var added []string
	for imp := range a {
		if !b[imp] {
			added = append(added, imp)
		}
	}
	sort.Strings(added)
	return added
}

func importSet(src string) map[string]bool {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ImportsOnly)
	if err != nil {
		return map[string]bool{}
	}
	set := map[string]bool{}
	for _, imp := range f.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		set[p] = true
	}
	return set
}

// docStyle inspects sibling top-level func decls to see whether the package
// consistently starts doc comments with the symbol's own name.
func docStyle(files []pkgFile) bool {
	total, named := 0, 0
	for _, f := range files {
		for _, d := range f.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Doc == nil {
				continue
			}
			total++
			first := strings.TrimPrefix(fd.Doc.List[0].Text, "// ")
			if strings.HasPrefix(first, fd.Name.Name+" ") {
				named++
			}
		}
	}
	return total == 0 || named*2 >= total
}
