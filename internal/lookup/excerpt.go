package lookup

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kyleking/wavez/internal/apply"
)

const (
	// The excerptBudget bound is how many bytes of source one prompt is
	// handed. What a session is handed it pays for on every later turn, and
	// past this the model is better off reading the file it needs.
	excerptBudget = 8000
	// The declLines bound is how many lines of a declaration are shown when
	// its end has to be guessed from indentation, and around is how many
	// lines either side of a mention in a test file are.
	declLines = 80
	around    = 8
	// The maxUsed bound is how many of the declarations a function reads
	// are shown with it.
	maxUsed = 6
)

// span is a run of lines of one file, first and last counted from one.
type span struct{ first, last int }

// piece is one run of lines of one file to show.
type piece struct {
	file  string
	lines []string
	span  span
}

// Excerpts is the source the sites point at, as numbered lines under a line
// naming each file: every declaration whole, then the parts of the test
// package's variables, constants, and types a Go function reads, then the
// parts of the test files that mention it. It is cut off, with a line
// saying so, at the budget.
func Excerpts(root string, sites []Site) string {
	var (
		out  strings.Builder
		seen = map[string]bool{}
	)

	for _, p := range slices.Concat(declarations(root, sites), used(root, sites), tested(root, sites)) {
		key := p.file + ":" + strconv.Itoa(p.span.first)
		if seen[key] || p.span.first < 1 || p.span.last > len(p.lines) {
			continue
		}

		seen[key] = true

		block := numbered(p.file, p.span, p.lines)
		if out.Len()+len(block) > excerptBudget {
			out.WriteString("(more left out: read " + p.file + " for the rest)\n")

			break
		}

		out.WriteString(block)
	}

	return out.String()
}

// declarations is each site's declaration.
func declarations(root string, sites []Site) []piece {
	var out []piece

	for _, site := range sites {
		if lines, ok := read(root, site.File); ok && site.Line >= 1 && site.Line <= len(lines) {
			out = append(out, piece{file: site.File, lines: lines, span: declaration(site.File, lines, site.Line)})
		}
	}

	return out
}

// used is the variables, constants, and types of its own package that each
// Go function among the sites reads. A change to what a function does is as
// often a change to the table it walks as to the function.
func used(root string, sites []Site) []piece {
	var out []piece

	for _, site := range sites {
		if !strings.HasSuffix(site.File, ".go") {
			continue
		}

		lines, ok := read(root, site.File)
		if !ok {
			continue
		}

		names := reads(lines, site.Line)
		if len(names) == 0 {
			continue
		}

		out = append(out, valuesIn(root, filepath.ToSlash(filepath.Dir(filepath.FromSlash(site.File))), names)...)
	}

	return out[:min(len(out), maxUsed)]
}

// valuesIn is the declarations that go by one of names in the Go files, tests
// aside, directly in dir.
func valuesIn(root, dir string, names map[string]bool) []piece {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		return nil
	}

	var out []piece

	for _, entry := range entries {
		file := dir + "/" + entry.Name()
		if entry.IsDir() || !strings.HasSuffix(file, ".go") || testFile.MatchString(file) {
			continue
		}

		if body, ok := read(root, file); ok {
			for _, s := range values(body, names) {
				out = append(out, piece{file: file, lines: body, span: s})
			}
		}
	}

	return out
}

// reads is the names the Go function declared around line refers to, other
// than the fields and methods it selects.
func reads(lines []string, line int) map[string]bool {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "", strings.Join(lines, "\n"), parser.SkipObjectResolution)
	if err != nil {
		return nil
	}

	names := map[string]bool{}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || line < fset.Position(fn.Pos()).Line || line > fset.Position(fn.End()).Line {
			continue
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.SelectorExpr:
				ast.Inspect(node.X, func(x ast.Node) bool {
					if id, ok := x.(*ast.Ident); ok {
						names[id.Name] = true
					}

					return true
				})

				return false
			case *ast.Ident:
				names[node.Name] = true
			}

			return true
		})
	}

	return names
}

// values is the spans of a Go file's package-level variables, constants,
// and types that go by one of names, each with its comment. A declaration
// in a parenthesized group is shown alone.
func values(lines []string, names map[string]bool) []span {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "", strings.Join(lines, "\n"), parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil
	}

	var spans []span

	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok == token.IMPORT {
			continue
		}

		for _, spec := range gen.Specs {
			if !named(spec, names) {
				continue
			}

			var node ast.Node = gen

			doc := gen.Doc
			if gen.Lparen.IsValid() {
				node, doc = spec, specDoc(spec)
			}

			s := span{first: fset.Position(node.Pos()).Line, last: fset.Position(node.End()).Line}
			if doc != nil {
				s.first = fset.Position(doc.Pos()).Line
			}

			spans = append(spans, s)
		}
	}

	return spans
}

func specDoc(spec ast.Spec) *ast.CommentGroup {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return s.Doc
	case *ast.ValueSpec:
		return s.Doc
	}

	return nil
}

func named(spec ast.Spec, names map[string]bool) bool {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return names[s.Name.Name]
	case *ast.ValueSpec:
		return slices.ContainsFunc(s.Names, func(id *ast.Ident) bool { return names[id.Name] })
	}

	return false
}

