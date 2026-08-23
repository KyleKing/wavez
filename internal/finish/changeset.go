package finish

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ChangeSetMatchesTask reports whether what the run wrote intersects what
// the task's own words name. It abstains when the task names no path and no
// symbol, because a bound that fires on every task that phrases itself in
// prose is noise rather than a check.
//
// It is a bound and not a judgment: a run that touched the named file may
// still have done the wrong thing there, and a run that touched none of
// them did not do the task as written.
func ChangeSetMatchesTask(root, task string, changed []string) (Report, error) {
	return matches(root, "the change set does not touch what the task names", task, changed)
}

// ChangeSetMatchesGoal is the same bound against the thread's standing
// goal, which is the harness observation parked as the alternative to a
// model-authored goal: it says this run has not touched what the goal
// names, and never says the goal is wrong.
func ChangeSetMatchesGoal(root, goal string, changed []string) (Report, error) {
	return matches(root, "the change set does not touch what the goal names", goal, changed)
}

func matches(root, check, text string, changed []string) (Report, error) {
	paths := namedPaths(text)
	symbols := namedSymbols(text)

	if len(paths) == 0 && len(symbols) == 0 {
		return Report{}, nil
	}

	if len(changed) == 0 {
		return Report{Findings: []Finding{{Check: check, Detail: "the run changed no file"}}}, nil
	}

	if touchesAny(changed, paths) {
		return Report{}, nil
	}

	if mentionsAny(root, changed, symbols) {
		return Report{}, nil
	}

	return Report{Findings: []Finding{{
		Check:  check,
		Detail: fmt.Sprintf("it names %s, and the run wrote %s", list(paths, symbols), strings.Join(changed, ", ")),
	}}}, nil
}

// namedPaths is every project-relative path the text spells out, whether or
// not it exists: a task naming a file the run is meant to create is naming
// it just as much as one naming a file to edit.
func namedPaths(text string) []string {
	var out []string

	for _, p := range dedupe(pathPattern.FindAllString(text, -1)) {
		if !strings.HasPrefix(p, "/") && !strings.Contains(p, "..") {
			out = append(out, p)
		}
	}

	return out
}

func namedSymbols(text string) []string {
	var out []string

	for _, match := range symbolPattern.FindAllStringSubmatch(text, -1) {
		if name := lastSegment(match[1]); len(name) >= minSymbolLen {
			out = append(out, name)
		}
	}

	return dedupe(out)
}

func touchesAny(changed, paths []string) bool {
	for _, c := range changed {
		clean := filepath.ToSlash(filepath.Clean(c))

		for _, p := range paths {
			if clean == p || strings.HasPrefix(clean, strings.TrimSuffix(p, "/")+"/") {
				return true
			}
		}
	}

	return false
}

// maxScanBytes bounds how much of a changed file the symbol scan reads. A
// file larger than this is not one a task named a declaration in.
const maxScanBytes = 1 << 20

func mentionsAny(root string, changed, symbols []string) bool {
	if len(symbols) == 0 {
		return false
	}

	for _, rel := range changed {
		//nolint:gosec // rel comes from the harness's own change set
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}

		if len(body) > maxScanBytes {
			body = body[:maxScanBytes]
		}

		for _, name := range symbols {
			if strings.Contains(string(body), name) {
				return true
			}
		}
	}

	return false
}

func list(paths, symbols []string) string {
	both := append(append([]string{}, paths...), symbols...)

	return strings.Join(both, ", ")
}
