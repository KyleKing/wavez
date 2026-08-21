package gate_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/vcs"
)

var errNoDiff = errors.New("working copy unreadable")

// fixedDiff stands in for jj: the gate only needs the run's hunks as a
// git-format patch, and where that text came from is not its business.
type fixedDiff string

func (d fixedDiff) WorkingCopyDiff(context.Context, string) (string, error) { return string(d), nil }

type brokenDiff struct{}

func (brokenDiff) WorkingCopyDiff(context.Context, string) (string, error) { return "", errNoDiff }

type brokenWorkspaces struct{}

func (brokenWorkspaces) AddWorkspace(context.Context, string, string, string) error { return errNoDiff }

func (brokenWorkspaces) ForgetWorkspace(context.Context, string, string) error { return nil }

const emptyStateTestFile = `package view

import (
	"strings"
	"testing"
)

func TestEmptyStateKeepsHeading(t *testing.T) {
	if !strings.Contains(EmptyState(), "No threads yet") {
		t.Error("heading missing")
	}
}

func TestEmptyStateHasHint(t *testing.T) {
	if !strings.Contains(EmptyState(), "Press n to start one") {
		t.Error("hint missing")
	}
}
`

// The motivating failure: a run asked to replace the empty-state string
// appended a second line and wrote a test asserting both, so every gate went
// green on a change one of those assertions could never have detected.
func TestFailToPassGateNamesTheAssertionThatWouldHavePassedAnyway(t *testing.T) {
	t.Parallel()

	before := []string{"package view", "", "func EmptyState() string {", `	return "No threads yet"`, "}"}
	after := []string{
		"package view", "",
		"func EmptyState() string {", `	return "No threads yet\nPress n to start one"`, "}",
	}

	root := fixtureModule(t, map[string][]string{
		"view/empty.go":      after,
		"view/empty_test.go": strings.Split(strings.TrimSuffix(emptyStateTestFile, "\n"), "\n"),
	})

	result := runFailToPass(t, root, wholeFileDiff("view/empty.go", before, after), []tool.Change{
		{Path: "view/empty.go"},
		{Path: "view/empty_test.go"},
	})

	if !result.Pass || len(result.Failures) != 0 {
		t.Errorf("Pass = %v, Failures = %+v: a weak test is advisory, not a failing gate",
			result.Pass, result.Failures)
	}

	if result.Examined != 2 {
		t.Errorf("Examined = %d, want 2", result.Examined)
	}

	if len(result.Advisories) != 1 || result.Advisories[0].Test != gate.SurvivedRevertTest {
		t.Fatalf("Advisories = %+v, want one %s", result.Advisories, gate.SurvivedRevertTest)
	}

	frames := strings.Join(result.Advisories[0].Frames, " ")
	if !strings.Contains(frames, "TestEmptyStateKeepsHeading") {
		t.Errorf("frames = %q, want the surviving test named", frames)
	}
}

func TestFailToPassGateVerdicts(t *testing.T) {
	t.Parallel()

	greetBefore := []string{"package greet", "", "func Greet() string {", `	return "hi"`, "}"}
	greetAfter := []string{"package greet", "", "func Greet() string {", `	return "hello"`, "}"}
	addedBefore := []string{"package greet", "", "func Greet() string {", `	return "hi"`, "}"}
	addedAfter := []string{
		"package greet", "",
		"func Greet() string {", `	return "hi"`, "}", "",
		"func Shout() string {", `	return "HI"`, "}",
	}

	tests := []struct {
		name       string
		test       string
		before     []string
		after      []string
		examined   int
		advisories int
	}{
		{
			name:     "a test that asserts the changed value dies with it",
			test:     "func TestGreet(t *testing.T) {\n\tif Greet() != \"hello\" {\n\t\tt.Error(\"stale\")\n\t}\n}",
			before:   greetBefore,
			after:    greetAfter,
			examined: 1,
		},
		{
			name:       "a test that asserts what did not change survives it",
			test:       "func TestGreet(t *testing.T) {\n\tif Greet() == \"\" {\n\t\tt.Error(\"empty\")\n\t}\n}",
			before:     greetBefore,
			after:      greetAfter,
			examined:   1,
			advisories: 1,
		},
		{
			name:     "a test of a symbol the change introduced cannot build without it",
			test:     "func TestShout(t *testing.T) {\n\tif Shout() != \"HI\" {\n\t\tt.Error(\"stale\")\n\t}\n}",
			before:   addedBefore,
			after:    addedAfter,
			examined: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := fixtureModule(t, map[string][]string{
				"greet/greet.go":      tt.after,
				"greet/greet_test.go": strings.Split("package greet\n\nimport \"testing\"\n\n"+tt.test, "\n"),
			})

			result := runFailToPass(t, root, wholeFileDiff("greet/greet.go", tt.before, tt.after), []tool.Change{
				{Path: "greet/greet.go"},
				{Path: "greet/greet_test.go"},
			})

			if !result.Pass {
				t.Errorf("Pass = false (failures %+v): the gate advises, it does not block", result.Failures)
			}

			if len(result.Advisories) != tt.advisories {
				t.Errorf("Advisories = %+v, want %d", result.Advisories, tt.advisories)
			}

			if result.Examined != tt.examined {
				t.Errorf("Examined = %d, want %d", result.Examined, tt.examined)
			}
		})
	}
}

