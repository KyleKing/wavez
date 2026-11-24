package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// detestBin is the program that selects tests from a trace of what each one
// executes, and detestDir is where a project that uses it keeps its ledger.
const (
	detestBin = "detest"
	detestDir = ".detest"
)

// detestAbstained is the status detest exits with when its selection cannot
// be trusted: no trace, a stale one, or a change to a dependency or to the
// test configuration.
const detestAbstained = 3

// errDetest is a detest that answered with something other than a selection.
var errDetest = errors.New("detest select")

// topLevelTest finds the tests a Go test file declares.
var topLevelTest = regexp.MustCompile(`(?m)^func (Test\w*)\(`)

// Detest returns a Selector that asks `detest select --worktree` which
// tests reach the session's change, and whether the project at root can use
// one: it keeps a .detest directory and detest is on PATH.
func Detest(root string) (DetestSelector, bool) {
	if info, err := os.Stat(filepath.Join(root, detestDir)); err != nil || !info.IsDir() {
		return DetestSelector{}, false
	}

	bin, err := exec.LookPath(detestBin)
	if err != nil {
		return DetestSelector{}, false
	}

	return DetestSelector{bin: bin}, true
}

// DetestSelector selects tests by running detest.
type DetestSelector struct {
	bin string
}

// detestAnswer is the part of detest's JSON the selector reads.
type detestAnswer struct {
	Status string `json:"status"`
	Picks  []struct {
		ID string `json:"id"`
	} `json:"picks"`
	// TestFiles are run whole: the change's own test files, and the ones
	// nearest to changed code no traced test reaches.
	TestFiles []string `json:"test_files"`
}

// Select implements Selector. A detest that abstains or fails answers with
// an incomplete selection, which runs every test.
func (d DetestSelector) Select(ctx context.Context, root, base string, _ []string) (Selection, error) {
	// Outside a session the change is whatever differs from the last commit.
	if base == "" {
		base = "HEAD"
	}

	args := []string{"select", "-C", root, "--worktree", "--base", base, "--json"}
	cmd := exec.CommandContext(ctx, d.bin, args...) //nolint:gosec // a fixed program found on PATH

	var stdout, stderr bytes.Buffer

	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == detestAbstained {
			return Selection{}, nil
		}

		return Selection{}, fmt.Errorf("%w: %w: %s", errDetest, err, strings.TrimSpace(stderr.String()))
	}

	var answer detestAnswer
	if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
		return Selection{}, fmt.Errorf("%w: reading its answer: %w", errDetest, err)
	}

	if answer.Status != "selected" {
		return Selection{}, nil
	}

	tests := map[string][]string{}

	for _, pick := range answer.Picks {
		unit, name, ok := strings.Cut(pick.ID, "::")
		if !ok || strings.HasSuffix(unit, ".py") {
			continue
		}

		// A subtest is run through the test that holds it.
		name, _, _ = strings.Cut(name, "/")
		add(tests, unit, name)
	}

	for _, file := range answer.TestFiles {
		if !strings.HasSuffix(file, "_test.go") {
			continue
		}

		//nolint:gosec // a path detest named inside the project
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			// A test file that was deleted has nothing left to run.
			continue
		}

		for _, match := range topLevelTest.FindAllSubmatch(src, -1) {
			add(tests, path.Dir(file), string(match[1]))
		}
	}

	return Selection{Tests: tests, Complete: true}, nil
}

// add puts a test under its package, as the ./-prefixed pattern go takes.
func add(tests map[string][]string, dir, name string) {
	pkg := "./" + strings.TrimPrefix(path.Clean(dir), "./")
	if dir == "." || dir == "" {
		pkg = "./"
	}

	if !slices.Contains(tests[pkg], name) {
		tests[pkg] = append(tests[pkg], name)
	}
}
