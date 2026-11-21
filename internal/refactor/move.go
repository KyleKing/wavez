package refactor

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// MoveResult describes a move that was applied.
type MoveResult struct {
	From string
	To   string
	// Names lists what moved, in source order.
	Names string
	// Created is set when the destination file did not exist.
	Created bool
	// SourceRemoved is set when the move left the source with no
	// declarations, so the file was removed.
	SourceRemoved bool
}

// Summary is the one-line report of a move.
func (r MoveResult) Summary() string {
	to := r.To
	if r.Created {
		to += " (created)"
	}

	out := fmt.Sprintf("moved %s from %s to %s\n", r.Names, r.From, to)
	if r.SourceRemoved {
		out += "removed " + r.From + ", which had nothing left in it\n"
	}

	return out
}

// Move moves top-level declarations, each with its doc comment, from one
// file to another file of the same package. The destination is created with
// the source's package clause when it does not exist, the moved text is
// copied byte for byte, and the imports of both files are fixed up.
//
// It refuses a destination in another package, since that changes every
// qualified reference and is a different operation.
func (r *Refactorer) Move(ctx context.Context, spec, dest string) (MoveResult, error) {
	s, err := r.open()
	if err != nil {
		return MoveResult{}, err
	}
	defer s.close()

	sel, err := s.selectTargets([]string{spec})
	if err != nil {
		return MoveResult{}, err
	}

	files := sel.files()
	if len(files) != 1 {
		return MoveResult{}, &TargetError{
			Target: spec,
			Reason: "those names are declared in " + strings.Join(files, " and ") + "; move from one file per call",
		}
	}

	src := sel.decls[0].file

	destRel, err := s.moveDest(src, spec, dest)
	if err != nil {
		return MoveResult{}, err
	}

	res := MoveResult{From: src.rel, To: destRel, Names: sel.names(src.rel)}

	touched, err := s.applyMove(ctx, sel, src, destRel, &res)
	if err != nil {
		return MoveResult{}, s.fail(err)
	}

	if err := s.verify(ctx, touched); err != nil {
		return MoveResult{}, err
	}

	return res, nil
}

// moveDest validates the destination of a move and returns it relative to
// the module root.
func (s *session) moveDest(src *parsedFile, spec, dest string) (string, error) {
	rel, ok := s.relPath(dest)
	if !ok || !strings.HasSuffix(rel, goExt) {
		return "", &TargetError{Target: dest, Reason: "the destination must be a .go file inside the module"}
	}

	if rel == src.rel {
		return "", &TargetError{Target: spec, Reason: "the declarations are already in " + rel}
	}

	from := fmt.Sprintf("package %s in %s", src.ast.Name.Name, path.Dir(src.rel))

	if path.Dir(rel) != path.Dir(src.rel) {
		return "", &CrossPackageError{From: from, To: "the directory " + path.Dir(rel)}
	}

	found, isDir := s.tree.exists(rel)
	if isDir {
		return "", &TargetError{Target: dest, Reason: "the destination is a directory"}
	}

	if !found {
		return rel, nil
	}

	existing, err := s.tree.parse(rel)
	if err != nil {
		return "", err
	}

	if existing.ast.Name.Name != src.ast.Name.Name {
		return "", &CrossPackageError{From: from, To: fmt.Sprintf("package %s (%s)", existing.ast.Name.Name, rel)}
	}

	return rel, nil
}

// applyMove writes both files and returns the ones the build check should
// cover.
func (s *session) applyMove(
	ctx context.Context, sel *selection, src *parsedFile, destRel string, res *MoveResult,
) ([]string, error) {
	destSrc, err := s.destSource(src, destRel, res)
	if err != nil {
		return nil, err
	}

	module := s.tree.modulePath()

	for _, spec := range neededImports(src, sel.in(src.rel)) {
		if destSrc, err = addImport(destRel, destSrc, spec, module); err != nil {
			return nil, err
		}
	}

	spans := sel.spans(src.rel)
	moved := make([]string, 0, len(spans))

	for _, sp := range spans {
		moved = append(moved, string(src.src[sp.start:sp.end]))
	}

	destSrc = []byte(strings.TrimRight(string(destSrc), "\n") + "\n\n" + strings.Join(moved, "\n\n") + "\n")
	if err := s.snap.write(destRel, destSrc); err != nil {
		return nil, err
	}

	kept, err := s.writeOrRemove(src.rel, cut(src.src, spans))
	if err != nil {
		return nil, err
	}

	touched := []string{destRel}
	if kept {
		touched = append(touched, src.rel)
	}

	res.SourceRemoved = !kept

	return []string{destRel, src.rel}, s.tidy(ctx, touched)
}

// destSource is the destination's current content, or a new file holding
// only the source's package clause.
func (s *session) destSource(src *parsedFile, destRel string, res *MoveResult) ([]byte, error) {
	if found, _ := s.tree.exists(destRel); found {
		return s.tree.read(destRel)
	}

	res.Created = true

	return []byte("package " + src.ast.Name.Name + "\n"), nil
}
