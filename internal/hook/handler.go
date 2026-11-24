package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/decide"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/goal"
	"github.com/kyleking/wavez/internal/session"
)

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
	// Abstained is true when a check could not run or could not finish,
	// so that the run settled nothing about what that check covers.
	Abstained bool
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
		Failed: report.Failed(), Abstained: report.Abstained(), Fingerprint: report.Fingerprint(),
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
	router *decide.Engine
	// lookPath finds a program the way the shell would.
	lookPath func(name string) (string, error)
	// checks names what a stop in a project would run.
	checks func(root string) (builtins, custom []string, err error)
	// goals checks the project's goal at the end of a turn: a line for the
	// person, text for the agent, and whether the turn should be held.
	goals func(ctx context.Context, root string) (person, agent string, hold bool, err error)
	// projectDir is where the harness says the session started.
	projectDir string
	// envFile is where the harness reads variables for the session's
	// later shell commands from.
	envFile   string
	maxRounds int
	// quiet turns the SessionStart announcement off.
	quiet bool
	// unchecked ends a turn without running the stop-stage checks.
	unchecked bool
	// stopBudget is how long the gates may run at a stop, which sets how long
	// the goal check may run after them.
	stopBudget time.Duration
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
// what arrived, what was decided, and how many bytes the agent was handed.
func WithTrace(w io.Writer) Option {
	return func(h *Handler) { h.trace = w }
}

// WithRoute turns the route hint on: a submitted prompt is put to d, and a
// route other than the fallback reaches the agent as one fixed sentence.
// Without this option a UserPromptSubmit event does nothing.
func WithRoute(d *decide.Engine) Option {
	return func(h *Handler) { h.router = d }
}

// WithProjectDir tells the handler where the session started, which is what
// the harness puts in CLAUDE_PROJECT_DIR. Handle resolves the project from
// it before it looks at an event's cwd.
func WithProjectDir(dir string) Option {
	return func(h *Handler) { h.projectDir = dir }
}

// WithEnvFile names the file Claude Code gives a SessionStart hook in
// CLAUDE_ENV_FILE, whose export lines it applies to every later shell
// command of the session. The handler adds the session's id there, so that
// `wavez check` run by the agent checks what this session changed.
func WithEnvFile(path string) Option {
	return func(h *Handler) { h.envFile = path }
}

// WithStopBudget tells the handler how long the stop gates may run in all,
// so the goal check keeps inside the timeout init gave the Stop hook. A
// value that is not positive is ignored.
func WithStopBudget(budget time.Duration) Option {
	return func(h *Handler) {
		if budget > 0 {
			h.stopBudget = budget
		}
	}
}

// WithoutAnnouncement keeps a SessionStart from telling the agent that the
// gates run by themselves.
func WithoutAnnouncement() Option {
	return func(h *Handler) { h.quiet = true }
}

// WithoutStopChecks ends a turn without running the stop-stage checks. The
// session's changes are still followed, so the goal, the end-of-turn line,
// and the edit-stage checks work as they do with them.
func WithoutStopChecks() Option {
	return func(h *Handler) { h.unchecked = true }
}

// New returns a Handler that runs gates through runner.
func New(runner Runner, opts ...Option) *Handler {
	h := &Handler{
		runner: runner, maxRounds: config.DefaultMaxRounds, stopBudget: gate.DefaultStopBudget,
		lookPath: exec.LookPath, checks: gate.Checks, goals: goal.StopReport,
	}
	for _, opt := range opts {
		opt(h)
	}

	return h
}

// Handle reads one hook event from in, finds the project it belongs to,
// and handles it as HandleEvent does. An event that belongs to no project
// is not an error: it is answered with nothing, and nothing is written
// anywhere.
func (h *Handler) Handle(ctx context.Context, in io.Reader, out io.Writer) error {
	ev, err := ReadEvent(in)
	if err != nil {
		return err
	}

	root, _ := ResolveRoot(h.projectDir, ev.Cwd)

	return h.HandleEvent(ctx, ev, root, out)
}

// HandleEvent handles one event of the project at root, as ResolveRoot
// found it, and writes the hook's JSON output, if it has any, to out. An
// empty root is an event that belongs to no project, which is answered
// with nothing. A nil return is exit status 0. Any error means the event
// could not be handled and nothing was written: the caller reports it on
// stderr and exits with a status other than 0 and 2, which Claude Code
// treats as a non-blocking error, so a wavez failure never holds a session.
func (h *Handler) HandleEvent(ctx context.Context, ev Event, root string, out io.Writer) error {
	if root == "" {
		h.log(record{Event: ev.Name, Session: ev.SessionID, Note: "no project at " + ev.Cwd}, output{}, nil)

		return nil
	}

	rec := record{Event: ev.Name, Session: ev.SessionID, Root: root, StopHookActive: ev.StopHookActive}

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

func (h *Handler) dispatch(ctx context.Context, ev Event, rec *record) (output, error) {
	store := session.NewStore(rec.Root)

	switch ev.Name {
	case eventPostToolUse:
		return h.edited(ctx, store, ev, rec)
	case eventStop:
		return h.stopping(ctx, store, ev, rec)
	case eventSessionStart:
		// The baseline is taken here so that a shell command run before
		// the first edit is still measured against the untouched tree.
		if _, err := store.Update(ctx, ev.SessionID, keep); err != nil {
			return output{}, wrap("starting session", err)
		}

		// Without the variable `wavez check` falls back to the working
		// tree's changes, so a file that cannot be written costs little.
		if err := appendSessionExport(h.envFile, ev.SessionID); err != nil {
			rec.Note = err.Error()
		}

		return h.announced(ev, rec), nil
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
func (h *Handler) edited(ctx context.Context, store *session.Store, ev Event, rec *record) (output, error) {
	rec.Tool = ev.ToolName
	if !slices.Contains(strings.Split(EditTools, "|"), ev.ToolName) {
		return output{}, nil
	}

	rel, ok := relative(rec.Root, ev.Cwd, ev.ToolInput.path())
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
	Event          string   `json:"event"`
	Session        string   `json:"session"`
	Root           string   `json:"root,omitempty"`
	Tool           string   `json:"tool,omitempty"`
	Outcome        string   `json:"outcome"`
	Note           string   `json:"note,omitempty"`
	Error          string   `json:"error,omitempty"`
	Goal           string   `json:"goal,omitempty"`
	Review         string   `json:"review,omitempty"`
	Changed        []string `json:"changed,omitempty"`
	AgentBytes     int      `json:"agent_bytes,omitempty"`
	StopHookActive bool     `json:"stop_hook_active,omitempty"`
}

func (h *Handler) log(rec record, res output, err error) {
	if h.trace == nil {
		return
	}

	rec.Outcome, rec.AgentBytes = res.outcome(), len(res.agentText())
	if err != nil {
		rec.Outcome, rec.Error, rec.AgentBytes = outcomeError, err.Error(), 0
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
