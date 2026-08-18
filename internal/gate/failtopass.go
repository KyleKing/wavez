package gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/tool"
)

// SurvivedRevertTest is the failure name reported for a test the run added or
// changed that still passes once the run's own non-test hunks are reverted.
// It is a fixed string so the condition is greppable in the gate log.
const SurvivedRevertTest = "survives-revert"

const (
	failToPassName = "fail-to-pass"
	goSourceExt    = ".go"
	goTestSubcmd   = "test"
	testJSONFlag   = "-json"
)

var errRevertMismatch = errors.New("workspace content does not match the diff being reverted")

// WorkingCopy reads a repository's uncommitted diff in git format. The gate
// reverts hunks rather than checking out a parent revision, so it needs the
// patch text itself and not a revision id.
type WorkingCopy interface {
	WorkingCopyDiff(ctx context.Context, repoRoot string) (string, error)
}

// FailToPassGate checks the property a green test suite cannot: that the
// tests a run wrote actually detect the change that run made. It reverts the
// run's non-test hunks in a throwaway workspace and re-runs only the tests
// the run added or modified. One that still passes there proves nothing about
// the change, and is reported as SurvivedRevertTest.
//
// The verdict is per test, so a run that wrote one load-bearing test beside
// one that would pass either way still fails the gate.
//
// It assumes the tests pass on the tree as written, which is GoTestGate's
// job. Run without that, a test that is simply broken reads here as one the
// revert killed.
type FailToPassGate struct {
	workspaces Workspaces
	working    WorkingCopy
	repoRoot   string
}

// NewFailToPassGate builds a gate over repoRoot, reading the run's hunks from
// working and isolating the revert with workspaces.
func NewFailToPassGate(repoRoot string, workspaces Workspaces, working WorkingCopy) *FailToPassGate {
	return &FailToPassGate{repoRoot: repoRoot, workspaces: workspaces, working: working}
}

// Name identifies this gate in the gate log.
func (*FailToPassGate) Name() string { return failToPassName }

// Resources reports the go-test key: the reverted workspace shares the main
// tree's build cache with the test and build gates.
func (*FailToPassGate) Resources() []string { return []string{goTestResource} }

// Run reverts the run's non-test Go hunks and re-runs the run's own tests
// against the reverted tree. Any jj, patch, or parse failure returns an error
// rather than a passing Result: a check that could not run has not passed.
func (g *FailToPassGate) Run(ctx context.Context, rc RunContext) (Result, error) {
	if len(goFiles(rc.Changes)) == 0 {
		return Result{Gate: g.Name(), Level: rc.Selection.Level, Pass: true}, nil
	}

	testChanges := goChangesMatching(rc.Changes, true)
	codeChanges := goChangesMatching(rc.Changes, false)
	if len(testChanges) == 0 {
		return Abstained(g.Name(), rc.Selection.Level, fmt.Sprintf(
			"%d changed Go file(s) and no changed test file, so this run wrote no test to check",
			len(goFiles(rc.Changes)))), nil
	}

	if len(codeChanges) == 0 {
		return Abstained(g.Name(), rc.Selection.Level,
			"the change set is test-only, so there is no non-test hunk to revert"), nil
	}

	candidates, err := candidateTests(g.repoRoot, testChanges)
	if err != nil {
		return Result{}, err
	}

	if len(candidates) == 0 {
		return Abstained(g.Name(), rc.Selection.Level, fmt.Sprintf(
			"%d changed test file(s) declare no test function on their changed lines", len(testChanges))), nil
	}

	patches, err := g.revertPatches(ctx, codeChanges)
	if err != nil {
		return Result{}, err
	}

	if len(patches) == 0 {
		return ExaminedNothing(g.Name(), rc.Selection.Level, fmt.Sprintf(
			"the working copy holds no hunk for %d changed non-test Go file(s), so nothing could be reverted",
			len(codeChanges))), nil
	}

	return g.runReverted(ctx, rc, candidates, patches)
}

// revertPatches narrows the working-copy diff to the run's own non-test Go
// files.
func (g *FailToPassGate) revertPatches(ctx context.Context, codeChanges []tool.Change) ([]filePatch, error) {
	diff, err := g.working.WorkingCopyDiff(ctx, g.repoRoot)
	if err != nil {
		return nil, fmt.Errorf("fail-to-pass gate: %w", err)
	}

	want := make(map[string]struct{}, len(codeChanges))

	for _, c := range codeChanges {
		rel, relErr := filepath.Rel(g.repoRoot, filepath.Join(g.repoRoot, c.Path))
		if relErr != nil {
			return nil, fmt.Errorf("fail-to-pass gate: locating %s: %w", c.Path, relErr)
		}

		want[filepath.ToSlash(rel)] = struct{}{}
	}

	return parseGitDiff(diff, want), nil
}

