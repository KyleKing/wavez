package refactor

import (
	"context"
	"fmt"
	"go/token"
	"path"
	"sort"
	"strings"
)

const (
	maxMentionLines = 12
	maxFileNames    = 20
)

// RenameResult describes a rename that was applied.
type RenameResult struct {
	Old string
	New string
	// Files lists the files gopls rewrote, relative to the module root.
	Files []string
	// Mentions lists comment and string lines that still carry the old
	// name. They are reported, never edited.
	Mentions []Mention
}

// Summary is the report an agent reads: what changed, and what it may still
// want to look at.
func (r RenameResult) Summary() string {
	var b strings.Builder

	fmt.Fprintf(&b, "renamed %s to %s in %s: %s\n", r.Old, r.New,
		count(len(r.Files), "file"), capped(r.Files, maxFileNames))

	if len(r.Mentions) == 0 {
		return b.String()
	}

	fmt.Fprintf(&b, "%q still appears in %s (left alone; edit the ones that mean this symbol):\n",
		r.Old, count(len(r.Mentions), "comment or string line"))

	for i, m := range r.Mentions {
		if i == maxMentionLines {
			fmt.Fprintf(&b, "  (+%d more)\n", len(r.Mentions)-maxMentionLines)

			break
		}

		fmt.Fprintf(&b, "  %s:%d\n", m.File, m.Line)
	}

	return b.String()
}

// Rename renames the declaration a target names and every reference to it
// across the module, tests included. The target is path:Name, where path is
// a file or a package directory and Name may be Type.Method or Type.Field.
//
// It refuses, changing nothing, when the name is missing or ambiguous, when
// gopls rejects the new name, and when the affected packages do not build
// afterwards.
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

	changed, err := s.tools.rename(ctx, decl.file.rel, decl.ident, newName)
	if err != nil {
		return RenameResult{}, s.fail(err)
	}

	if err := s.verify(ctx, changed); err != nil {
		return RenameResult{}, err
	}

	sort.Strings(changed)

	touched := map[string]bool{}
	for _, file := range changed {
		touched[file] = true
	}

	return RenameResult{
		Old: decl.name, New: newName, Files: changed,
		Mentions: s.mentions(files, decl.base(), path.Dir(decl.file.rel), touched),
	}, nil
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

	return decl, nil
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
