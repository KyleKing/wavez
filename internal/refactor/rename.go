package refactor

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxMentionLines = 12
	maxFileNames    = 20
	initName        = "init"
	testMainName    = "TestMain"
	examplePrefix   = "Example"
)

// RenameResult describes a rename that was applied.
type RenameResult struct {
	Old string
	New string
	// Files lists the files rewritten, relative to the module root.
	Files []string
	// Generated lists the rewritten files that carry a "Code generated"
	// header, whose generator will write the old name back.
	Generated []string
	// Excluded lists lines that use the old name in files the build leaves
	// out. gopls does not load those, so they are not renamed.
	Excluded []Mention
	// Mentions lists comment and string lines that still carry the old
	// name, after Rewritten of them were renamed as well.
	Mentions []Mention
	// Lines counts the changed lines of each file in Files, which a dry
	// run reports.
	Lines []int
	// StillBroken counts the compile errors the touched packages had
	// before the rename and still have.
	StillBroken int
	// ExcludedUnchecked is set when the go tool could not say which files
	// the build holds, so Excluded is empty without having been looked for.
	ExcludedUnchecked bool
	// DryRun is set when the tree was put back after the rename ran.
	DryRun bool
	// Rewritten counts the comment and string mentions of the old name, in
	// the files renamed, that name the symbol unmistakably and were renamed.
	Rewritten int
}

// Summary is the report an agent reads: what changed, and what it may still
// want to look at.
func (r RenameResult) Summary() string {
	var b strings.Builder

	files := r.Files
	if r.DryRun {
		b.WriteString(dryRunHeader)
	}

	if r.DryRun && len(r.Lines) == len(r.Files) {
		files = make([]string, 0, len(r.Files))
		for i, file := range r.Files {
			files = append(files, fmt.Sprintf("%s (%s)", file, count(r.Lines[i], "line")))
		}
	}

	fmt.Fprintf(&b, "renamed %s to %s in %s: %s\n", r.Old, r.New,
		count(len(r.Files), "file"), capped(files, maxFileNames))

	if len(r.Generated) > 0 {
		fmt.Fprintf(&b, "generated code among them, which its generator will write back: %s\n",
			capped(r.Generated, maxFileNames))
	}

	b.WriteString(alreadyBroken(r.StillBroken))

	old := r.Old[strings.LastIndexByte(r.Old, '.')+1:]

	switch {
	case r.ExcludedUnchecked:
		b.WriteString("go list failed, so files excluded from this build were not checked for " + old + "\n")
	case old != r.Old:
		listed(&b, r.Excluded, "not renamed, excluded from this build (GOOS/GOARCH or build tag); "+
			"these select ."+old+" and may mean "+r.Old+":\n")
	default:
		listed(&b, r.Excluded, "not renamed, excluded from this build (GOOS/GOARCH or build tag); "+
			"these still use "+old+":\n")
	}

	if r.Rewritten > 0 {
		fmt.Fprintf(&b, "renamed it in %s too\n", count(r.Rewritten, "comment or string mention"))
	}

	listed(&b, r.Mentions, fmt.Sprintf("%q still appears in %s (left alone; edit the ones that mean this symbol):\n",
		old, count(len(r.Mentions), "comment or string line")))

	return b.String()
}

// listed writes a heading and the file:line of each place under it, capped,
// and nothing at all when there are no places.
func listed(b *strings.Builder, places []Mention, heading string) {
	if len(places) == 0 {
		return
	}

	b.WriteString(heading)

	for i, m := range places {
		if i == maxMentionLines {
			fmt.Fprintf(b, "  (+%d more)\n", len(places)-maxMentionLines)

			break
		}

		if m.Text == "" {
			fmt.Fprintf(b, "  %s:%d\n", m.File, m.Line)

			continue
		}

		fmt.Fprintf(b, "  %s:%d: %s\n", m.File, m.Line, m.Text)
	}
}

