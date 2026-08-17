// Package hook runs the two external commands DESIGN.md's table stakes name,
// pre-tool-use and post-tool-use, around a tool call.
//
// A hook is user configuration, the same standing as the destructive-command
// guard and the permission gate: it may refuse a call, and DESIGN.md's rule
// that model output never becomes a policy input still holds, because nothing
// the model emits reaches this package except as opaque payload bytes. What a
// hook is trusted to do is object. It is not trusted to widen anything: exit 0
// means only that the hook did not object, and never grants a call the
// sandbox, the guard, or the permission gate would otherwise withhold. A hook
// runs after those checks pass, never instead of them.
//
// A hook's stdout and stderr are captured and returned for the thread's event
// log and the TUI. They never reach the model, as a tool result or otherwise:
// a channel the model could read would let a hook's text steer the next turn,
// which is the coupling the policy rule above exists to prevent.
package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/tool"
)

// DefaultTimeout bounds one hook process. A hook is a command the user wrote,
// so a hung one must not hang the run.
const DefaultTimeout = 5 * time.Second

// RefuseExitCode is the exit status a pre-tool-use hook uses to refuse a call
// deliberately. Every other nonzero status refuses too, as a hook that failed
// rather than one that objected.
const RefuseExitCode = 2

// waitDelay bounds how long Wait blocks on the output pipe after the process
// is killed. A hook that spawns a background child leaves that child holding
// the pipe open, and without this the timeout would not actually bound the
// call.
const waitDelay = 500 * time.Millisecond

// maxOutputBytes caps captured hook output, since it lands in the event log.
const maxOutputBytes = 4096

// Event names which hook fired. It is carried in the payload so one script can
// serve both.
type Event string

// Events a hook payload may carry.
const (
	// EventPreToolUse fires after the permission gate allows a call and before
	// the tool runs.
	EventPreToolUse Event = "pre_tool_use"
	// EventPostToolUse fires after the tool returns, whether or not it erred.
	EventPostToolUse Event = "post_tool_use"
)

// Verdict is a pre-tool-use hook's answer.
type Verdict string

// Verdicts a pre-tool-use hook may produce.
const (
	// Allow means the hook did not object.
	Allow Verdict = "allow"
	// Refuse means the call must not run.
	Refuse Verdict = "refuse"
)

// Call describes the tool call a hook is asked about.
type Call struct {
	ThreadID string
	Tool     string
	Input    json.RawMessage
	Paths    []string
}

// Payload is the JSON one hook reads on stdin. Fields are additive: a hook
// that ignores an unknown key keeps working across versions.
type Payload struct {
	Result   *ResultPayload  `json:"result,omitempty"`
	Event    Event           `json:"event"`
	ThreadID string          `json:"thread_id,omitempty"`
	Tool     string          `json:"tool"`
	Input    json.RawMessage `json:"input"`
	Paths    []string        `json:"paths,omitempty"`
}

// ResultPayload carries the tool's result to a post-tool-use hook.
type ResultPayload struct {
	Content string        `json:"content"`
	Changes []tool.Change `json:"changes,omitempty"`
	IsError bool          `json:"is_error"`
}

// Decision is a pre-tool-use hook's outcome. Reason and Output both quote the
// hook's own text, so both are for the event log and the TUI only. The model
// is told a refused call was refused and nothing more, the same as a
// permission denial: text a hook writes is not a channel for steering the next
// turn.
type Decision struct {
	Verdict  Verdict
	Reason   string
	Output   string
	ExitCode int
}

// Observation is a post-tool-use hook's outcome. It carries no verdict: the
// tool already ran, so a post hook reports and never refuses.
type Observation struct {
	Reason   string
	Output   string
	ExitCode int
	OK       bool
}

// Runner invokes the configured hook commands. The zero value of its command
// fields means "not configured", which costs no process and no marshaling. A
// Runner is safe for concurrent use.
type Runner struct {
	dir     string
	pre     []string
	post    []string
	timeout time.Duration
}

// Option configures a Runner.
type Option func(*Runner)

// WithPreToolUse sets the pre-tool-use command as an argv, program first. It
// is executed directly, never through a shell, so nothing in it is subject to
// word splitting or expansion.
func WithPreToolUse(argv ...string) Option {
	return func(r *Runner) { r.pre = argv }
}

// WithPostToolUse sets the post-tool-use command as an argv, program first.
func WithPostToolUse(argv ...string) Option {
	return func(r *Runner) { r.post = argv }
}

// WithTimeout overrides DefaultTimeout, the bound on one hook process. A
// non-positive value restores DefaultTimeout, since an unbounded hook is not
// an option this package offers.
func WithTimeout(d time.Duration) Option {
	return func(r *Runner) {
		if d > 0 {
			r.timeout = d
		}
	}
}