// assertAbstention checks the two halves of an abstention: a silent one
// carries its reason to the log and nothing to the model, and a loud one
// names itself so a client can tell it from an ordinary failing check.
func assertAbstention(t *testing.T, result gate.Result, silent bool) {
	t.Helper()

	if silent {
		if result.Reason == "" {
			t.Error("an abstention with no reason is indistinguishable from a clean run")
		}

		if len(result.Failures) != 0 {
			t.Errorf("Failures = %+v, want none; the model has nothing to act on", result.Failures)
		}

		return
	}

	if len(result.Failures) != 1 || result.Failures[0].Test != gate.ExaminedNothingTest {
		t.Errorf("Failures = %+v, want %s", result.Failures, gate.ExaminedNothingTest)
	}
}

// An abstention is auditable in the log and silent to the model, except
// where the gate expected work it could not do. A run that changed only
// source is told nothing: reporting "no test detects this change" as a
// failure reads as "write a test", and a run that satisfies that by writing
// any test at all has cheated the condition rather than met it.
func TestFailToPassGateAbstainsRatherThanPassing(t *testing.T) {
	t.Parallel()

	src := []string{"package greet", "", "func Greet() string {", `	return "hello"`, "}"}
	testSrc := []string{
		"package greet", "", "import \"testing\"", "", "func TestGreet(t *testing.T) {",
		"\tif Greet() == \"\" {", "\t\tt.Error(\"empty\")", "\t}", "}",
	}

	tests := []struct {
		name     string
		changes  []tool.Change
		wantPass bool
	}{
		{
			name:     "code changed with no test touched",
			changes:  []tool.Change{{Path: "greet/greet.go"}},
			wantPass: true,
		},
		{
			name:     "test-only change has nothing to revert",
			changes:  []tool.Change{{Path: "greet/greet_test.go"}},
			wantPass: true,
		},
		{
			name: "the working copy holds no hunk for the changed file",
			changes: []tool.Change{
				{Path: "greet/greet.go"},
				{Path: "greet/greet_test.go", Ranges: []tool.LineRange{{Start: 5, End: 9}}},
			},
			wantPass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := fixtureModule(t, map[string][]string{
				"greet/greet.go":      src,
				"greet/greet_test.go": testSrc,
			})

			result := runFailToPass(t, root, "", tt.changes)

			if result.Pass != tt.wantPass {
				t.Errorf("Pass = %v, want %v", result.Pass, tt.wantPass)
			}

			if result.Examined != 0 {
				t.Errorf("Examined = %d, want 0", result.Examined)
			}

			assertAbstention(t, result, tt.wantPass)
		})
	}
}

// A change set this gate has no opinion on is not an abstention: nothing in
// it could have been checked by a Go test either way.
func TestFailToPassGatePassesOnNonGoChanges(t *testing.T) {
	t.Parallel()

	root := fixtureModule(t, map[string][]string{
		"greet/greet.go": {"package greet", "", "func Greet() string {", `	return "hello"`, "}"},
	})

	result := runFailToPass(t, root, "", []tool.Change{{Path: "README.md"}})
	if !result.Pass {
		t.Errorf("Pass = false on a change set with no Go file: %+v", result.Failures)
	}
}