// runReverted applies the reverse patches in a throwaway workspace and runs
// the candidate tests there.
func (g *FailToPassGate) runReverted(
	ctx context.Context, rc RunContext, candidates []testFunc, patches []filePatch,
) (Result, error) {
	dir := filepath.Join(os.TempDir(), "wavez-failtopass-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	name := filepath.Base(dir)

	if err := g.workspaces.AddWorkspace(ctx, g.repoRoot, name, dir); err != nil {
		return Result{}, fmt.Errorf("fail-to-pass gate: %w", err)
	}

	defer func() {
		_ = g.workspaces.ForgetWorkspace(ctx, g.repoRoot, name) //nolint:errcheck // cleanup
		_ = os.RemoveAll(dir)                                   //nolint:errcheck // cleanup
	}()

	if err := revertInWorkspace(dir, patches); err != nil {
		return Result{}, err
	}

	summary, err := runCandidates(ctx, dir, candidates)
	if err != nil {
		return Result{}, err
	}

	return verdict(g.Name(), rc.Selection.Level, candidates, summary), nil
}

// verdict turns the reverted run into a Result. A tree that no longer builds
// without the run's change killed every candidate, since none of them can
// pass there.
func verdict(gateName string, level Level, candidates []testFunc, summary GoTestSummary) Result {
	if summary.BuildFailed {
		return Result{Gate: gateName, Level: level, Examined: len(candidates), Pass: true}
	}

	wanted := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		wanted[c.name] = struct{}{}
	}

	result := Result{Gate: gateName, Level: level}

	for _, id := range summary.PassedTests {
		name := testName(id)
		if _, ok := wanted[name]; !ok || strings.Contains(name, "/") {
			continue
		}

		result.Examined++
		result.Failures = append(result.Failures, TrimmedFailure{
			Test:    SurvivedRevertTest,
			Package: testPackage(id),
			Frames:  []string{name + " passes with the run's non-test changes reverted, so it does not check them"},
		})
	}

	for _, f := range summary.FailedTests {
		if _, ok := wanted[f.Name]; ok && !strings.Contains(f.Name, "/") {
			result.Examined++
		}
	}

	if result.Examined == 0 {
		return ExaminedNothing(gateName, level, fmt.Sprintf(
			"none of the %d test(s) this run wrote ran against the reverted tree", len(candidates)))
	}

	result.Pass = len(result.Failures) == 0

	return result
}

func runCandidates(ctx context.Context, dir string, candidates []testFunc) (GoTestSummary, error) {
	names := make([]string, 0, len(candidates))
	pkgs := make([]string, 0, len(candidates))

	for _, c := range candidates {
		names = append(names, c.name)
		pkgs = append(pkgs, c.pkg)
	}

	args := append([]string{goTestSubcmd, "-count=1", testJSONFlag, "-run", regexOf(names)}, dedupe(pkgs)...)

	//nolint:gosec // args are test names and package paths derived from this run's own change set
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir

	out, _ := cmd.Output() //nolint:errcheck // status carried by the parsed summary, not this call's error

	summary, err := ParseGoTestJSON(bytes.NewReader(out))
	if err != nil {
		return GoTestSummary{}, fmt.Errorf("fail-to-pass gate: %w", err)
	}

	return summary, nil
}

func revertInWorkspace(dir string, patches []filePatch) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("fail-to-pass gate: opening workspace %s: %w", dir, err)
	}

	defer func() {
		_ = root.Close() //nolint:errcheck // cleanup
	}()

	for _, p := range patches {
		if err := revertOne(root, p); err != nil {
			return fmt.Errorf("fail-to-pass gate: reverting %s: %w", p.path, err)
		}
	}

	return nil
}

func revertOne(root *os.Root, p filePatch) error {
	if p.created {
		if err := root.Remove(filepath.FromSlash(p.path)); err != nil {
			return fmt.Errorf("removing %s: %w", p.path, err)
		}

		return nil
	}

	var current []byte

	if !p.deleted {
		read, err := readInRoot(root, p.path)
		if err != nil {
			return err
		}

		current = read
	}

	reverted, err := revertLines(current, p.hunks)
	if err != nil {
		return err
	}

	return restoreInRoot(root, p.path, reverted)
}

