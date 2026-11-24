package lookup

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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
// files that mention it. A reader holding it has what reading those files
// would have told it. It is cut off, with a line saying so, at the budget.
func Excerpts(root string, sites []Site) string {
	var (
		out  strings.Builder
		seen = map[string]bool{}
	)

	for _, p := range append(declarations(root, sites), tested(root, sites)...) {
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

	source := Excerpts(root, sites)
	if source == "" {
		return hint
	}

	return hint + "\nTheir current source:\n" +
		source + apply.Fill(applyHelp)
}

// applyHelp tells the model how to make its edits and check them in one
// step. The format is the one `wavez apply` reads.
const applyHelp = `You have the source, so skip reading and make every edit, tests included, in one command:
wavez apply <<'WAVEZ'
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
