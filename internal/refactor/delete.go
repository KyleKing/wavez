package refactor

import (
	"context"
	"fmt"
	"go/ast"
	"path"
	"sort"
	"strings"
)

// DeleteResult describes a delete that was applied.
type DeleteResult struct {
	// Deleted lists, per file, the names removed from it.
	Deleted []string
	// Removed lists the files the delete left with no declarations, which
	// were removed.
	Removed []string
	Count   int
	// StillBroken counts the compile errors the packages had before the
	// delete and still have.
	StillBroken int
	// DryRun is set when the tree was put back after the delete ran.
	DryRun bool
}

// Summary is the short report of a delete.
func (r DeleteResult) Summary() string {
	out := fmt.Sprintf("deleted %s: %s\n", count(r.Count, "declaration"), strings.Join(r.Deleted, "; "))
	if len(r.Removed) > 0 {
		out += "removed " + strings.Join(r.Removed, " ") + ", left with nothing in it\n"
	}

	out += alreadyBroken(r.StillBroken)
	if r.DryRun {
		return dryRunHeader + out
	}

	return out
}

// Delete removes the declarations the targets name, with their doc
// comments, and fixes the imports of the files they were in. Deleting a
// type deletes its methods with it.
//
// It refuses, changing nothing, while anything outside the set being
// deleted still refers to one of them, and names each holder so the caller
// can add it to the same call.
func (r *Refactorer) Delete(ctx context.Context, specs []string) (DeleteResult, error) {
	s, err := r.open()
	if err != nil {
		return DeleteResult{}, err
	}
	defer s.close()

	sel, err := s.selectTargets(specs)
	if err != nil {
		return DeleteResult{}, err
	}

	if err := s.addMethods(sel); err != nil {
		return DeleteResult{}, err
	}

	holders, err := s.holders(ctx, sel)
	if err != nil {
		return DeleteResult{}, err
	}

	if len(holders) > 0 {
		return DeleteResult{}, &StillUsedError{Holders: holders, Retry: retryCommand(sel, holders)}
	}

	res := DeleteResult{Count: len(sel.decls)}

	if err := s.applyDelete(ctx, sel, &res); err != nil {
		return DeleteResult{}, s.fail(err)
	}

	if res.StillBroken, err = s.verify(ctx, sel.files()); err != nil {
		return DeleteResult{}, err
	}

	res.DryRun, err = r.finish(s)

	return res, err
}

// addMethods widens the selection with the methods of every selected type,
// wherever in the package they are declared.
func (s *session) addMethods(sel *selection) error {
	for _, d := range sel.types() {
		files, err := s.tree.parseDir(path.Dir(d.file.rel))
		if err != nil {
			return err
		}

		for _, pf := range files {
			if pf.ast.Name.Name != d.file.ast.Name.Name {
				continue
			}

			methods := declarationsOf(pf)
			for i := range methods {
				if methods[i].kind == kindMethod && methods[i].recv == d.name {
					sel.add(&methods[i])
				}
			}
		}
	}

	return nil
}

// holders asks gopls for the references to each selected declaration and
// returns the ones that sit outside the selection. Test entry points are
// not asked about: only the test runner calls them, and one process per
// test function is most of what a delete would otherwise cost.
func (s *session) holders(ctx context.Context, sel *selection) ([]Holder, error) {
	var asked []declaration

	for i := range sel.decls {
		if !isTestEntry(&sel.decls[i]) {
			asked = append(asked, sel.decls[i])
		}
	}

	found := make([][]location, len(asked))

	err := inParallel(len(asked), func(i int) error {
		locs, refErr := s.tools.references(ctx, position(asked[i].file.rel, asked[i].ident), false)
		found[i] = locs

		return refErr
	})
	if err != nil {
		return nil, err
	}

	var out []Holder

	seen := map[Holder]bool{}

	for i, locs := range found {
		for _, loc := range locs {
			if sel.covers(loc.file, loc.line) {
				continue
			}

			h := Holder{Name: asked[i].name, File: loc.file, Line: loc.line, In: s.enclosing(loc)}
			if !seen[h] {
				seen[h] = true

				out = append(out, h)
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}

		return out[i].Line < out[j].Line
	})

	return out, nil
}

func isTestEntry(d *declaration) bool {
	return d.kind == kindFunc && strings.HasSuffix(d.file.rel, testSuffix) && entryPrefix(d.name) != ""
}

// enclosing names the top-level declaration a location sits in, or nothing
// when the file does not parse or the line is outside every declaration.
func (s *session) enclosing(loc location) string {
	pf, err := s.tree.parse(loc.file)
	if err != nil {
		return ""
	}

	within := func(n ast.Node) bool {
		return pf.line(n.Pos()) <= loc.line && loc.line <= pf.line(n.End())
	}

	for _, node := range pf.ast.Decls {
		if !within(node) {
			continue
		}

		switch n := node.(type) {
		case *ast.FuncDecl:
			return funcDeclaration(pf, n).name
		case *ast.GenDecl:
			return specName(n, within)
		}
	}

	return ""
}

func specName(gen *ast.GenDecl, within func(ast.Node) bool) string {
	for _, spec := range gen.Specs {
		if !within(spec) {
			continue
		}

		switch sp := spec.(type) {
		case *ast.TypeSpec:
			return sp.Name.Name
		case *ast.ValueSpec:
			return sp.Names[0].Name
		}
	}

	return ""
}

// retryCommand spells the call that deletes the holders along with what was
// asked for, so acting on a refusal is one step.
func retryCommand(sel *selection, holders []Holder) string {
	names := map[string][]string{}
	seen := map[string]bool{}

	add := func(file, name string) {
		if key := file + ":" + name; name != "" && name != "_" && !seen[key] {
			seen[key] = true

			names[file] = append(names[file], name)
		}
	}

	for i := range sel.decls {
		add(sel.decls[i].file.rel, sel.decls[i].name)
	}

	for _, h := range holders {
		add(h.File, h.In)
	}

	files := make([]string, 0, len(names))
	for file := range names {
		files = append(files, file)
	}

	sort.Strings(files)

	var b strings.Builder

	b.WriteString("wavez delete")

	for _, file := range files {
		b.WriteString(" " + file + ":" + strings.Join(names[file], ","))
	}

	return b.String()
}

// applyDelete cuts the selection out of each file and tidies what is left.
func (s *session) applyDelete(ctx context.Context, sel *selection, res *DeleteResult) error {
	var kept []string

	for _, file := range sel.files() {
		pf, err := s.tree.parse(file)
		if err != nil {
			return err
		}

		res.Deleted = append(res.Deleted, fmt.Sprintf("%s (%s)", file, sel.names(file)))

		stays, err := s.writeOrRemove(file, cut(pf.src, sel.spans(file)))
		if err != nil {
			return err
		}

		if !stays {
			res.Removed = append(res.Removed, file)

			continue
		}

		if len(pf.ast.Imports) > 0 {
			kept = append(kept, file)
		}
	}

	return s.tidy(ctx, kept)
}