func restoreInRoot(root *os.Root, path string, data []byte) error {
	f, err := root.OpenFile(filepath.FromSlash(path), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mutantPerm)
	if err != nil {
		return fmt.Errorf("opening %s for writing: %w", path, err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close() //nolint:errcheck // the write error is the one that matters

		return fmt.Errorf("writing %s: %w", path, err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", path, err)
	}

	return nil
}

// testFunc is one test the run itself wrote or changed, with the package it
// has to be run from.
type testFunc struct {
	pkg  string
	name string
}

// TestFunc is one test a change set declares, paired with the package
// argument it has to be run from.
type TestFunc struct {
	Package string
	Name    string
}

// ChangedTests reports the test functions declared on the changed lines of a
// change set's Go test files. It is the artifact question asked without
// running anything: which tests does this change claim to be checked by.
func ChangedTests(repoRoot string, changes []tool.Change) ([]TestFunc, error) {
	found, err := candidateTests(repoRoot, goChangesMatching(changes, true))
	if err != nil {
		return nil, err
	}

	out := make([]TestFunc, 0, len(found))
	for _, f := range found {
		out = append(out, TestFunc{Package: f.pkg, Name: f.name})
	}

	return out, nil
}

// candidateTests collects the test functions declared on the changed lines of
// the run's own test files. A changed test file with no line ranges
// contributes every test it declares.
func candidateTests(repoRoot string, changes []tool.Change) ([]testFunc, error) {
	var out []testFunc

	for _, c := range changes {
		path := c.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(repoRoot, path)
		}

		src, err := os.ReadFile(path) // #nosec G304 -- path comes from this run's own change set
		if err != nil {
			return nil, fmt.Errorf("fail-to-pass gate: reading %s: %w", c.Path, err)
		}

		fset := token.NewFileSet()

		file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("fail-to-pass gate: parsing %s: %w", c.Path, err)
		}

		pkg := packageArg(repoRoot, path)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !isTestFuncName(fn.Name.Name) || !declTouched(fset, fn, c.Ranges) {
				continue
			}

			out = append(out, testFunc{pkg: pkg, name: fn.Name.Name})
		}
	}

	return out, nil
}

func packageArg(repoRoot, path string) string {
	rel, err := filepath.Rel(repoRoot, filepath.Dir(path))
	if err != nil || rel == "." {
		return "."
	}

	return "./" + filepath.ToSlash(rel)
}

func isTestFuncName(name string) bool {
	return strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Fuzz")
}

func declTouched(fset *token.FileSet, fn *ast.FuncDecl, ranges []tool.LineRange) bool {
	if len(ranges) == 0 {
		return true
	}

	start := fset.Position(fn.Pos()).Line
	end := fset.Position(fn.End()).Line

	for _, r := range ranges {
		if r.Start <= end && r.End >= start {
			return true
		}
	}

	return false
}

// goChangesMatching narrows a change set to its Go test files or to its Go
// non-test files.
func goChangesMatching(changes []tool.Change, wantTests bool) []tool.Change {
	var out []tool.Change

	for _, c := range changes {
		if filepath.Ext(c.Path) != goSourceExt {
			continue
		}

		if strings.HasSuffix(c.Path, "_test"+goSourceExt) == wantTests {
			out = append(out, c)
		}
	}

	return out
}

func regexOf(names []string) string {
	return "^(" + strings.Join(dedupe(names), "|") + ")$"
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))

	out := make([]string, 0, len(in))

	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}

		seen[s] = struct{}{}

		out = append(out, s)
	}

	sort.Strings(out)

	return out
}

// filePatch is one file's section of a git-format diff. Created and deleted
// name the /dev/null side, since reverting those means removing or restoring
// the file rather than patching it.
type filePatch struct {
	path    string
	hunks   []hunk
	created bool
	deleted bool
}

// hunk holds a diff hunk's body lines with their prefixes, plus how many
// lines each side declared. The counts bound the body, so trailing text after
// the last hunk is never mistaken for a context line.
type hunk struct {
	lines      []string
	newStart   int
	oldRemains int
	newRemains int
}

// parseGitDiff extracts the sections of a git-format diff whose path is in
// want.
func parseGitDiff(diff string, want map[string]struct{}) []filePatch {
	var (
		out []filePatch
		cur *filePatch
		old string
	)

	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			out = keepPatch(out, cur, want)
			cur = &filePatch{}
			old = ""
		case cur == nil:
			continue
		case len(cur.hunks) == 0 && strings.HasPrefix(line, "--- "):
			old = diffPath(line[4:])
			cur.created = old == ""
		case len(cur.hunks) == 0 && strings.HasPrefix(line, "+++ "):
			cur.path = diffPath(line[4:])
			cur.deleted = cur.path == ""

			if cur.deleted {
				cur.path = old
			}
		case strings.HasPrefix(line, "@@"):
			if h, ok := parseHunkHeader(line); ok {
				cur.hunks = append(cur.hunks, h)
			}
		case len(cur.hunks) > 0:
			appendBodyLine(&cur.hunks[len(cur.hunks)-1], line)
		}
	}

	return keepPatch(out, cur, want)
}