// Rename renames the declaration a target names and every reference to it
// across the module, tests included. The target is path:Name, where path is
// a file or a package directory and Name may be Type.Method or Type.Field.
// An interface method takes the methods that implement it along, and an
// exported name takes its Example functions.
//
// It refuses, changing nothing, when the name is missing or ambiguous, when
// the new name would stop an entry point being one, when gopls rejects the
// new name, and when the rename adds a compile error to the affected
// packages. Uses in files the build leaves out are reported, not renamed.
func (r *Refactorer) Rename(ctx context.Context, spec, newName string) (RenameResult, error) {
	s, err := r.open()
	if err != nil {
		return RenameResult{}, err
	}
	defer s.close()

	decl, err := s.renameTarget(spec, newName)
	if err != nil {
		return RenameResult{}, err
	}

	files, err := s.keepModule()
	if err != nil {
		return RenameResult{}, err
	}

	changed, err := s.applyRename(ctx, &decl, newName)
	if err != nil {
		return RenameResult{}, s.fail(err)
	}

	res := RenameResult{Old: decl.name, New: newName, Files: changed}

	if res.StillBroken, err = s.verify(ctx, changed, decl.base(), newName); err != nil {
		return RenameResult{}, err
	}

	touched := map[string]bool{}

	for _, file := range changed {
		touched[file] = true
		res.Lines = append(res.Lines, s.changedLines(file))

		if s.isGenerated(file) {
			res.Generated = append(res.Generated, file)
		}
	}

	if res.Rewritten, err = s.renameMentions(changed, decl.base(), newName); err != nil {
		return RenameResult{}, s.fail(err)
	}

	excluded, checked := s.excluded(ctx, files, &decl)
	res.Excluded, res.ExcludedUnchecked = excluded, !checked
	res.Mentions = s.mentions(files, decl.base(), path.Dir(decl.file.rel), touched)
	res.DryRun, err = r.finish(s)

	return res, err
}

// applyRename renames the declaration and then the Example functions
// named after it, and returns the files changed, sorted.
func (s *session) applyRename(ctx context.Context, decl *declaration, newName string) ([]string, error) {
	rename := s.renameInterfaceMethod
	if !decl.isInterfaceMethod() {
		rename = func(ctx context.Context, decl *declaration, newName string) ([]string, error) {
			return s.tools.rename(ctx, position(decl.file.rel, decl.ident), newName)
		}
	}

	renamed, err := rename(ctx, decl, newName)
	if err != nil {
		return nil, err
	}

	examples, err := s.renameExamples(decl, newName)
	if err != nil {
		return nil, err
	}

	changed := slices.Concat(renamed, examples)
	slices.Sort(changed)

	return slices.Compact(changed), nil
}

// renameInterfaceMethod renames an interface method together with the
// methods that implement it, by rewriting every reference gopls reports
// for any of them. A gopls rename cannot do it: it refuses an implementer
// alone, and given the interface it leaves the implementers of the
// interface's own package behind, in a tree it then refuses to touch.
func (s *session) renameInterfaceMethod(ctx context.Context, decl *declaration, newName string) ([]string, error) {
	implementers, err := s.tools.implementations(ctx, decl.file.rel, decl.ident)
	if err != nil {
		return nil, err
	}

	asked := []string{position(decl.file.rel, decl.ident)}
	for _, at := range implementers {
		asked = append(asked, at.String())
	}

	found := make([][]location, len(asked))

	err = inParallel(len(asked), func(i int) error {
		locs, refErr := s.tools.references(ctx, asked[i], true)
		found[i] = locs

		return refErr
	})
	if err != nil {
		return nil, err
	}

	byFile := map[string][]location{}
	for _, loc := range slices.Concat(found...) {
		byFile[loc.file] = append(byFile[loc.file], loc)
	}

	changed := make([]string, 0, len(byFile))

	for file, locs := range byFile {
		if err := s.replaceAt(file, locs, decl.base(), newName); err != nil {
			return nil, err
		}

		changed = append(changed, file)
	}

	return changed, nil
}

// replaceAt rewrites old as replacement at each location of a file. A
// location that does not hold old is skipped, and the build check is what
// notices if it mattered.
func (s *session) replaceAt(file string, locs []location, old, replacement string) error {
	src, err := s.tree.read(file)
	if err != nil {
		return err
	}

	var offsets []int

	for _, loc := range locs {
		if at, ok := loc.offset(src, old); ok {
			offsets = append(offsets, at)
		}
	}

	slices.Sort(offsets)
	offsets = slices.Compact(offsets)

	for _, at := range slices.Backward(offsets) {
		src = splice(src, at, at+len(old), replacement)
	}

	return s.snap.write(file, src)
}

// renameExamples renames the Example functions that document the symbol,
// because go vet, which go test runs, fails an ExampleRead left behind
// when Read is gone. Nothing calls an example, so its name is the only
// edit.
func (s *session) renameExamples(decl *declaration, newName string) ([]string, error) {
	old := decl.base()
	if !ast.IsExported(old) {
		return nil, nil
	}

	owner := examplePrefix
	if decl.recv != "" {
		owner += decl.recv + "_"
	}

	names, err := s.tree.goFilesIn(path.Dir(decl.file.rel))
	if err != nil {
		return nil, err
	}

	var changed []string

	for _, file := range names {
		if !strings.HasSuffix(file, testSuffix) {
			continue
		}

		src, err := s.tree.read(file)
		if err != nil {
			return nil, err
		}

		out := withExamplesRenamed(file, src, owner+old, owner+newName)
		if bytes.Equal(out, src) {
			continue
		}

		if err := s.snap.write(file, out); err != nil {
			return nil, err
		}

		changed = append(changed, file)
	}

	return changed, nil
}

