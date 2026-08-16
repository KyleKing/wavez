package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
)

func runLike(it *Intent, r *Result) error {
	pkgName, files, testFiles, err := loadPackage(it.PkgDir)
	if err != nil {
		return err
	}
	_ = pkgName

	target, srcDecl, ok := findFuncDecl(files, it.Src)
	if !ok {
		return fmt.Errorf("like: source function/method %s not found in %s", it.Src, it.PkgDir)
	}

	variants := map[string]string{
		it.Src:             it.Dst,
		lowerFirst(it.Src): lowerFirst(it.Dst),
	}

	docText := ""
	if srcDecl.Doc != nil {
		docText = renderDocComment(wordReplaceAll(srcDecl.Doc.Text(), variants))
	}

	startOff := target.fset.Position(srcDecl.Pos()).Offset
	endOff := target.fset.Position(srcDecl.End()).Offset
	srcText := target.src[startOff:endOff]

	mirroredText, err := mirrorFuncText(srcText, it.Src, variants, docText)
	if err != nil {
		return err
	}

	after, err := insertAfter(target.src, endOff, mirroredText)
	if err != nil {
		return err
	}
	after, added, err := runImports(target.path, target.src, after)
	if err != nil {
		return err
	}
	r.ImportsAdded = append(r.ImportsAdded, added...)
	r.record(target.path, target.src, after)
	if err := os.WriteFile(target.path, []byte(after), 0o644); err != nil {
		return err
	}

	if testPath, testFset, testDecl, ok := findTestFunc(testFiles, "Test"+it.Src); ok {
		if err := mirrorTest(testPath, testFset, testDecl, it, r); err != nil {
			return err
		}
	}
	return nil
}

// renderDocComment turns *ast.CommentGroup.Text() (comment markers already
// stripped) back into "// "-prefixed lines with a trailing newline.
func renderDocComment(text string) string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = "//"
		} else {
			lines[i] = "// " + l
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func wordReplaceAll(text string, variants map[string]string) string {
	for from, to := range variants {
		text = wordReplace(text, from, to)
	}
	return text
}

func wordReplace(text, from, to string) string {
	if from == "" {
		return text
	}
	return regexp.MustCompile(`\b`+regexp.QuoteMeta(from)+`\b`).ReplaceAllString(text, to)
}

// renameDecl parses a standalone function's source, renames identifiers per
// variants, and reparses the formatted result into a fresh, self-consistent
// FuncDecl (so later ast.Inspect/format.Node calls see clean positions).
func renameDecl(srcText string, variants map[string]string) (*token.FileSet, *ast.FuncDecl, error) {
	fset1 := token.NewFileSet()
	file1, err := parser.ParseFile(fset1, "", "package p\n"+srcText, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("parse source decl: %w", err)
	}
	decl1 := file1.Decls[0].(*ast.FuncDecl)
	decl1.Doc = nil

	ast.Inspect(decl1, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			if to, found := variants[id.Name]; found {
				id.Name = to
			}
		}
		return true
	})

	var buf strings.Builder
	if err := format.Node(&buf, fset1, decl1); err != nil {
		return nil, nil, fmt.Errorf("format renamed decl: %w", err)
	}

	fset2 := token.NewFileSet()
	file2, err := parser.ParseFile(fset2, "", "package p\n"+buf.String(), parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("reparse renamed decl: %w", err)
	}
	return fset2, file2.Decls[0].(*ast.FuncDecl), nil
}

