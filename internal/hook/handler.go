package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"

	"github.com/kyleking/wavez/internal/decide"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/session"
)

// DefaultMaxRounds is how many stops in a row may fail the same way before
// the handler lets the agent stop.
const DefaultMaxRounds = 3

// chainFactor bounds one turn: however the failures vary, the handler
// blocks at most chainFactor times the round cap between two stops the
// agent made on its own.
const chainFactor = 4

// Verdict is what a gate run concluded, reduced to what a hook acts on.
type Verdict struct {
	// Fingerprint identifies the blocking failures, so a repeat can be
	// told from a new failure.
	Fingerprint string
	// Text is what the agent should read, and is empty when there is
	// nothing to say.
	Text string
	// Notes is for the person and never reaches the agent: the checks
	// that could not run.
	Notes string
	// Failed is true when a blocking check failed.
	Failed bool
}

// Runner runs the checks a set of changed files selects.
type Runner interface {
	Run(ctx context.Context, root string, changed []string, stage gate.Stage) (Verdict, error)
}

// Gate adapts the gate package's runner to the one the handler takes.
type Gate struct {
	Runner gate.Runner
}

// Run runs the gates and reduces their report to a Verdict.
func (g Gate) Run(ctx context.Context, root string, changed []string, stage gate.Stage) (Verdict, error) {
	report, err := g.Runner.Run(ctx, root, changed, stage)
	if err != nil {
		return Verdict{}, fmt.Errorf("running %s gates: %w", stage, err)
	}

	return Verdict{
		Failed: report.Failed(), Fingerprint: report.Fingerprint(),
		Text: report.Render(), Notes: report.Notes(),
	}, nil
}

// Handler turns one Claude Code hook event into a gate run and the output
// that tells the harness what to do with the result.
type Handler struct {
	runner Runner
	trace  io.Writer
	// router answers the route question, and is nil while the project has
	// the hint turned off.
	router decide.Decider
	// lookPath finds a program the way the shell would.
	lookPath  func(name string) (string, error)
	maxRounds int
}

// Option configures a Handler.
type Option func(*Handler)

// WithMaxRounds sets how many consecutive stops may fail with the same
// fingerprint; the last of them is let through. Values below 1 are ignored.
func WithMaxRounds(rounds int) Option {
	return func(h *Handler) {
		if rounds >= 1 {
			h.maxRounds = rounds
		}
	}
}

// WithTrace makes the handler append one JSON line per event to w, saying
// what arrived and what was decided.
func WithTrace(w io.Writer) Option {
	return func(h *Handler) { h.trace = w }
}

// WithRoute turns the route hint on: a submitted prompt is put to d, and a
// route other than the fallback reaches the agent as one fixed sentence.
// Without this option a UserPromptSubmit event does nothing.
func WithRoute(d decide.Decider) Option {
	return func(h *Handler) { h.router = d }
}

// WithLookPath replaces how the handler finds gopls, which the command a
// rename denial suggests cannot run without.
func WithLookPath(find func(name string) (string, error)) Option {
	return func(h *Handler) {
		if find != nil {
			h.lookPath = find
		}
	}
}

// New returns a Handler that runs gates through runner.
func New(runner Runner, opts ...Option) *Handler {
	h := &Handler{runner: runner, maxRounds: DefaultMaxRounds, lookPath: exec.LookPath}
	for _, opt := range opts {
		opt(h)
	}

	return h
}

// Handle reads one hook event from in and writes the hook's JSON output, if
// it has any, to out. A nil return is exit status 0. Any error means the
// event could not be handled and nothing was written: the caller reports it
// on stderr and exits with a status other than 0 and 2, which Claude Code
// treats as a non-blocking error, so a wavez failure never holds a session.
func (h *Handler) Handle(ctx context.Context, in io.Reader, out io.Writer) error {
	ev, err := readEvent(in)
	if err != nil {
		return err
	}

	if ev.Cwd == "" {
		return fmt.Errorf("%w: no cwd", ErrMalformedEvent)
	}

	rec := record{Event: ev.Name, Session: ev.SessionID, Root: FindRoot(ev.Cwd), StopHookActive: ev.StopHookActive}

	res, err := h.dispatch(ctx, ev, &rec)
	h.log(rec, res, err)

	if err != nil {
		return err
	}

	if res.empty() {
		return nil
	}

	return res.write(out)
}

func (h *Handler) dispatch(ctx context.Context, ev event, rec *record) (output, error) {
	store := session.NewStore(rec.Root)

	switch ev.Name {
	case eventPostToolUse:
		return h.edited(ctx, store, ev, rec)
	case eventStop:
		return h.stopping(ctx, store, ev, rec)
	case eventSessionStart:
		// The baseline is taken here so that a shell command run before
		// the first edit is still measured against the untouched tree.
		_, err := store.Update(ctx, ev.SessionID, keep)

		return output{}, wrap("starting session", err)
	case eventUserPromptSubmit:
		return h.prompted(ctx, ev, rec), nil
	case eventPreToolUse:
		return h.running(ctx, ev, rec), nil
	default:
		return output{}, nil
	}
}

func keep(*session.State) error { return nil }

// edited records the file an edit tool wrote and runs the edit-stage checks
// on it. Whatever they find is context for the model and never a block.
func (h *Handler) edited(ctx context.Context, store *session.Store, ev event, rec *record) (output, error) {
	rec.Tool = ev.ToolName
	if !slices.Contains(strings.Split(EditTools, "|"), ev.ToolName) {
		return output{}, nil
	}

	rel, ok := relative(rec.Root, ev.ToolInput.path())
	if ev.ToolInput.path() == "" || !ok {
		_, err := store.Update(ctx, ev.SessionID, keep)

		return output{}, wrap("recording edit", err)
	}

	rec.Changed = []string{rel}

	_, err := store.Update(ctx, ev.SessionID, func(state *session.State) error {
		state.Record(rel)

		return nil
	})
	if err != nil {
		return output{}, wrap("recording edit", err)
	}

	verdict, err := h.runner.Run(ctx, rec.Root, []string{rel}, gate.StageEdit)
	if err != nil {
		return output{}, wrap("edit gates", err)
	}

	if verdict.Text == "" {
		return output{}, nil
	}

	return contextFor(eventPostToolUse, verdict.Text), nil
}

func wrap(doing string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%s: %w", doing, err)
}

// record is one line of the trace.
type record struct {
	Event   string `json:"event"`
	Session string `json:"session"`
	Root    string `json:"root"`
	Tool    string `json:"tool,omitempty"`
	// Outcome is what the handler did: silent, context, block, deny,
	// released, or error.
	Outcome        string   `json:"outcome"`
	Note           string   `json:"note,omitempty"`
	Error          string   `json:"error,omitempty"`
	Changed        []string `json:"changed,omitempty"`
	StopHookActive bool     `json:"stop_hook_active,omitempty"`
}

func (h *Handler) log(rec record, res output, err error) {
	if h.trace == nil {
		return
	}

	rec.Outcome = res.outcome()
	if err != nil {
		rec.Outcome, rec.Error = "error", err.Error()
	}

	line, marshalErr := json.Marshal(rec)
	if marshalErr != nil {
		return
	}

	// The trace is a debugging aid: when it cannot be written the hook
	// carries on without it.
	if _, err := h.trace.Write(append(line, '\n')); err != nil {
		h.trace = nil
	}
}
