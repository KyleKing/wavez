package gate

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kyleking/wavez/internal/mutate"
	"github.com/kyleking/wavez/internal/tool"
)

// maxMutants bounds one run. Each mutant costs a full test invocation, so
// an unbounded change set would turn a verification round into a suite run
// many times over. What was dropped is reported rather than silently cut.
const maxMutants = 30

// Workspaces creates and releases an isolated working copy of a repository.
// A mutant must never be written into the tree the user is editing, and a
// crashed run must not be able to leave mutated source behind.
type Workspaces interface {
	AddWorkspace(ctx context.Context, repoRoot, name, dir string) error
	ForgetWorkspace(ctx context.Context, repoRoot, name string) error
}

// MutationGate asks what coverage cannot: whether the selected tests
// actually check the changed lines, or merely execute them. It mutates only
// the changed line ranges and runs only the selection, so the cost is a
// handful of test invocations rather than a suite sweep.
//
// It is not in the default gate list. Every mutant costs a test run, so
// this is invoked deliberately until there is a measured per-run cost worth
// charging to every verification round. `wavez -mutate` still exits nonzero
// on a survivor, because there the user asked the question.
type MutationGate struct {
	workspaces Workspaces
	repoRoot   string
}

// NewMutationGate builds a gate over repoRoot, isolating mutants with
// workspaces.
func NewMutationGate(repoRoot string, workspaces Workspaces) *MutationGate {
	return &MutationGate{repoRoot: repoRoot, workspaces: workspaces}
}

// Name identifies this gate in the gate log.
func (*MutationGate) Name() string { return "mutation" }

// Resources reports the go-test key: a mutation run drives the same build
// cache as the test and build gates, and interleaving them would have each
// invalidating the other's work.
func (*MutationGate) Resources() []string { return []string{goTestResource} }

// Run mutates the changed Go lines and reports every mutant the selection
// failed to kill. A survivor says the change is not checked, which is weak
// work rather than broken work, so it is reported as an Advisory and the
// gate passes.
func (g *MutationGate) Run(ctx context.Context, rc RunContext) (Result, error) {
	changedGo := len(goFiles(rc.Changes))

	args := buildTestArgs(rc.Selection)
	if changedGo > 0 && len(args) == 0 {
		return ExaminedNothing(g.Name(), rc.Selection.Level, fmt.Sprintf(
			"selection produced no tests for %d changed Go file(s), so no mutant could be killed",
			changedGo,
		)), nil
	}

	mutants, dropped, err := g.plan(rc)
	if err != nil {
		return Result{}, err
	}

	if len(mutants) == 0 {
		if changedGo > 0 {
			return ExaminedNothing(g.Name(), rc.Selection.Level, fmt.Sprintf(
				"no mutable expression on the changed lines of %d Go file(s); "+
					"the change may be unverifiable by this gate rather than verified",
				changedGo,
			)), nil
		}

		return Result{Gate: g.Name(), Level: rc.Selection.Level, Pass: true}, nil
	}

	return g.runMutants(ctx, rc, mutants, dropped, args)
}

// plan builds the mutant list for the change set, and reports how many were
// dropped by maxMutants so a bounded run never reads as a complete one.
func (g *MutationGate) plan(rc RunContext) ([]mutate.Mutant, int, error) {
	var all []mutate.Mutant

	for _, change := range goChanges(rc.Changes) {
		path := change.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(g.repoRoot, path)
		}

		src, err := os.ReadFile(path) // #nosec G304 -- path comes from this run's own change set
		if err != nil {
			return nil, 0, fmt.Errorf("mutation gate: reading %s: %w", change.Path, err)
		}

		rel, relErr := filepath.Rel(g.repoRoot, path)
		if relErr != nil {
			rel = change.Path
		}

		mutants, err := mutate.Mutants(rel, src, change.Ranges)
		if err != nil {
			return nil, 0, fmt.Errorf("mutation gate: %w", err)
		}

		all = append(all, mutants...)
	}

	if len(all) <= maxMutants {
		return all, 0, nil
	}

	return all[:maxMutants], len(all) - maxMutants, nil
}

