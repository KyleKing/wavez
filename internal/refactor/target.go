package refactor

import (
	"path"
	"path/filepath"
	"strings"
)

// target is a parsed path:Name[,Name] argument.
type target struct {
	spec  string
	path  string
	names []string
	isDir bool
}

// relPath turns a path as typed into one relative to the module root. A
// relative path is tried against the working directory first and the root
// second; one that exists in neither is taken to be under the working
// directory, which is where a file about to be created belongs.
func (s *session) relPath(typed string) (string, bool) {
	bases := []string{s.work, s.tree.dir}
	if filepath.IsAbs(typed) {
		bases = []string{""}
	}

	first := ""

	for _, base := range bases {
		rel, err := filepath.Rel(s.tree.dir, filepath.Join(base, typed))
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}

		rel = filepath.ToSlash(rel)
		if found, _ := s.tree.exists(rel); found {
			return rel, true
		}

		if first == "" {
			first = rel
		}
	}

	return first, first != ""
}

func (s *session) parseTarget(spec string) (target, error) {
	i := strings.LastIndexByte(spec, ':')
	if i <= 0 || i == len(spec)-1 {
		return target{}, &TargetError{Target: spec, Reason: "want path/to/file.go:Name or ./pkg/dir:Name"}
	}

	rel, ok := s.relPath(spec[:i])
	if !ok {
		return target{}, &TargetError{Target: spec, Reason: "the path is outside the module root " + s.tree.dir}
	}

	found, isDir := s.tree.exists(rel)
	if !found {
		return target{}, &TargetError{Target: spec, Reason: "no such file or directory: " + rel}
	}

	if !isDir && !strings.HasSuffix(rel, goExt) {
		return target{}, &TargetError{Target: spec, Reason: rel + " is not a Go file"}
	}

	t := target{spec: spec, path: rel, isDir: isDir}

	for name := range strings.SplitSeq(spec[i+1:], ",") {
		if name = strings.TrimSpace(name); name != "" {
			t.names = append(t.names, name)
		}
	}

	if len(t.names) == 0 {
		return target{}, &TargetError{Target: spec, Reason: "no name after the colon"}
	}

	return t, nil
}

// scope parses the files a target searches: the one file, or every Go file
// of the directory.
func (s *session) scope(t target) ([]*parsedFile, error) {
	if t.isDir {
		return s.tree.parseDir(t.path)
	}

	pf, err := s.tree.parse(t.path)
	if err != nil {
		return nil, err
	}

	return []*parsedFile{pf}, nil
}

// resolve finds the one declaration a name means inside a target's scope.
// A bare name that no top-level declaration carries falls back to methods of
// that name, and with members set a Type.Name falls back to struct fields
// and interface methods.
func (s *session) resolve(t target, name string, members bool) (declaration, error) {
	files, err := s.scope(t)
	if err != nil {
		return declaration{}, err
	}

	var all []declaration
	for _, pf := range files {
		all = append(all, declarationsOf(pf)...)
	}

	found := matching(all, name)
	if len(found) == 0 && members && strings.Contains(name, ".") {
		for _, pf := range files {
			all = append(all, fieldsOf(pf)...)
		}

		found = matching(all, name)
	}

	switch len(found) {
	case 0:
		return declaration{}, s.notFound(t, name, all)
	case 1:
		return found[0], nil
	default:
		return declaration{}, ambiguous(name, found)
	}
}

func matching(all []declaration, name string) []declaration {
	var exact, methods []declaration

	for i := range all {
		d := &all[i]

		switch {
		case d.name == name:
			exact = append(exact, *d)
		case d.kind == kindMethod && d.base() == name:
			methods = append(methods, *d)
		}
	}

	if len(exact) > 0 {
		return exact
	}

	return methods
}

func ambiguous(name string, found []declaration) *AmbiguousError {
	err := &AmbiguousError{Name: name}
	for i := range found {
		d := &found[i]
		err.Candidates = append(err.Candidates, Candidate{Target: d.target(), Kind: d.kind.String(), Line: d.line()})
	}

	return err
}

// notFound builds the refusal for a name the scope does not declare. For a
// file target it looks through the rest of the package first, because the
// usual mistake is naming the wrong file of the right package.
func (s *session) notFound(t target, name string, all []declaration) *NotFoundError {
	err := &NotFoundError{Name: name, Scope: t.path}

	if !t.isDir {
		if siblings, dirErr := s.tree.parseDir(path.Dir(t.path)); dirErr == nil {
			err.Elsewhere = elsewhere(siblings, name)
		}
	}

	names := make([]string, 0, len(all))
	for i := range all {
		names = append(names, all[i].name)
	}

	err.Near = nearNames(name, names)

	return err
}

func elsewhere(files []*parsedFile, name string) string {
	for _, pf := range files {
		if found := matching(declarationsOf(pf), name); len(found) > 0 {
			return found[0].target()
		}
	}

	return ""
}