func keepPatch(out []filePatch, cur *filePatch, want map[string]struct{}) []filePatch {
	if cur == nil || cur.path == "" || len(cur.hunks) == 0 {
		return out
	}

	if _, ok := want[cur.path]; !ok {
		return out
	}

	return append(out, *cur)
}

func diffPath(field string) string {
	field = strings.TrimSpace(field)
	if field == "/dev/null" {
		return ""
	}

	if idx := strings.Index(field, "/"); idx >= 0 {
		return field[idx+1:]
	}

	return field
}

func parseHunkHeader(line string) (hunk, bool) {
	fields := strings.Fields(line)

	const minHunkFields = 3
	if len(fields) < minHunkFields || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return hunk{}, false
	}

	old, ok := parseRange(fields[1][1:])
	if !ok {
		return hunk{}, false
	}

	nw, ok := parseRange(fields[2][1:])
	if !ok {
		return hunk{}, false
	}

	return hunk{newStart: nw.start, oldRemains: old.count, newRemains: nw.count}, true
}

// diffRange is one side of a hunk header: where it starts in that side's
// file and how many lines it spans.
type diffRange struct {
	start int
	count int
}

func parseRange(field string) (diffRange, bool) {
	const sides = 2

	parts := strings.SplitN(field, ",", sides)

	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return diffRange{}, false
	}

	count := 1
	if len(parts) == sides {
		if count, err = strconv.Atoi(parts[1]); err != nil {
			return diffRange{}, false
		}
	}

	return diffRange{start: start, count: count}, true
}

func appendBodyLine(h *hunk, line string) {
	if strings.HasPrefix(line, `\`) || (h.oldRemains == 0 && h.newRemains == 0) {
		return
	}

	switch {
	case strings.HasPrefix(line, "+"):
		h.newRemains--
	case strings.HasPrefix(line, "-"):
		h.oldRemains--
	case line == "" || strings.HasPrefix(line, " "):
		h.oldRemains--
		h.newRemains--
	default:
		return
	}

	h.lines = append(h.lines, line)
}

// revertLines undoes hunks against content, which must be the post-change
// text the diff describes. Hunks are undone last-first so each one's line
// numbers still hold when it is reached.
func revertLines(content []byte, hunks []hunk) ([]byte, error) {
	lines, trailing := splitLines(content)

	for i := len(hunks) - 1; i >= 0; i-- {
		sides := hunkSides(hunks[i])
		want := sides.want

		at := max(hunks[i].newStart-1, 0)
		if at+len(want) > len(lines) {
			return nil, fmt.Errorf("%w at line %d", errRevertMismatch, hunks[i].newStart)
		}

		for j, w := range want {
			if lines[at+j] != w {
				return nil, fmt.Errorf("%w at line %d", errRevertMismatch, hunks[i].newStart+j)
			}
		}

		next := make([]string, 0, len(lines)-len(want)+len(sides.replacement))
		next = append(next, lines[:at]...)
		next = append(next, sides.replacement...)
		next = append(next, lines[at+len(want):]...)
		lines = next
	}

	return joinLines(lines, trailing), nil
}

// hunkSides splits a hunk body into the lines the file must currently hold
// and the lines that replace them when the hunk is undone.
type hunkSide struct {
	want        []string
	replacement []string
}

func hunkSides(h hunk) hunkSide {
	var want, replacement []string

	for _, line := range h.lines {
		body := ""
		if line != "" {
			body = line[1:]
		}

		switch {
		case strings.HasPrefix(line, "+"):
			want = append(want, body)
		case strings.HasPrefix(line, "-"):
			replacement = append(replacement, body)
		default:
			want = append(want, body)
			replacement = append(replacement, body)
		}
	}

	return hunkSide{want: want, replacement: replacement}
}

func splitLines(content []byte) ([]string, bool) {
	s := string(content)
	if s == "" {
		return nil, true
	}

	if strings.HasSuffix(s, "\n") {
		return strings.Split(strings.TrimSuffix(s, "\n"), "\n"), true
	}

	return strings.Split(s, "\n"), false
}

func joinLines(lines []string, trailingNewline bool) []byte {
	if len(lines) == 0 {
		return nil
	}

	joined := strings.Join(lines, "\n")
	if trailingNewline {
		joined += "\n"
	}

	return []byte(joined)
}