func TestFailToPassGateFailsClosedOnVCSErrors(t *testing.T) {
	t.Parallel()

	src := []string{"package greet", "", "func Greet() string {", `	return "hello"`, "}"}
	testSrc := []string{
		"package greet", "", "import \"testing\"", "", "func TestGreet(t *testing.T) {",
		"\tif Greet() == \"\" {", "\t\tt.Error(\"empty\")", "\t}", "}",
	}
	changes := []tool.Change{{Path: "greet/greet.go"}, {Path: "greet/greet_test.go"}}
	diff := wholeFileDiff("greet/greet.go", src, src)

	tests := []struct {
		workspaces gate.Workspaces
		working    gate.WorkingCopy
		name       string
	}{
		{name: "the diff cannot be read", workspaces: copyWorkspaces{}, working: brokenDiff{}},
		{name: "the workspace cannot be created", workspaces: brokenWorkspaces{}, working: fixedDiff(diff)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := fixtureModule(t, map[string][]string{
				"greet/greet.go":      src,
				"greet/greet_test.go": testSrc,
			})

			g := gate.NewFailToPassGate(root, tt.workspaces, tt.working)

			result, err := g.Run(t.Context(), gate.RunContext{RepoRoot: root, Changes: changes})
			if err == nil {
				t.Fatalf("Run = %+v, want an error", result)
			}

			if result.Pass {
				t.Error("Pass = true on a gate that could not run")
			}
		})
	}
}

// The end-to-end path against real jj, which is the part copyWorkspaces
// cannot exercise: `jj workspace add -r @` has to carry the uncommitted
// change, or the gate examines a tree without the change it was asked about.
//
// It sets jj's author identity in the process environment, so it cannot run
// beside a parallel test.
func TestFailToPassGateOnRealJJWorkspace(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj not installed")
	}

	t.Setenv("JJ_USER", "wavez test")
	t.Setenv("JJ_EMAIL", "test@example.com")

	root := fixtureModule(t, map[string][]string{
		"view/empty.go": {"package view", "", "func EmptyState() string {", `	return "No threads yet"`, "}"},
	})

	runJJ(t, root, "git", "init")
	runJJ(t, root, "commit", "-m", "base")

	writeLines(t, root, "view/empty.go", []string{
		"package view", "",
		"func EmptyState() string {", `	return "No threads yet\nPress n to start one"`, "}",
	})
	writeLines(t, root, "view/empty_test.go", strings.Split(strings.TrimSuffix(emptyStateTestFile, "\n"), "\n"))

	jj := vcs.NewJj()
	g := gate.NewFailToPassGate(root, jj, jj)

	start := time.Now()

	result, err := g.Run(t.Context(), gate.RunContext{
		RepoRoot: root,
		Changes:  []tool.Change{{Path: "view/empty.go"}, {Path: "view/empty_test.go"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	t.Logf("fail-to-pass gate over 2 tests in a real jj workspace: %s", time.Since(start).Round(time.Millisecond))

	if !result.Pass || result.Examined != 2 || len(result.Advisories) != 1 {
		t.Fatalf("Pass = %v, Examined = %d, Advisories = %+v, want true, 2, and one advisory",
			result.Pass, result.Examined, result.Advisories)
	}

	frames := strings.Join(result.Advisories[0].Frames, " ")
	if !strings.Contains(frames, "TestEmptyStateKeepsHeading") {
		t.Errorf("frames = %q, want the surviving test named", frames)
	}
}

func runFailToPass(t *testing.T, root, diff string, changes []tool.Change) gate.Result {
	t.Helper()

	g := gate.NewFailToPassGate(root, copyWorkspaces{}, fixedDiff(diff))

	result, err := g.Run(t.Context(), gate.RunContext{RepoRoot: root, Changes: changes})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	return result
}

// fixtureModule writes a throwaway Go module whose files are given as line
// slices, so a test's source and the diff it hands the gate are built from
// the same lines and cannot drift apart.
func fixtureModule(t *testing.T, files map[string][]string) string {
	t.Helper()

	root := t.TempDir()
	writeLines(t, root, "go.mod", []string{"module ftpmod", "", "go 1.25"})

	for rel, lines := range files {
		writeLines(t, root, rel, lines)
	}

	return root
}

func writeLines(t *testing.T, root, rel string, lines []string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", rel, err)
	}
}

// wholeFileDiff renders the git-format patch that turns before into after as
// a single hunk with no context lines.
func wholeFileDiff(path string, before, after []string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n", path, path, path, path)
	fmt.Fprintf(&b, "@@ -1,%d +1,%d @@\n", len(before), len(after))

	for _, line := range before {
		b.WriteString("-" + line + "\n")
	}

	for _, line := range after {
		b.WriteString("+" + line + "\n")
	}

	return b.String()
}

func runJJ(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "jj", args...) //nolint:gosec // fixed subcommands from this test
	cmd.Dir = dir

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("jj %s: %v: %s", strings.Join(args, " "), err, out)
	}
}
