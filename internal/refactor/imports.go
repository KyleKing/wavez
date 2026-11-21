package refactor

import (
	"bytes"
	"go/ast"
	"go/token"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// importClass orders the groups of an import block the way gofmt-style
// tooling expects them: standard library, third party, this module.
type importClass int

const (
	classStd importClass = iota
	classThird
	classLocal
)

func classify(importPath, module string) importClass {
	first, _, _ := strings.Cut(importPath, "/")

	switch {
	case module != "" && (importPath == module || strings.HasPrefix(importPath, module+"/")):
		return classLocal
	case !strings.Contains(first, "."):
		return classStd
	default:
		return classThird
	}
}

// importSpec is one import as its file wrote it.
type importSpec struct {
	name string
	path string
	text string
}

func specOf(pf *parsedFile, spec *ast.ImportSpec) importSpec {
	out := importSpec{text: string(pf.src[pf.offset(spec.Pos()):pf.offset(spec.End())])}
	if spec.Name != nil {
		out.name = spec.Name.Name
	}

	if unquoted, err := strconv.Unquote(spec.Path.Value); err == nil {
		out.path = unquoted
	}

	return out
}

var versionSuffix = regexp.MustCompile(`^v\d+$`)

// packageNames guesses what an unnamed import is called in the file. The
// real name lives in the imported package's own clause, so this lists the
// spellings path conventions produce; a miss is caught by the import fix-up
// and then by the build check.
func packageNames(importPath string) []string {
	base := path.Base(importPath)
	if versionSuffix.MatchString(base) {
		base = path.Base(path.Dir(importPath))
	}

	base, _, _ = strings.Cut(base, ".")
	trimmed := strings.TrimSuffix(strings.TrimPrefix(base, "go-"), "-go")

	return []string{base, trimmed, strings.ReplaceAll(trimmed, "-", "_")}
}

// neededImports picks the imports of a file that the given declarations
// refer to, judged by the package qualifiers their syntax uses. Blank and
// dot imports are left where they are.
func neededImports(pf *parsedFile, decls []declaration) []importSpec {
	used := map[string]bool{}

	for i := range decls {
		ast.Inspect(decls[i].node, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if ident, isIdent := sel.X.(*ast.Ident); isIdent {
					used[ident.Name] = true
				}
			}

			return true
		})
	}

	var out []importSpec

	for _, raw := range pf.ast.Imports {
		spec := specOf(pf, raw)

		names := []string{spec.name}
		if spec.name == "" {
			names = packageNames(spec.path)
		}

		for _, name := range names {
			if used[name] {
				out = append(out, spec)

				break
			}
		}
	}

	return out
}

// addImport returns src with one more import, placed in the group of its
// class so the block keeps its standard, third-party, local order. It is a
// no-op when the file already imports the path under the same name.
func addImport(rel string, src []byte, spec importSpec, module string) ([]byte, error) {
	pf, err := parseSource(rel, src)
	if err != nil {
		return nil, err
	}

	var block *ast.GenDecl

	for _, raw := range pf.ast.Imports {
		if have := specOf(pf, raw); have.path == spec.path && have.name == spec.name {
			return src, nil
		}
	}

	for _, node := range pf.ast.Decls {
		if gen, ok := node.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			block = gen

			break
		}
	}

	switch {
	case block == nil:
		at := pf.lineEnd(pf.offset(pf.ast.Name.End()))

		return splice(src, at, at, "\n\nimport (\n\t"+spec.text+"\n)"), nil
	case !block.Lparen.IsValid():
		only := block.Specs[0]
		text := "import (\n\t" + string(src[pf.offset(only.Pos()):pf.offset(only.End())]) + "\n)"

		return addImport(rel, splice(src, pf.offset(block.Pos()), pf.offset(only.End()), text), spec, module)
	default:
		return insertIntoBlock(pf, block, spec, module), nil
	}
}

func insertIntoBlock(pf *parsedFile, block *ast.GenDecl, spec importSpec, module string) []byte {
	line := "\t" + spec.text + "\n"
	if len(block.Specs) == 0 {
		return splice(pf.src, pf.offset(block.Lparen), pf.offset(block.Rparen)+1, "(\n"+line+")")
	}

	class := classify(spec.path, module)

	var (
		anchor      ast.Spec
		anchorClass importClass
	)

	for _, raw := range block.Specs {
		imp, ok := raw.(*ast.ImportSpec)
		if !ok {
			continue
		}

		if c := classify(specOf(pf, imp).path, module); c <= class {
			anchor, anchorClass = raw, c
		}
	}

	if anchor == nil {
		first := pf.offset(block.Specs[0].Pos())
		at := bytes.LastIndexByte(pf.src[:first], '\n') + 1

		return splice(pf.src, at, at, line+"\n")
	}

	at := pf.lineEnd(pf.offset(anchor.End())) + 1
	if anchorClass == class {
		return splice(pf.src, at, at, line)
	}

	next := strings.TrimSpace(string(pf.src[at:pf.lineEnd(at)]))
	if next != "" && !strings.HasPrefix(next, ")") {
		line += "\n"
	}

	return splice(pf.src, at, at, "\n"+line)
}

// splice returns src with the bytes from start to end replaced by text.
func splice(src []byte, start, end int, text string) []byte {
	out := make([]byte, 0, len(src)+len(text))
	out = append(out, src[:start]...)
	out = append(out, text...)

	return append(out, src[end:]...)
}
