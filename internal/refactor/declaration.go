package refactor

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

type kind int

const (
	kindFunc kind = iota
	kindMethod
	kindType
	kindConst
	kindVar
	kindField
)

func (k kind) String() string {
	switch k {
	case kindFunc:
		return "func"
	case kindMethod:
		return "method"
	case kindType:
		return "type"
	case kindConst:
		return "const"
	case kindVar:
		return "var"
	case kindField:
		return "field"
	default:
		return "declaration"
	}
}

// declaration is one named thing a file declares. For a name inside a
// const, var, or type declaration the span is the whole declaration, and
// siblings lists every name sharing it.
type declaration struct {
	file *parsedFile
	node ast.Node
	name string
	recv string
	// siblings holds every name of the enclosing declaration when it
	// declares more than one.
	siblings []string
	kind     kind
	// ident is the byte offset of the name, which is the position gopls is
	// asked about.
	ident int
	// start and end bound the bytes to move or delete: the doc comment
	// through any comment trailing the last line.
	start, end int
}

// line is the line the name is declared on.
func (d *declaration) line() int { return d.file.lineAt(d.ident) }

// base is the name without its receiver or type qualifier.
func (d *declaration) base() string {
	if i := strings.LastIndexByte(d.name, '.'); i >= 0 {
		return d.name[i+1:]
	}

	return d.name
}

func (d *declaration) at() string { return d.file.rel + ":" + strconv.Itoa(d.line()) }

func (d *declaration) target() string { return d.file.rel + ":" + d.name }

func (d *declaration) contains(file string, line int) bool {
	return d.file.rel == file && line >= d.file.lineAt(d.start) && line <= d.file.lineAt(d.end)
}

// declarationsOf lists the top-level declarations of a file in source
// order, leaving out imports and blank names.
func declarationsOf(pf *parsedFile) []declaration {
	var out []declaration

	for _, node := range pf.ast.Decls {
		switch n := node.(type) {
		case *ast.FuncDecl:
			out = append(out, funcDeclaration(pf, n))
		case *ast.GenDecl:
			out = append(out, genDeclarations(pf, n)...)
		}
	}

	return out
}

func funcDeclaration(pf *parsedFile, fn *ast.FuncDecl) declaration {
	d := newDeclaration(pf, fn, fn.Doc, fn.Name)
	d.kind = kindFunc

	if recv := receiverName(fn); recv != "" {
		d.kind = kindMethod
		d.recv = recv
		d.name = recv + "." + fn.Name.Name
	}

	return d
}

func genDeclarations(pf *parsedFile, gen *ast.GenDecl) []declaration {
	if gen.Tok == token.IMPORT {
		return nil
	}

	var (
		out   []declaration
		names []string
	)

	for _, ident := range specNames(gen) {
		d := newDeclaration(pf, gen, gen.Doc, ident)
		d.kind = kindOf(gen.Tok)
		out = append(out, d)
		names = append(names, ident.Name)
	}

	if len(out) > 1 {
		for i := range out {
			out[i].siblings = names
		}
	}

	return out
}

func kindOf(tok token.Token) kind {
	switch tok {
	case token.TYPE:
		return kindType
	case token.CONST:
		return kindConst
	default:
		return kindVar
	}
}

func specNames(gen *ast.GenDecl) []*ast.Ident {
	var out []*ast.Ident

	for _, spec := range gen.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			out = append(out, s.Name)
		case *ast.ValueSpec:
			for _, name := range s.Names {
				if name.Name != "_" {
					out = append(out, name)
				}
			}
		}
	}

	return out
}

// fieldsOf lists the struct fields and interface methods a file's types
// declare, each named Type.Member. Rename reaches these; move and delete do
// not, because a member is not a declaration that stands alone.
func fieldsOf(pf *parsedFile) []declaration {
	var out []declaration

	ast.Inspect(pf.ast, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}

		for _, ident := range memberNames(spec.Type) {
			d := newDeclaration(pf, spec, nil, ident)
			d.kind = kindField
			d.recv = spec.Name.Name
			d.name = spec.Name.Name + "." + ident.Name
			out = append(out, d)
		}

		return false
	})

	return out
}

func memberNames(expr ast.Expr) []*ast.Ident {
	var list *ast.FieldList

	switch t := expr.(type) {
	case *ast.StructType:
		list = t.Fields
	case *ast.InterfaceType:
		list = t.Methods
	}

	if list == nil {
		return nil
	}

	var out []*ast.Ident
	for _, field := range list.List {
		out = append(out, field.Names...)
	}

	return out
}

func newDeclaration(pf *parsedFile, node ast.Node, doc *ast.CommentGroup, ident *ast.Ident) declaration {
	start := node.Pos()
	if doc != nil {
		start = doc.Pos()
	}

	end := trailingComment(pf, node.End())

	return declaration{
		file: pf, node: node, name: ident.Name,
		ident: pf.offset(ident.Pos()), start: pf.offset(start), end: pf.offset(end),
	}
}

// trailingComment extends end over a comment that starts on the same line,
// so a move or delete does not leave `// why` behind on a line of its own.
func trailingComment(pf *parsedFile, end token.Pos) token.Pos {
	line := pf.line(end)

	for _, group := range pf.ast.Comments {
		if group.Pos() >= end && pf.line(group.Pos()) == line {
			return group.End()
		}
	}

	return end
}

// receiverName is the base type a method is declared on, without its
// pointer or type parameters.
func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}

	expr := fn.Recv.List[0].Type

	for {
		switch t := expr.(type) {
		case *ast.StarExpr:
			expr = t.X
		case *ast.ParenExpr:
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

// hasDeclarations reports whether a file declares anything besides imports.
func hasDeclarations(file *ast.File) bool {
	for _, node := range file.Decls {
		if gen, ok := node.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			continue
		}

		return true
	}

	return false
}