// withExamplesRenamed returns src with each top-level function named
// prefix, or prefix and a _suffix, given replacement in prefix's place. A
// file that does not parse is returned as it is.
func withExamplesRenamed(file string, src []byte, prefix, replacement string) []byte {
	pf, err := parseSource(file, src)
	if err != nil {
		return src
	}

	for _, node := range slices.Backward(pf.ast.Decls) {
		fn, ok := node.(*ast.FuncDecl)
		if !ok || fn.Recv != nil {
			continue
		}

		if rest, ok := strings.CutPrefix(fn.Name.Name, prefix); ok && (rest == "" || rest[0] == '_') {
			at := pf.offset(fn.Name.Pos())
			src = splice(src, at, at+len(fn.Name.Name), replacement+rest)
		}
	}

	return src
}

// changedLines counts the lines of a file that differ from what it held
// before the operation.
func (s *session) changedLines(file string) int {
	before, _ := s.snap.original(file)

	after, err := s.tree.read(file)
	if err != nil {
		return 0
	}

	was, now := bytes.Split(before, []byte("\n")), bytes.Split(after, []byte("\n"))
	n := max(len(was), len(now)) - min(len(was), len(now))

	for i := range min(len(was), len(now)) {
		if !bytes.Equal(was[i], now[i]) {
			n++
		}
	}

	return n
}

func (s *session) isGenerated(file string) bool {
	src, err := s.tree.read(file)
	if err != nil {
		return false
	}

	pf, err := parseSource(file, src)

	return err == nil && ast.IsGenerated(pf.ast)
}

func (s *session) renameTarget(spec, newName string) (declaration, error) {
	t, err := s.parseTarget(spec)
	if err != nil {
		return declaration{}, err
	}

	if len(t.names) != 1 {
		return declaration{}, &TargetError{Target: spec, Reason: "rename takes exactly one name"}
	}

	if !token.IsIdentifier(newName) {
		return declaration{}, &InvalidNameError{Name: newName, Reason: "it is a keyword or not a Go identifier"}
	}

	decl, err := s.resolve(t, t.names[0], true)
	if err != nil {
		return declaration{}, err
	}

	if decl.base() == newName {
		return declaration{}, &InvalidNameError{Name: newName, Reason: "that is already its name"}
	}

	if reason := entryPointReason(&decl, newName); reason != "" {
		return declaration{}, &InvalidNameError{Name: newName, Reason: reason}
	}

	return decl, nil
}

// entryPointReason says why a rename would stop the toolchain finding a
// function it calls by name, or nothing when it would not. The compiler
// accepts every one of these renames, so the build check cannot catch them.
func entryPointReason(decl *declaration, newName string) string {
	if decl.kind != kindFunc {
		return ""
	}

	switch {
	case decl.name == mainName && decl.file.ast.Name.Name == mainName:
		return "main is where the program starts, and package main does not link without it"
	case decl.name == initName:
		return "init runs when the package loads only under that name"
	case !strings.HasSuffix(decl.file.rel, testSuffix):
		return ""
	}

	prefix := entryPrefix(decl.name)

	switch {
	case prefix == "":
		return ""
	case decl.name == testMainName:
		return "go test calls TestMain only under that name"
	case newName == testMainName:
		return "go test would call it as the test binary's entry point"
	case entryPrefix(newName) != prefix:
		return fmt.Sprintf("go test runs %s only while its name is %s followed by nothing or by a character "+
			"that is not a lower-case letter", decl.name, prefix)
	default:
		return ""
	}
}

// entryPrefix is the prefix that makes go test run a function of a test
// file, or nothing for a name it does not run.
func entryPrefix(name string) string {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", examplePrefix} {
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}

		if first, _ := utf8.DecodeRuneInString(rest); rest == "" || !unicode.IsLower(first) {
			return prefix
		}
	}

	return ""
}

// keepModule records every Go file of the module before gopls writes, since
// gopls decides which files a rename reaches and only says so afterwards.
func (s *session) keepModule() ([]string, error) {
	files, err := s.tree.walkGoFiles()
	if err != nil {
		return nil, err
	}

	for _, file := range files {
		if err := s.snap.keep(file); err != nil {
			return nil, err
		}
	}

	return files, nil
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return fmt.Sprintf("%d %ss", n, noun)
}

func capped(items []string, limit int) string {
	if len(items) <= limit {
		return strings.Join(items, " ")
	}

	return fmt.Sprintf("%s (+%d more)", strings.Join(items[:limit], " "), len(items)-limit)
}
