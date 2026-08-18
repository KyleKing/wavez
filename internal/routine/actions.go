package routine

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

// GatePrefix is the action-name prefix every gate-wrapping action carries,
// so `gate.format` in a routine and the `format` gate are recognizably the
// same check.
const GatePrefix = "gate."

// RunActionName is the action that executes a CLI. Its argv is executed
// directly and never through a shell, so a project cannot smuggle a
// pipeline or a redirect into a routine.
const RunActionName = "run"

// DefaultRunTimeout bounds one `run` step that declares no timeout of its
// own. It is generous because a routine step is a build or a test suite,
// not an edit-path check.
const DefaultRunTimeout = 10 * time.Minute

// tailLines is how much of a failing command's output survives when none of
// it references a changed file. Gates trim to the frames that touch the
// change; a manual run has no change to trim against, and handing back
// nothing at all would be worse than handing back the end of the output.
const tailLines = 20

// ErrDirEscapes reports a `dir` parameter resolving outside the project.
var ErrDirEscapes = errors.New("dir escapes the project root")

// GateAction adapts one gate.Gate into the action `gate.<name>`, which is
// how DESIGN.md's "gates ship as built-in routines" holds without gates
// knowing routines exist. It takes no parameters: a gate's own scope comes
// from the change batch, not from the routine that invoked it.
func GateAction(g gate.Gate) Action {
	return Action{
		Name: GatePrefix + g.Name(),
		Bind: func(params map[string]any) (Bound, error) {
			if err := rejectUnknown(params); err != nil {
				return Bound{}, err
			}

			return Bound{
				Resources: g.Resources(),
				Run: func(ctx context.Context, env Env) (Outcome, error) {
					result, err := g.Run(ctx, gate.RunContext{
						RepoRoot: env.Root, Changes: env.Changes, Selection: env.Selection,
					})
					if err != nil {
						return Outcome{}, fmt.Errorf("gate %s: %w", g.Name(), err)
					}

					return Outcome{Pass: result.Pass, Examined: result.Examined, Failures: result.Failures}, nil
				},
			}, nil
		},
	}
}

// RunAction builds the `run` action for the project at root. Parameters are
// `argv` (required), `dir` (relative to root), and `timeoutMs`.
func RunAction(root string) Action {
	return Action{
		Name: RunActionName,
		Bind: func(params map[string]any) (Bound, error) {
			return bindRun(root, params)
		},
	}
}

func bindRun(root string, params map[string]any) (Bound, error) {
	if err := rejectUnknown(params, "argv", "dir", "timeoutMs"); err != nil {
		return Bound{}, err
	}

	argv, err := stringList(params, "argv")
	if err != nil {
		return Bound{}, err
	}

	if len(argv) == 0 || argv[0] == "" {
		return Bound{}, fmt.Errorf("%w: argv needs a program", ErrEmptyParam)
	}

	rel, err := optionalString(params, "dir", ".")
	if err != nil {
		return Bound{}, err
	}

	dir, err := containedDir(root, rel)
	if err != nil {
		return Bound{}, err
	}

	timeoutMs, err := optionalInt(params, "timeoutMs", int(DefaultRunTimeout/time.Millisecond))
	if err != nil {
		return Bound{}, err
	}

	if timeoutMs <= 0 {
		return Bound{}, fmt.Errorf("%w: timeoutMs must be positive", ErrParamType)
	}

	return Bound{
		Resources: []string{"worktree"},
		Run: func(ctx context.Context, env Env) (Outcome, error) {
			return runCommand(ctx, argv, dir, time.Duration(timeoutMs)*time.Millisecond, env)
		},
	}, nil
}

func runCommand(ctx context.Context, argv []string, dir string, timeout time.Duration, env Env) (Outcome, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// argv comes from the project's own config file, and runs directly
	// rather than through a shell.
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // see above
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err == nil {
		return Outcome{Pass: true, Examined: 1}, nil
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return Outcome{}, fmt.Errorf("running %s: %w", argv[0], err)
	}

	return Outcome{
		Examined: 1,
		Failures: []gate.TrimmedFailure{trimOutput(argv[0], string(out), env.Changes)},
	}, nil
}

// trimOutput applies the gate trimming rule to a command's output: keep the
// lines that reference a changed file, and fall back to the tail when none
// of them do.
func trimOutput(name, out string, changes []tool.Change) gate.TrimmedFailure {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	trimmed := gate.TrimFailure(gate.FailedTest{Name: name, Output: lines}, changedPaths(changes))
	if len(trimmed.Frames) > 0 {
		return trimmed
	}

	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}

	return gate.TrimmedFailure{Test: name, Frames: lines}
}

func changedPaths(changes []tool.Change) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, c.Path)
	}

	return out
}

// containedDir resolves rel against root and refuses anything outside it. A
// routine names its working directory in a file the project owns, and a
// `dir` of "../.." would still be a step running somewhere the project's
// own sandbox does not cover.
func containedDir(root, rel string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolving project root: %w", err)
	}

	path := rel
	if !filepath.IsAbs(path) {
		path = filepath.Join(abs, path)
	}

	path = filepath.Clean(path)
	if path != abs && !strings.HasPrefix(path, abs+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrDirEscapes, rel)
	}

	return path, nil
}