// New builds a Runner that executes hooks with dir as their working
// directory. With no command configured, both methods return without starting
// a process.
func New(dir string, opts ...Option) *Runner {
	r := &Runner{dir: dir, timeout: DefaultTimeout}
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// PreToolUseConfigured reports whether a pre-tool-use command is set.
func (r *Runner) PreToolUseConfigured() bool { return len(r.pre) > 0 }

// PostToolUseConfigured reports whether a post-tool-use command is set.
func (r *Runner) PostToolUseConfigured() bool { return len(r.post) > 0 }

// PreToolUse asks the configured hook whether call may run, and fails closed:
// a timeout, a command that will not start, a signal, an unencodable payload,
// and any nonzero exit all refuse. Allowing on failure would make a hook
// something an attacker disables by breaking it, so the only path to Allow is
// a hook that ran to completion and exited 0.
//
// It returns no error because every failure is already a Refuse; treating one
// as an error would invite a caller to fall through to running the tool.
func (r *Runner) PreToolUse(ctx context.Context, call Call) Decision {
	if len(r.pre) == 0 {
		return Decision{Verdict: Allow}
	}

	payload, err := json.Marshal(Payload{
		Event:    EventPreToolUse,
		ThreadID: call.ThreadID,
		Tool:     call.Tool,
		Input:    call.Input,
		Paths:    call.Paths,
	})
	if err != nil {
		return Decision{
			Verdict: Refuse,
			Reason:  fmt.Sprintf("encoding pre-tool-use payload for %q: %v", call.Tool, err),
		}
	}

	out := r.exec(ctx, r.pre, payload)
	decision := Decision{Output: out.output, ExitCode: out.code}

	switch {
	case errors.Is(out.ctxErr, context.DeadlineExceeded):
		decision.Verdict = Refuse
		decision.Reason = fmt.Sprintf("pre-tool-use hook timed out after %s", r.timeout)
	case out.ctxErr != nil:
		decision.Verdict = Refuse
		decision.Reason = fmt.Sprintf("pre-tool-use hook did not finish: %v", out.ctxErr)
	case out.err != nil:
		decision.Verdict = Refuse
		decision.Reason = fmt.Sprintf("pre-tool-use hook could not run: %v", out.err)
	case out.code == 0:
		decision.Verdict = Allow
	case out.code == RefuseExitCode:
		decision.Verdict = Refuse
		decision.Reason = refuseReason(call.Tool, out.output)
	default:
		decision.Verdict = Refuse
		decision.Reason = fmt.Sprintf("pre-tool-use hook exited %d", out.code)
	}

	return decision
}

// PostToolUse reports the tool call and its result to the configured hook. The
// tool has already run, so nothing here can refuse: a failing or hung hook
// yields OK=false for the event log and the run continues.
func (r *Runner) PostToolUse(ctx context.Context, call Call, result tool.Result) Observation {
	if len(r.post) == 0 {
		return Observation{OK: true}
	}

	payload, err := json.Marshal(Payload{
		Event:    EventPostToolUse,
		ThreadID: call.ThreadID,
		Tool:     call.Tool,
		Input:    call.Input,
		Paths:    call.Paths,
		Result: &ResultPayload{
			Content: result.Content,
			Changes: result.Changes,
			IsError: result.IsError,
		},
	})
	if err != nil {
		return Observation{Reason: fmt.Sprintf("encoding post-tool-use payload for %q: %v", call.Tool, err)}
	}

	out := r.exec(ctx, r.post, payload)
	observation := Observation{Output: out.output, ExitCode: out.code}

	switch {
	case errors.Is(out.ctxErr, context.DeadlineExceeded):
		observation.Reason = fmt.Sprintf("post-tool-use hook timed out after %s", r.timeout)
	case out.ctxErr != nil:
		observation.Reason = fmt.Sprintf("post-tool-use hook did not finish: %v", out.ctxErr)
	case out.err != nil:
		observation.Reason = fmt.Sprintf("post-tool-use hook could not run: %v", out.err)
	case out.code != 0:
		observation.Reason = fmt.Sprintf("post-tool-use hook exited %d", out.code)
	default:
		observation.OK = true
	}

	return observation
}

// outcome is one hook process's raw result, before either method reads a
// verdict out of it.
type outcome struct {
	ctxErr error
	err    error
	output string
	code   int
}

func (r *Runner) exec(ctx context.Context, argv []string, payload []byte) outcome {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	//nolint:gosec // argv is user configuration, never model output
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = r.dir
	cmd.Stdin = bytes.NewReader(payload)
	cmd.WaitDelay = waitDelay

	combined, err := cmd.CombinedOutput()
	out := outcome{output: truncate(combined), ctxErr: ctx.Err()}

	if err == nil {
		return out
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		out.code = exitErr.ExitCode()

		return out
	}

	out.err = err
	out.code = -1

	return out
}

// refuseReason prefers the hook's own first line, so a hook explains itself
// rather than the caller reporting a bare exit code.
func refuseReason(toolName, output string) string {
	for line := range strings.SplitSeq(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}

	return fmt.Sprintf("pre-tool-use hook refused %q", toolName)
}

func truncate(b []byte) string {
	if len(b) <= maxOutputBytes {
		return string(b)
	}

	return string(b[:maxOutputBytes]) + "\n[hook output truncated]"
}