// runMutants writes each mutant into an isolated workspace and runs the
// selection against it.
func (g *MutationGate) runMutants(
	ctx context.Context, rc RunContext, mutants []mutate.Mutant, dropped int, args []string,
) (Result, error) {
	dir := filepath.Join(os.TempDir(), "wavez-mutation-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	name := filepath.Base(dir)

	if err := g.workspaces.AddWorkspace(ctx, g.repoRoot, name, dir); err != nil {
		return Result{}, fmt.Errorf("mutation gate: %w", err)
	}

	// Cleanup only: every verdict is already collected by the time these run.
	defer func() {
		_ = g.workspaces.ForgetWorkspace(ctx, g.repoRoot, name) //nolint:errcheck // cleanup
		_ = os.RemoveAll(dir)                                   //nolint:errcheck // cleanup
	}()

	result := Result{Gate: g.Name(), Level: rc.Selection.Level}

	for _, m := range mutants {
		survived, err := g.survives(ctx, dir, m, args)
		if err != nil {
			return Result{}, err
		}

		result.Examined++

		if survived {
			result.Advisories = append(result.Advisories, TrimmedFailure{
				Test:   "survived-mutant",
				Frames: []string{m.Describe()},
			})
		}
	}

	if dropped > 0 {
		result.Advisories = append(result.Advisories, TrimmedFailure{
			Test:   "mutants-dropped",
			Frames: []string{fmt.Sprintf("%d further mutant(s) were not run, so this pass is partial", dropped)},
		})
	}

	result.Pass = true

	return result, nil
}

// survives writes one mutant into dir and reports whether the selection
// still passed. A mutant whose file no longer builds is not a survivor and
// not a kill: it proves nothing about the tests, so it is skipped.
//
// Writes go through an os.Root anchored at the workspace, so a change-set
// path can never resolve outside it, symlinks included.
func (*MutationGate) survives(ctx context.Context, dir string, m mutate.Mutant, args []string) (bool, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false, fmt.Errorf("mutation gate: opening workspace %s: %w", dir, err)
	}

	defer func() {
		_ = root.Close() //nolint:errcheck // cleanup
	}()

	original, err := readInRoot(root, m.Path)
	if err != nil {
		return false, fmt.Errorf("mutation gate: reading %s in workspace: %w", m.Path, err)
	}

	if err := writeInRoot(root, m.Path, m.Source); err != nil {
		return false, fmt.Errorf("mutation gate: applying %s: %w", m.Describe(), err)
	}

	// The workspace is discarded either way; restoring keeps one mutant from
	// being read as part of the next one.
	defer func() {
		_ = writeInRoot(root, m.Path, original) //nolint:errcheck // cleanup
	}()

	//nolint:gosec // args are a fixed subset of Selection's own test and package names
	cmd := exec.CommandContext(ctx, "go", append([]string{"test", "-count=1", "-json"}, args...)...)
	cmd.Dir = dir

	out, _ := cmd.Output() //nolint:errcheck // status carried by the parsed summary, not this call's error

	summary, err := ParseGoTestJSON(bytes.NewReader(out))
	if err != nil {
		return false, fmt.Errorf("mutation gate: %w", err)
	}

	if summary.BuildFailed {
		return false, nil
	}

	return summary.Pass, nil
}

const mutantPerm = 0o600

func readInRoot(root *os.Root, path string) ([]byte, error) {
	f, err := root.Open(filepath.FromSlash(path))
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	defer func() {
		_ = f.Close() //nolint:errcheck // read-only
	}()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	return data, nil
}

func writeInRoot(root *os.Root, path string, data []byte) error {
	f, err := root.OpenFile(filepath.FromSlash(path), os.O_WRONLY|os.O_TRUNC, mutantPerm)
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

// goChanges narrows a change set to its Go files, keeping the line ranges
// goFiles discards: this gate mutates lines, not whole files.
func goChanges(changes []tool.Change) []tool.Change {
	out := make([]tool.Change, 0, len(changes))

	for _, c := range changes {
		if filepath.Ext(c.Path) == ".go" {
			out = append(out, c)
		}
	}

	return out
}