// mirrorFuncText renames identifiers in a standalone function's source,
// then keeps only its err-handling skeleton (assignments that set err,
// `if err != nil` guards, and the final return) marking the rest as a hole.
func mirrorFuncText(srcText, srcName string, variants map[string]string, docText string) (string, error) {
	fset2, decl2, err := renameDecl(srcText, variants)
	if err != nil {
		return "", err
	}

	sigOnly := &ast.FuncDecl{Recv: decl2.Recv, Name: decl2.Name, Type: decl2.Type, Body: &ast.BlockStmt{}}
	var sigBuf strings.Builder
	if err := format.Node(&sigBuf, fset2, sigOnly); err != nil {
		return "", fmt.Errorf("format signature: %w", err)
	}
	sigLine := strings.TrimSpace(strings.SplitN(sigBuf.String(), "{", 2)[0]) + " {"

	kept := keepErrSkeleton(fset2, decl2.Body.List)

	var out strings.Builder
	if docText != "" {
		out.WriteString(docText)
	}
	out.WriteString(sigLine)
	out.WriteString("\n\t// HOLE: mirror of " + srcName + "\n")
	for _, s := range kept {
		out.WriteString("\t" + s + "\n")
	}
	out.WriteString("}\n")
	return out.String(), nil
}

// keepErrSkeleton keeps assignments that set err, `if err...` guards, and a
// trailing return statement, dropping everything else in the body.
func keepErrSkeleton(fset *token.FileSet, stmts []ast.Stmt) []string {
	var kept []string
	for i, s := range stmts {
		var b strings.Builder
		if err := format.Node(&b, fset, s); err != nil {
			continue
		}
		text := b.String()
		isLast := i == len(stmts)-1
		switch st := s.(type) {
		case *ast.AssignStmt:
			if assignsErr(st) {
				kept = append(kept, text)
			}
		case *ast.IfStmt:
			if strings.Contains(text, "err") {
				kept = append(kept, text)
			}
		case *ast.ReturnStmt:
			if isLast {
				kept = append(kept, text)
			}
		}
	}
	return kept
}

func assignsErr(a *ast.AssignStmt) bool {
	for _, lhs := range a.Lhs {
		if id, ok := lhs.(*ast.Ident); ok && id.Name == "err" {
			return true
		}
	}
	return false
}

func findFuncDecl(files []pkgFile, name string) (pkgFile, *ast.FuncDecl, bool) {
	for _, f := range files {
		for _, d := range f.file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == name {
				return f, fd, true
			}
		}
	}
	return pkgFile{}, nil, false
}

func findTestFunc(testFiles []string, name string) (string, *token.FileSet, *ast.FuncDecl, bool) {
	for _, path := range testFiles {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, b, parser.ParseComments)
		if err != nil {
			continue
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == name {
				return path, fset, fd, true
			}
		}
	}
	return "", nil, nil, false
}

// mirrorTest renames TestSRC into TestDST, keeping the whole body: unlike
// the production skeleton, a test's assertions are the point, so nothing is
// dropped, only flagged for review.
func mirrorTest(testPath string, fset *token.FileSet, testDecl *ast.FuncDecl, it *Intent, r *Result) error {
	before, err := os.ReadFile(testPath)
	if err != nil {
		return err
	}
	startOff := fset.Position(testDecl.Pos()).Offset
	endOff := fset.Position(testDecl.End()).Offset
	srcText := string(before)[startOff:endOff]

	variants := map[string]string{
		"Test" + it.Src:    "Test" + it.Dst,
		it.Src:             it.Dst,
		lowerFirst(it.Src): lowerFirst(it.Dst),
	}

	fset2, decl2, err := renameDecl(srcText, variants)
	if err != nil {
		return err
	}
	var buf strings.Builder
	if err := format.Node(&buf, fset2, decl2); err != nil {
		return fmt.Errorf("format renamed test: %w", err)
	}
	mirrored := "// HOLE: verify assertions mirrored from Test" + it.Src + " still apply\n" + buf.String() + "\n"

	src := string(before)
	after, err := appendAtEOF(src, mirrored)
	if err != nil {
		return err
	}
	after, added, err := runImports(testPath, src, after)
	if err != nil {
		return err
	}
	r.ImportsAdded = append(r.ImportsAdded, added...)
	r.Warnings = append(r.Warnings, "mirrored test body kept Test"+it.Src+"'s assertions verbatim (renamed only); review before trusting")
	r.record(testPath, src, after)
	return os.WriteFile(testPath, []byte(after), 0o644)
}
