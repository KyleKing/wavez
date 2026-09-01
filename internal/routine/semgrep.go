package routine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/gate"
)

// SemgrepName is the opt-in semgrep routine's name. Unlike the gate builtins
// it wraps no gate: it is `semgrep` in ".wavez.pkl", not `gate-semgrep`,
// because there is no gate for it to wrap.
const SemgrepName = "semgrep"

// SemgrepActionName is the action the semgrep routine's step invokes.
const SemgrepActionName = "semgrep.scan"

// SemgrepBinary is the executable the step looks for on PATH.
const SemgrepBinary = "semgrep"

// DefaultSemgrepTimeout bounds one scan.
const DefaultSemgrepTimeout = 5 * time.Minute

// semgrepDefinition is the built-in semgrep routine. It is disabled by
// default: not every checkout has semgrep installed, so the routine exists
// only when the project asks for it with `semgrep { enabled = true }` in
// ".wavez.pkl". Naming it there keeps the built-in step and flips the switch;
// a checkout without the binary then abstains rather than fails.
func semgrepDefinition() Definition {
	return Definition{
		Name:     SemgrepName,
		Triggers: []Trigger{TriggerManual},
		Steps:    []StepDef{{Name: "scan", Action: SemgrepActionName}},
		Enabled:  false,
	}
}

// SemgrepAction builds the `semgrep.scan` action for the project at root. It
// takes no parameters: the scan covers the whole project, the way the gate
// builtins take no parameters because their scope comes from elsewhere.
// Options substitute how the binary is resolved and invoked, for tests.
func SemgrepAction(root string, opts ...SemgrepOption) Action {
	s := newSemgrepScanner()
	for _, opt := range opts {
		opt(s)
	}

	return Action{
		Name: SemgrepActionName,
		Bind: func(params map[string]any) (Bound, error) {
			if err := rejectUnknown(params); err != nil {
				return Bound{}, err
			}

			return Bound{
				Resources: []string{"worktree"},
				Run: func(ctx context.Context, _ Env) (Outcome, error) {
					return s.scan(ctx, root)
				},
			}, nil
		},
	}
}

// semgrepRun invokes the binary and returns its stdout. Tests substitute one
// so none of them needs semgrep installed or a PATH to point at it.
type semgrepRun func(ctx context.Context, name string, args []string, dir string) ([]byte, error)

// semgrepScanner shells out to semgrep. Its two function fields are what
// tests substitute, so no test needs the binary or a PATH to point at it.
type semgrepScanner struct {
	lookPath func(name string) (string, error)
	runCmd   semgrepRun
}

// SemgrepOption configures how the semgrep action resolves and runs the
// binary.
type SemgrepOption func(*semgrepScanner)

// WithSemgrepLookPath overrides how the action resolves the semgrep binary,
// for tests that simulate the binary being absent.
func WithSemgrepLookPath(fn func(name string) (string, error)) SemgrepOption {
	return func(s *semgrepScanner) { s.lookPath = fn }
}

// WithSemgrepCommand overrides how the action invokes semgrep, for tests that
// drive a fake process instead of a real binary.
func WithSemgrepCommand(fn semgrepRun) SemgrepOption {
	return func(s *semgrepScanner) { s.runCmd = fn }
}

func newSemgrepScanner() *semgrepScanner {
	return &semgrepScanner{lookPath: exec.LookPath, runCmd: semgrepCommand}
}

// semgrepCommand runs one semgrep invocation and returns its stdout. The
// arguments are fixed at the call site, not read from the project's config,
// so there is no argv to smuggle a redirect through.
func semgrepCommand(ctx context.Context, name string, args []string, dir string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultSemgrepTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // name is the fixed SemgrepBinary constant
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("running %s: %w", name, err)
	}

	return out, nil
}

// semgrepFinding is one result in semgrep's --json output.
type semgrepFinding struct {
	Path    string `json:"path"`
	CheckID string `json:"check_id"`
	Extra   struct {
		Message string `json:"message"`
	} `json:"extra"`
	Start struct {
		Line int `json:"line"`
	} `json:"start"`
}

// semgrepOutput is the slice of semgrep's --json output the step reads.
type semgrepOutput struct {
	Paths struct {
		Scanned []string `json:"scanned"`
	} `json:"paths"`
	Results []semgrepFinding `json:"results"`
}

// scan runs one scan and maps it onto the gate outcome shape. A missing
// binary is an abstention, not an error: the project opted into the check,
// not into a requirement that every checkout install it. A scan that examined
// nothing abstains the same way, and only a real finding fails the step.
func (s *semgrepScanner) scan(ctx context.Context, root string) (Outcome, error) {
	if !s.installed() {
		return Outcome{Pass: true, Examined: 0}, nil
	}

	out, err := s.runCmd(ctx, SemgrepBinary, []string{"scan", "--json", "--quiet"}, root)
	if err != nil {
		if !isFindingsExit(err) {
			return Outcome{}, err
		}

		// Exit status 1 is semgrep's "found something": the JSON on stdout
		// still says what, so the error itself is not the finding.
	}

	var parsed semgrepOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return Outcome{}, fmt.Errorf("parsing %s output: %w", SemgrepBinary, err)
	}

	if len(parsed.Results) == 0 {
		// A clean scan still examined what it scanned; a scan that looked at
		// nothing has nothing to say and abstains.
		return Outcome{Pass: true, Examined: len(parsed.Paths.Scanned)}, nil
	}

	frames := make([]string, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		frames = append(frames, strings.TrimSpace(fmt.Sprintf("%s:%d: %s: %s",
			r.Path, r.Start.Line, r.CheckID, r.Extra.Message)))
	}

	return Outcome{
		Pass:     false,
		Examined: len(parsed.Results),
		Failures: []gate.TrimmedFailure{{Test: SemgrepName, Frames: frames}},
	}, nil
}

// installed reports whether the binary the step shells out to is on PATH.
// A checkout without it abstains: the project opted into the check, not into
// a requirement that every checkout install it.
func (s *semgrepScanner) installed() bool {
	_, err := s.lookPath(SemgrepBinary)

	return err == nil
}

// isFindingsExit reports an exit status semgrep uses for "found something".
// It matches by exit code rather than exec's concrete type, so a substituted
// command that fails the same way reads the same.
func isFindingsExit(err error) bool {
	var ec interface{ ExitCode() int }

	return errors.As(err, &ec) && ec.ExitCode() == 1
}
