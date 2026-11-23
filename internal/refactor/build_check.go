package refactor

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	maxBuildLines = 10
	mainName      = "main"
	testSuffix    = "_test.go"
)

// build compiles the packages in dirs together with their tests, without
// linking or running anything. `go list -export` is the cheapest command
// that does: it compiles each package and each test variant, and exits
// non-zero with the compiler's diagnostics when one does not. With every
// set the compiler reports each error instead of stopping at ten a
// package, which is what lets two failing builds be compared.
func (t toolchain) build(ctx context.Context, dirs []string, every bool) error {
	args := []string{"list", "-test", "-export", "-f", "{{.ImportPath}}"}
	if every {
		args = append(args, "-gcflags=-e")
	}

	for _, dir := range dirs {
		args = append(args, "./"+dir)
	}

	_, err := t.run(ctx, t.goTool, args...)

	return err
}

// verify confirms the packages an operation touched still compile, and
// returns how many compile errors were there before it and remain. When
// the build fails it restores every file and builds once more: a tree that
// built before stays restored, and a tree that was already broken gets the
// operation back only if the operation added no error of its own. The
// names are the old and new spelling of a renamed identifier, which the
// comparison reads as one.
func (s *session) verify(ctx context.Context, files []string, names ...string) (int, error) {
	dirs := s.packageDirs(files)
	if len(dirs) == 0 {
		return 0, nil
	}

	if err := s.lostMain(dirs); err != nil {
		return 0, s.fail(err)
	}

	err := s.tools.build(ctx, dirs, false)
	if err == nil {
		return 0, nil
	}

	var failed *commandError
	if !errors.As(err, &failed) {
		return 0, s.fail(err)
	}

	// Ask again for every error; when that run cannot answer, the first
	// one's output stands.
	errors.As(s.tools.build(ctx, dirs, true), &failed)

	after := diagnostics(failed.output)

	applied, err := s.snap.capture()
	if err != nil {
		return 0, s.fail(err)
	}

	if restoreErr := s.snap.restore(); restoreErr != nil {
		return 0, fmt.Errorf("%s\nand restoring the files failed: %w", reduce(after), restoreErr)
	}

	var before *commandError
	if beforeErr := s.tools.build(ctx, s.packageDirs(files), true); !errors.As(beforeErr, &before) {
		if beforeErr != nil {
			return 0, beforeErr
		}

		return 0, &BuildError{Output: reduce(after)}
	}

	if added := addedDiagnostics(diagnostics(before.output), after, names); len(added) > 0 {
		return 0, &BuildError{Output: reduce(added)}
	}

	if err := s.snap.apply(applied); err != nil {
		return 0, s.fail(err)
	}

	return len(after), nil
}

// lostMain reports a package main that the operation left without the func
// main it had. Nothing else would: the build check compiles and does not
// link, and a missing main is the linker's error.
func (s *session) lostMain(dirs []string) error {
	for _, dir := range dirs {
		if !s.hasMain(dir, s.tree.read) && s.hasMain(dir, s.snap.before) {
			return &BuildError{Output: "  " + dir + ": function main is undeclared in the main package"}
		}
	}

	return nil
}

// hasMain reports whether the kept non-test files of a directory, as read
// gives them, declare func main in package main.
func (s *session) hasMain(dir string, read func(rel string) ([]byte, error)) bool {
	for rel := range s.snap.kept {
		if path.Dir(rel) != dir || strings.HasSuffix(rel, testSuffix) {
			continue
		}

		src, err := read(rel)
		if err != nil {
			continue
		}

		pf, err := parseSource(rel, src)
		if err != nil || pf.ast.Name.Name != mainName {
			continue
		}

		for _, d := range declarationsOf(pf) {
			if d.kind == kindFunc && d.name == mainName {
				return true
			}
		}
	}

	return false
}

// fail restores the tree and returns the error that caused it, joined with
// any failure to restore.
func (s *session) fail(err error) error {
	if restoreErr := s.snap.restore(); restoreErr != nil {
		return fmt.Errorf("%w\nand restoring the files failed: %w", err, restoreErr)
	}

	return err
}

// packageDirs is the directories of files that still hold a Go file, as the
// go tool rejects a directory with none.
func (s *session) packageDirs(files []string) []string {
	seen := map[string]bool{}

	var out []string

	for _, file := range files {
		dir := path.Dir(file)
		if seen[dir] {
			continue
		}

		seen[dir] = true

		if names, err := s.tree.goFilesIn(dir); err == nil && len(names) > 0 {
			out = append(out, dir)
		}
	}

	sort.Strings(out)

	return out
}

var (
	diagnosticPattern = regexp.MustCompile(`^\S+\.go:\d+(:\d+)?: `)
	positionPattern   = regexp.MustCompile(`\S+\.go:\d+(:\d+)?:?`)
)

// diagnostics is the compiler's file:line lines from a build's output, each
// once however many test variants of the package repeated it. Output with
// none, which is the go tool failing before the compiler ran, is kept
// whole apart from the package headers.
func diagnostics(output string) []string {
	var found, rest []string

	seen := map[string]bool{}

	for line := range strings.SplitSeq(strings.TrimSpace(output), "\n") {
		if seen[line] || line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		seen[line] = true

		if diagnosticPattern.MatchString(line) {
			found = append(found, line)
		} else {
			rest = append(rest, line)
		}
	}

	if len(found) == 0 {
		return rest
	}

	return found
}

// addedDiagnostics returns the lines of after that before has no match
// for. Two lines match when they say the same thing once positions are
// dropped, since an edit moves every line below it, and once the names of
// a rename are read as one.
func addedDiagnostics(before, after, names []string) []string {
	names = append([]string(nil), names...)
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })

	message := func(line string) string {
		line = positionPattern.ReplaceAllString(line, "")
		for _, name := range names {
			line = strings.ReplaceAll(line, name, "\x00")
		}

		return line
	}

	had := map[string]int{}
	for _, line := range before {
		had[message(line)]++
	}

	var added []string

	for _, line := range after {
		if key := message(line); had[key] > 0 {
			had[key]--
		} else {
			added = append(added, line)
		}
	}

	return added
}

// reduce caps diagnostics so a cascade of errors costs a reader ten lines.
func reduce(lines []string) string {
	if extra := len(lines) - maxBuildLines; extra > 0 {
		lines = append(lines[:maxBuildLines:maxBuildLines], fmt.Sprintf("(+%d more)", extra))
	}

	return "  " + strings.Join(lines, "\n  ")
}
