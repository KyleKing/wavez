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

const maxBuildLines = 10

// build compiles the packages in dirs together with their tests, without
// linking or running anything. `go list -export` is the cheapest command
// that does: it compiles each package and each test variant, and exits
// non-zero with the compiler's diagnostics when one does not.
func (t toolchain) build(ctx context.Context, dirs []string) error {
	base := []string{"list", "-test", "-export", "-f", "{{.ImportPath}}"}

	args := make([]string, 0, len(base)+len(dirs))
	args = append(args, base...)

	for _, dir := range dirs {
		args = append(args, "./"+dir)
	}

	_, err := t.run(ctx, t.goTool, args...)

	return err
}

// verify confirms the packages an operation touched still compile. When
// they do not it restores every file and builds once more, which is what
// tells a tree the operation broke from one that was broken already.
func (s *session) verify(ctx context.Context, files []string) error {
	dirs := s.packageDirs(files)
	if len(dirs) == 0 {
		return nil
	}

	err := s.tools.build(ctx, dirs)
	if err == nil {
		return nil
	}

	var failed *commandError
	if !errors.As(err, &failed) {
		return s.fail(err)
	}

	if restoreErr := s.snap.restore(); restoreErr != nil {
		return fmt.Errorf("%s\nand restoring the files failed: %w", reduceOutput(failed.output), restoreErr)
	}

	var before *commandError
	if errors.As(s.tools.build(ctx, s.packageDirs(files)), &before) {
		return &BuildError{Output: reduceOutput(before.output), Preexisting: true}
	}

	return &BuildError{Output: reduceOutput(failed.output)}
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

var diagnosticPattern = regexp.MustCompile(`^\S+\.go:\d+(:\d+)?: `)

// reduceOutput keeps the compiler's file:line diagnostics and drops the
// package headers and notes around them, capped so a cascade of errors
// costs a reader ten lines.
func reduceOutput(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")

	kept := make([]string, 0, len(lines))

	for _, line := range lines {
		if diagnosticPattern.MatchString(line) {
			kept = append(kept, line)
		}
	}

	if len(kept) == 0 {
		kept = lines
	}

	if extra := len(kept) - maxBuildLines; extra > 0 {
		kept = append(kept[:maxBuildLines], fmt.Sprintf("(+%d more)", extra))
	}

	return "  " + strings.Join(kept, "\n  ")
}
