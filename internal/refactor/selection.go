package refactor

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"sort"
	"strings"
)

// selection is the whole declarations a move or delete was asked to act on.
type selection struct {
	decls []declaration
}

// add includes a declaration once, however many targets name it.
func (sel *selection) add(d *declaration) {
	for i := range sel.decls {
		if have := &sel.decls[i]; have.file.rel == d.file.rel && have.ident == d.ident {
			return
		}
	}

	sel.decls = append(sel.decls, *d)
}

// selectTargets resolves every name of every target into one selection. It
// refuses a name that shares a const, var, or type block with names the
// call left out, because the block is the unit the edit can cut cleanly.
func (s *session) selectTargets(specs []string) (*selection, error) {
	sel := &selection{}

	for _, spec := range specs {
		t, err := s.parseTarget(spec)
		if err != nil {
			return nil, err
		}

		for _, name := range t.names {
			d, err := s.resolve(t, name, false)
			if err != nil {
				return nil, err
			}

			sel.add(&d)
		}
	}

	for i := range sel.decls {
		if err := sel.wholeBlock(&sel.decls[i]); err != nil {
			return nil, err
		}
	}

	return sel, nil
}

func (sel *selection) wholeBlock(d *declaration) error {
	var missing []string

	for _, sibling := range d.siblings {
		if sibling != d.name && !sel.holds(d.file.rel, d.start, sibling) {
			missing = append(missing, sibling)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	return &GroupedError{Name: d.name, At: d.at(), Siblings: missing}
}

func (sel *selection) holds(file string, start int, name string) bool {
	for i := range sel.decls {
		if d := &sel.decls[i]; d.file.rel == file && d.start == start && d.name == name {
			return true
		}
	}

	return false
}

// covers reports whether a line falls inside one of the selected
// declarations.
func (sel *selection) covers(file string, line int) bool {
	for i := range sel.decls {
		if sel.decls[i].contains(file, line) {
			return true
		}
	}

	return false
}

// files lists the files holding selected declarations, sorted.
func (sel *selection) files() []string {
	seen := map[string]bool{}

	var out []string

	for i := range sel.decls {
		if rel := sel.decls[i].file.rel; !seen[rel] {
			seen[rel] = true

			out = append(out, rel)
		}
	}

	sort.Strings(out)

	return out
}

// in returns the selected declarations of one file in source order.
func (sel *selection) in(file string) []declaration {
	var out []declaration

	for i := range sel.decls {
		if sel.decls[i].file.rel == file {
			out = append(out, sel.decls[i])
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].ident < out[j].ident })

	return out
}

// span is a half-open byte range of a file.
type span struct {
	start, end int
}

// spans returns the byte ranges the selection covers in one file, in order
// and without the repeats that names sharing a block produce.
func (sel *selection) spans(file string) []span {
	var out []span

	decls := sel.in(file)
	for i := range decls {
		if n := len(out); n > 0 && out[n-1].start == decls[i].start {
			continue
		}

		out = append(out, span{start: decls[i].start, end: decls[i].end})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })

	return out
}

// names lists the selected names of one file for a report.
func (sel *selection) names(file string) string {
	decls := sel.in(file)
	out := make([]string, 0, len(decls))

	for i := range decls {
		out = append(out, decls[i].name)
	}

	return strings.Join(out, ", ")
}

// cut removes spans from src, each with the rest of its last line and the
// blank line that would otherwise be left doubled.
func cut(src []byte, spans []span) []byte {
	out := src

	for i := len(spans) - 1; i >= 0; i-- {
		wide := widen(out, spans[i])
		out = splice(out, wide.start, wide.end, "")
	}

	return []byte(strings.TrimRight(string(out), "\n") + "\n")
}

// widen grows a span over the indentation before it, the rest of its last
// line, and the blank lines after it when a blank line already precedes it.
func widen(src []byte, sp span) span {
	start, end := sp.start, sp.end

	for start > 0 && (src[start-1] == ' ' || src[start-1] == '\t') {
		start--
	}

	for end < len(src) && (src[end] == ' ' || src[end] == '\t' || src[end] == '\r') {
		end++
	}

	if end < len(src) && src[end] == '\n' {
		end++
	}

	blankBefore := start == 0 || bytes.HasSuffix(src[:start], []byte("\n\n"))
	for blankBefore && end < len(src) && src[end] == '\n' {
		end++
	}

	return span{start: start, end: end}
}

// tidy finishes the files an operation rewrote: gopls fixes their imports,
// and each file that was gofmt-clean before is formatted again. A file that
// was not formatted to begin with is left in its own style rather than
// reformatted around the change.
func (s *session) tidy(ctx context.Context, files []string) error {
	if err := s.tools.fixImports(ctx, files); err != nil {
		return err
	}

	for _, file := range files {
		src, err := s.tree.read(file)
		if err != nil {
			return err
		}

		if before, existed := s.snap.original(file); existed && !isFormatted(before) {
			continue
		}

		formatted, err := format.Source(src)
		if err != nil {
			return fmt.Errorf("formatting %s: %w", file, err)
		}

		if bytes.Equal(formatted, src) {
			continue
		}

		if err := s.snap.write(file, formatted); err != nil {
			return err
		}
	}

	return nil
}

func isFormatted(src []byte) bool {
	formatted, err := format.Source(src)

	return err == nil && bytes.Equal(formatted, src)
}

// writeOrRemove stores a file's new content, or removes the file when
// nothing is left in it but a package clause and imports. A file whose
// package clause carries the package documentation is kept. It reports
// whether the file is still there.
func (s *session) writeOrRemove(file string, src []byte) (bool, error) {
	pf, err := parseSource(file, src)
	if err != nil {
		return false, err
	}

	if hasDeclarations(pf.ast) || pf.ast.Doc != nil {
		return true, s.snap.write(file, src)
	}

	return false, s.snap.remove(file)
}

// types returns the selected type declarations.
func (sel *selection) types() []*declaration {
	var out []*declaration

	for i := range sel.decls {
		if sel.decls[i].kind == kindType {
			out = append(out, &sel.decls[i])
		}
	}

	return out
}