// tested is the parts of each site's test files that mention it.
func tested(root string, sites []Site) []piece {
	var out []piece

	for _, site := range sites {
		for _, file := range site.Tests {
			lines, ok := read(root, file)
			if !ok {
				continue
			}

			for _, s := range mentions(file, lines, site.Name) {
				out = append(out, piece{file: file, lines: lines, span: s})
			}
		}
	}

	return out
}

func read(root, file string) ([]string, bool) {
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file))) //nolint:gosec // a tracked file git named
	if err != nil {
		return nil, false
	}

	return strings.Split(strings.TrimRight(string(body), "\n"), "\n"), true
}

func numbered(file string, s span, lines []string) string {
	var b strings.Builder

	b.WriteString("--- " + file + " lines " + strconv.Itoa(s.first) + "-" + strconv.Itoa(s.last) + "\n")

	for i := s.first; i <= s.last; i++ {
		b.WriteString(strconv.Itoa(i) + "\t" + lines[i-1] + "\n")
	}

	return b.String()
}

// declaration is the lines of the declaration that starts at line. A Go
// file is parsed, and the span is the declaration with its doc comment. Any
// other file is read by indentation: the declaration runs until a line that
// is indented no deeper than its first and does not close a bracket.
func declaration(file string, lines []string, line int) span {
	if strings.HasSuffix(file, ".go") {
		if s, ok := goDeclaration(lines, line); ok {
			return s
		}
	}

	depth := indent(lines[line-1])
	last := line

	for i := line + 1; i <= len(lines) && i < line+declLines; i++ {
		text := strings.TrimSpace(lines[i-1])
		if text != "" && indent(lines[i-1]) <= depth && !strings.ContainsAny(text[:1], "})]") {
			break
		}

		if text != "" {
			last = i
		}
	}

	return span{first: line, last: last}
}

func indent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

func goDeclaration(lines []string, line int) (span, bool) {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "", strings.Join(lines, "\n"), parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return span{}, false
	}

	for _, decl := range file.Decls {
		first, last := fset.Position(decl.Pos()).Line, fset.Position(decl.End()).Line
		if line < first || line > last {
			continue
		}

		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Doc != nil {
			first = fset.Position(fn.Doc.Pos()).Line
		}

		if gen, ok := decl.(*ast.GenDecl); ok && gen.Doc != nil {
			first = fset.Position(gen.Doc.Pos()).Line
		}

		return span{first: first, last: last}, true
	}

	return span{}, false
}

// mentions is the parts of a test file that name name: in Go the test
// functions that do, whole, and elsewhere the lines around each mention,
// with windows that touch joined.
func mentions(file string, lines []string, name string) []span {
	if strings.HasSuffix(file, ".go") {
		if spans, ok := goMentions(lines, name); ok {
			return spans
		}
	}

	var spans []span

	for i, text := range lines {
		if !strings.Contains(text, name) {
			continue
		}

		first, last := max(1, i+1-around), min(len(lines), i+1+around)
		if n := len(spans); n > 0 && first <= spans[n-1].last+1 {
			spans[n-1].last = last

			continue
		}

		spans = append(spans, span{first: first, last: last})
	}

	return spans
}

func goMentions(lines []string, name string) ([]span, bool) {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "", strings.Join(lines, "\n"), parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}

	var spans []span

	for _, decl := range file.Decls {
		first, last := fset.Position(decl.Pos()).Line, fset.Position(decl.End()).Line
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Doc != nil {
			first = fset.Position(fn.Doc.Pos()).Line
		}

		if strings.Contains(strings.Join(lines[first-1:last], "\n"), name) {
			spans = append(spans, span{first: first, last: last})
		}
	}

	return spans, true
}

// Brief is everything a prompt is answered with when the project hands the
// model the code and not only where it is: the lookup, the source, and the
// one command that applies edits and checks them. It is "" for no sites.
func Brief(root string, sites []Site) string {
	hint := Hint(sites)
	if hint == "" {
		return ""
	}

	// A rename is one command, and the source is no help in typing it.
	source := Excerpts(root, sites)
	if source == "" || goRename(sites) != "" {
		return hint
	}

	lead := whole
	if slices.ContainsFunc(sites, func(s Site) bool { return len(s.Tests) == 0 }) {
		lead = partial
	}

	return hint + "\nTheir current source:\n" +
		source + lead + apply.Fill(applyHelp)
}

const (
	// The whole sentence leads the edit instructions when every site came
	// with the tests that name it, and partial when one did not: a model
	// told it holds everything edits blind where it does not.
	whole   = "You have the source, so skip reading and make every edit, tests included, in one command:\n"
	partial = "Read what else the change needs in one message, not a file at a time, " +
		"then make every edit, tests included, in one command:\n"
)

// applyHelp tells the model how to make its edits and check them in one
// step. The format is the one `wavez apply` reads.
const applyHelp = `wavez apply <<'WAVEZ'
=== path/to/file.go
%OPEN%
exact lines to replace (no line numbers)
%SPLIT%
their replacement
%CLOSE%
WAVEZ
Nothing above the %SPLIT% line appends to the file, or creates it. Several edits may follow one === line.
It formats, builds, and runs the changed packages' tests, and prints one line on a pass.
No separate test run is needed.
`
