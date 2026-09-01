// Package agent drives the streaming tool-use loop: it builds a request from
// a stable prefix and a thread's history, streams the model's response,
// executes tool calls, and repeats until the model ends its turn or a bound
// trips.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/condition"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
)

const (
	// DefaultMaxTurns bounds model calls within one Run as a dead-man's
	// switch: the deadline, cost ceiling, and stagnation bounds are meant to
	// trip first, so this only catches a bug in one of those. Set well above
	// any legitimate run.
	DefaultMaxTurns = 200
	// DefaultMaxToolCallsPerTurn bounds tool calls within a single model
	// turn, not the whole run: a model flooding one turn with hundreds of
	// calls is the one runaway pattern wall-clock cannot catch, since no
	// decode happens between them.
	DefaultMaxToolCallsPerTurn = 50
	// DefaultMaxVerifyRounds is the default bound on verification rounds
	// once the model reports it is done.
	DefaultMaxVerifyRounds = 2
	// DefaultMaxWallClock bounds one Run's total wall-clock time. Across the
	// replay corpus, runs that passed every check took 45s to 501s, so 180s
	// cut off the slowest fifth of the work that would have succeeded and
	// recorded it as a deadline. The spend ceiling is the bound that should
	// bind, since it is the one that measures what a run actually costs.
	DefaultMaxWallClock = 600 * time.Second
	// DefaultMaxHostedSpendUSD bounds accumulated hosted-tier spend for one
	// Run. It is a runaway bound rather than a budget: the most expensive
	// run on record finished every check for $0.58, and the ceiling exists
	// so a loop that cannot stop itself costs a dollar rather than whatever
	// the deadline allows.
	DefaultMaxHostedSpendUSD = 1.00
	// DefaultMaxStagnantErrors bounds consecutive tool-call results that
	// return an error, regardless of whether their inputs matched: the
	// near-miss loop (three different wrong str_replace anchors in a row)
	// the exact-repeat detector cannot see.
	DefaultMaxStagnantErrors = 3
	// DefaultTurnsBeforeNudge is how many turns a run may spend without
	// changing a file before it is told to start. Two dogfood runs spent
	// their whole budget reading, one for 24 turns and one for 60, and
	// neither left a file changed: the second spent 44 shell calls mapping
	// every consumer of a struct field it was adding a key beside. It is a
	// nudge rather than a bound, because a task can genuinely need reading
	// first, and it repeats at most maxNudges times.
	DefaultTurnsBeforeNudge = 15
	// A third telling is evidence the model is not going to start.
	maxNudges = 2

	million = 1_000_000.0
)

// toolStrReplace is the edit tool named in more than one rule here.
const (
	toolStrReplace = "str_replace"
	toolWrite      = "write"
)

// editToolNames are the tools that leave a tool.Change on success.
var editToolNames = map[string]struct{}{
	"delete":       {},
	"rename":       {},
	toolStrReplace: {},
	toolWrite:      {},
}

// ModelPricing prices one model's hosted usage in dollars per million
// tokens. A cached prompt token is billed at CacheReadPerMillion, and a
// zero there means the provider bills it at the full input rate.
type ModelPricing struct {
	InputPerMillion     float64
	OutputPerMillion    float64
	CacheReadPerMillion float64
}

// DefaultPricing prices the models DESIGN.md's model-routing decision
// names, per each provider's published per-token rates. A model with no
// entry here prices at zero, so the cost ceiling never trips for a model
// wavez has no real price for.
//
// The GLM tiers run on a flat coding plan, so what they report is a shadow
// price rather than a bill: it is what the same tokens would cost at z.ai's
// pay-per-use rates, which is what keeps a run comparable to the corpus
// recorded before the plan.
//
//nolint:mnd // published per-million-token prices, not magic numbers
var DefaultPricing = map[string]ModelPricing{
	// Free at every rate, entered rather than omitted so the zero reads as
	// measured instead of missing.
	"glm-4.7-flash":                     {},
	"glm-5.3":                           {InputPerMillion: 1.40, OutputPerMillion: 4.40, CacheReadPerMillion: 0.26},
	"moonshotai/kimi-k2.7-code":         {InputPerMillion: 0.67, OutputPerMillion: 3.40, CacheReadPerMillion: 0.19},
	"openai/gpt-5-mini":                 {InputPerMillion: 0.25, OutputPerMillion: 2.00, CacheReadPerMillion: 0.025},
	"qwen/qwen3-8b":                     {InputPerMillion: 0.117, OutputPerMillion: 0.455},
	"qwen/qwen3-coder":                  {InputPerMillion: 0.30, OutputPerMillion: 1.00, CacheReadPerMillion: 0.10},
	"qwen/qwen3-coder-30b-a3b-instruct": {InputPerMillion: 0.07, OutputPerMillion: 0.28},
}

// Stop names why Run returned.
type Stop string

// Reasons Run stops.
const (
	// StopComplete means the model ended its turn with no pending tool call.
	StopComplete Stop = "complete"
	// StopMaxTurns means Run reached its configured turn bound, a
	// dead-man's switch that should only trip on a bug in another bound.
	StopMaxTurns Stop = "max_turns"
	// StopToolCallFlood means a single model turn emitted more tool calls
	// than the configured per-turn bound.
	StopToolCallFlood Stop = "tool_call_flood"
	// StopMalformedTool means a tool call's input was not valid JSON.
	StopMalformedTool Stop = "malformed_tool_call"
	// StopLoopDetected means a tool call repeated the immediately preceding
	// call's name and input.
	StopLoopDetected Stop = "loop_detected"
	// StopStagnant means a configured number of consecutive tool-call
	// results returned an error, regardless of whether their inputs matched,
	// or the run reached its end having called an edit tool but left no
	// file changed.
	StopStagnant Stop = "stagnant"
	// StopDeadline means Run reached the absolute wall-clock deadline
	// computed once at entry.
	StopDeadline Stop = "deadline"
	// StopCostCeiling means Run's accumulated hosted-tier spend crossed the
	// configured dollar ceiling.
	StopCostCeiling Stop = "cost_ceiling"
	// StopCanceled means ctx was done before Run could reach another stop.
	StopCanceled Stop = "canceled"
	// StopFailed means the routed provider's stream failed for a reason
	// other than ctx cancellation, with no further tier to escalate to.
	StopFailed Stop = "provider_failed"
	// StopVerifyFailed means the model's changes still failed verification
	// after MaxVerifyRounds rounds.
	StopVerifyFailed Stop = "verify_failed"
	// StopAskedInProse means the model ended a turn offering to do the work
	// rather than doing it, twice, having been told once to act instead.
	StopAskedInProse Stop = "asked_in_prose"
	// StopAnnouncedNotDone means the model ended a turn announcing its next
	// step and taking none, twice, having been told once to act instead.
	StopAnnouncedNotDone Stop = "announced_not_done"
	// StopTreeState means verification failed on something the run cannot
	// have caused, so the run stops for the scheduler rather than handing
	// the model a failure it can only guess at. The run's changes stand.
	StopTreeState Stop = "tree_state"
)

// ErrEmptyCompletion reports a provider that returned neither prose nor a
// tool call. It is raised rather than absorbed as an outcome: an empty
// completion is the provider rejecting the request shape, and reporting it
// as a bound hides a broken tier behind a plausible story about the model.
var ErrEmptyCompletion = errors.New("the model returned no text and no tool call")

// ErrScriptedFailure is a sentinel a test provider may wrap to model a
// non-cancellation stream failure distinct from ctx.Err().
var ErrScriptedFailure = errors.New("agent: provider stream failed")

// errNoCheckpointer is returned by RestoreCheckpoint when the Loop was
// built with no Checkpointer configured.
var errNoCheckpointer = errors.New("agent: no Checkpointer configured for this Loop")

// Prefix holds the parts of an llm.Request that stay byte-identical across
// every turn of one Run: the system prompt (including the session ledger,
// which Run folds in once at the start) and the tool set. Only Messages grows
// turn over turn.
type Prefix struct {
	System string
	Ledger string
	Tools  []llm.ToolSpec
	// FastTools is the narrower surface the fast tier is shown, empty to
	// show it Tools like every other tier.
	//
	// It exists because the same preamble is not the same cost: it is 33%
	// of what a fast turn can use against 1.8% of a hosted one, an 18x
	// difference for identical bytes. The tiers are served by different
	// processes and so keep separate prefix caches, which is what makes
	// this free where narrowing the surface mid-thread would not be.
	//
	// The registry is not narrowed with it. A tool left out here is one
	// the fast tier is not told about, not one it is refused; plan mode
	// narrows the registry instead, because that is a permission and this
	// is a budget.
	FastTools []llm.ToolSpec
}

// Outcome reports how Run ended, carrying the measured numbers a bound
// report needs rather than just a turn count: elapsed wall time, tokens,
// hosted spend, and, for a stagnation stop, which tool failed and how many
// times in a row.
type Outcome struct {
	// Review is the last verdict a configured Reviewer returned, zero when
	// none ran. An objection here does not make the run a failure: the run
	// completed and the objection stands unresolved for the user to settle.
	Review Verdict
	// Reason says why the run stopped, in the words a bound report needs.
	// Condition pairs it with Stop as the Verdict shape a Cycle phase's exit
	// gate returns one granularity up.
	Reason       string
	Checkpoint   string
	StagnantTool string
	Stop         Stop
	// Edits holds one operation id per accepted change, in the order they
	// landed, so undo reaches a single edit rather than only the whole run.
	Edits []EditPoint
	// FinishFindings is what the deterministic finish checks found on a run
	// that otherwise completed. It is a bound rather than a judgment: an
	// empty slice says the run did something of the right shape, never that
	// it is correct.
	FinishFindings []string
	Turns          int
	ToolCalls      int
	InputTokens    int
	OutputTokens   int
	// TokensCompacted is the estimated saving deterministic compaction made
	// across the run, zero when compaction never ran.
	TokensCompacted int
	Elapsed         time.Duration
	HostedSpendUSD  float64
	// ThreadSpendUSD is what every run on this thread has cost, including
	// this one. HostedSpendUSD is what the ceiling bounds, since the
	// ceiling is a runaway guard on one unattended run; this is what says
	// whether resuming the thread is still worth it.
	ThreadSpendUSD float64
	StagnantCount  int
	// RecoveredCalls counts the tool calls read back out of prose because
	// the provider did not parse the model's own serialization. It is
	// recorded because a tier that needs it is a tier with a templating
	// problem, and that should be visible rather than silently absorbed.
	RecoveredCalls int
	// GateFalseAlarms counts the gates that retracted a failure over an
	// unchanged change set during this run, which is a harness defect
	// presenting as a code defect.
	GateFalseAlarms int
	// GatesPassedAtEnd reports whether the gates passed on the change set of
	// a run that stopped on a bound, and is false when no such check ran. A
	// run that hit a bound having left the tree building and its tests
	// passing is a different outcome from one that left it broken, and both
	// stop as failures otherwise: measured on `h6`, a run stopped
	// loop_detected with every gate passing on what it had written.
	GatesPassedAtEnd bool
}

// Condition reports the stop condition that held as the Verdict a Cycle
// phase's exit gate returns: a Loop's stop reasons and a phase's exit gate
// are the same idea at two granularities.
func (o Outcome) Condition() condition.Verdict {
	return condition.Met(string(o.Stop), o.Reason)
}

// LocalSlots admits one request to the on-box model and gives the admission
// back when the request is done. It is defined here because the loop is the
// only place that knows, per turn, which tier a request goes to: a run
// pinned to the fast tier escalates mid-run, and one that held the slot
// across its hosted turns would starve every other local thread for work it
// was not doing.
type LocalSlots interface {
	AdmitSlot(ctx context.Context, holder string) (func(), error)
}

// GateVerdict is what one verification round concluded about a run's own work.
type GateVerdict string

// Verdicts a Verifier may return.
const (
	// VerdictPass means every gate passed.
	VerdictPass GateVerdict = "pass"
	// VerdictFailed means a gate failed on work this run could have caused,
	// so the feedback is the model's to act on.
	VerdictFailed GateVerdict = "failed"
	// VerdictUnattributed means a gate failed and nothing it printed names a
	// file this run changed or a package a changed file reaches. The tree is
	// broken by something other than this run, and a model handed the
	// failure spends turns on work it cannot have caused: one dogfood lane
	// abandoned a correct change set over a neighbor's failing test, and a
	// second spent every turn it had on a third lane's lint. It goes to the
	// scheduler instead, which is the only party that can see the other
	// writer.
	VerdictUnattributed GateVerdict = "unattributed"
)

// Verifier gates a run once the model reports it is done, per DESIGN.md's
// decision to verify once on the final turn rather than on every turn.
// Verify reports VerdictPass when changes accumulated across the run pass,
// and otherwise returns feedback already trimmed to what the model may see
// (the gate.Result.ForModel / gate.TrimFailure asymmetry).
type Verifier interface {
	Verify(ctx context.Context, changes []tool.Change) (feedback string, verdict GateVerdict)
}

// EditPoint is one accepted change and the jj operation holding the tree as
// it stood just after it. Because jj snapshots the working copy on every
// command, capturing an operation id after an edit records that edit's tree
// without committing anything: measured on this repo, restoring to a
// captured id brings back the exact file content, at 40-70 ms per capture.
type EditPoint struct {
	// Repo is the repository root the operation belongs to, which is where a
	// restore must run. An edit reached through a declared extra directory
	// belongs to a different repository than the project root.
	Repo  string
	Op    string
	Paths []string
}

// Checkpointer captures and restores a jj checkpoint around a run, per
// DESIGN.md's VCS decision to take checkpointing from jj's operation log
// instead of writing own snapshots. Capture must be cheap enough to call
// before every run, since jj snapshots the working copy as a side effect
// of any command. Restore must be safe to call when nothing changed since
// the checkpoint it is given.
type Checkpointer interface {
	// RepoRoot names the repository root holding path, which is where a
	// checkpoint captured after an edit there belongs. An error names a path
	// under no repository, which records no checkpoint rather than failing
	// the edit.
	RepoRoot(ctx context.Context, path string) (string, error)
	Capture(ctx context.Context, repoRoot string) (string, error)
	Restore(ctx context.Context, repoRoot, checkpoint string) error
}

// Options bounds and configures a Loop.
type Options struct {
	Verifier     Verifier
	Reviewer     Reviewer
	Finisher     Finisher
	LocalSlots   LocalSlots
	Checkpointer Checkpointer
	Clock        gate.Clock
	Hooks        Hooks
	ChangeGate   ChangeGate
	Pricing      map[string]ModelPricing
	// Models is the model name sent in a request routed to each tier.
	Models router.Tiers[string]
	// Thinking is each tier's reasoning default, applied to a turn whose
	// thread pinned none. Nil leaves the served model's own default.
	Thinking router.Tiers[*bool]
	RepoRoot string
	Compact  thread.CompactOptions
	// ContextWindow is the fast tier's served context in tokens, which
	// bounds what the router sends there and when compaction starts. Zero
	// means router.FastContextBudget.
	ContextWindow       int
	MaxTurns            int
	MaxToolCallsPerTurn int
	MaxVerifyRounds     int
	MaxReviewRounds     int
	MaxStagnantErrors   int
	TurnsBeforeNudge    int
	MaxWallClock        time.Duration
	MaxHostedSpendUSD   float64
	CompactTrigger      float64
	// FastPresencePenalty and FastRepeatPenalty bound repetition on the fast
	// tier only, where every degenerate emission this project has recorded
	// happened. Zero and zero leave llama.cpp's own defaults, which disable
	// both.
	FastPresencePenalty float64
	FastRepeatPenalty   float64
	CompactEnabled      bool
	// GateWrites asks the permission gate about write_local tool calls,
	// keyed per file. It is off by default: the measurement says 5.7 write
	// calls a run against 2.0 distinct files, so the write class is two
	// prompts a run, and enabling it is a separate decision from declaring
	// the class.
	GateWrites bool
}

// samplingFor is the repetition bound for one turn, which is zero off the
// fast tier: no hosted turn in this project's thread logs has degenerated,
// and a hosted model prices and samples on its own terms.
func (o Options) samplingFor(route router.Decision) sampling {
	if route.Choice != router.ChoiceFast {
		return sampling{}
	}

	return sampling{presence: o.FastPresencePenalty, repeat: o.FastRepeatPenalty}
}

// sampling is one turn's repetition bound.
type sampling struct {
	presence float64
	repeat   float64
}

// Option configures a Loop.
type Option func(*Options)

// WithLocalSlots makes every turn routed to the local tier take a slot on
// the on-box model for the length of its request, and give it back after.
func WithLocalSlots(s LocalSlots) Option { return func(o *Options) { o.LocalSlots = s } }

// WithGatedWrites asks the permission gate about write_local tool calls,
// keyed per file. Off by default; see Options.GateWrites.
func WithGatedWrites() Option { return func(o *Options) { o.GateWrites = true } }

// WithMaxTurns overrides DefaultMaxTurns.
func WithMaxTurns(n int) Option { return func(o *Options) { o.MaxTurns = n } }

// WithMaxToolCallsPerTurn overrides DefaultMaxToolCallsPerTurn. It bounds
// how many tool calls a single model turn may emit, not the whole run.
func WithMaxToolCallsPerTurn(n int) Option { return func(o *Options) { o.MaxToolCallsPerTurn = n } }

// WithMaxWallClock overrides DefaultMaxWallClock. Run computes an absolute
// deadline once from this duration at entry and never re-derives it per
// turn. Zero disables the bound.
func WithMaxWallClock(d time.Duration) Option { return func(o *Options) { o.MaxWallClock = d } }

// WithMaxHostedSpendUSD overrides DefaultMaxHostedSpendUSD, the dollar
// ceiling on one run's accumulated hosted-tier spend. Local turns never
// accrue cost, so this only bites once a turn escalates. Zero disables it.
func WithMaxHostedSpendUSD(v float64) Option { return func(o *Options) { o.MaxHostedSpendUSD = v } }

// WithFastSampling bounds repetition on fast-tier turns. A
// grammar-constrained tool argument cannot stop early the way free text
// can, so a model that starts repeating inside one has no exit and runs to
// the context limit.
func WithFastSampling(presence, repeat float64) Option {
	return func(o *Options) { o.FastPresencePenalty, o.FastRepeatPenalty = presence, repeat }
}

// WithMaxStagnantErrors overrides DefaultMaxStagnantErrors: the number of
// consecutive tool-call results that must return an error, regardless of
// whether their inputs matched the previous call, before Run stops. Any
// successful tool call resets the count. Zero disables it. This trigger is
// independent of the exact-repeat detection Run always runs.
func WithMaxStagnantErrors(n int) Option { return func(o *Options) { o.MaxStagnantErrors = n } }

// WithTurnsBeforeNudge overrides DefaultTurnsBeforeNudge: how many turns a
// run may go without changing a file before it is told to make its first
// edit. Zero disables the nudge.
func WithTurnsBeforeNudge(n int) Option { return func(o *Options) { o.TurnsBeforeNudge = n } }

// WithPricing replaces the per-model dollar-per-million-token table Run
// prices hosted usage against, in full. Start from DefaultPricing to extend
// it rather than lose the entry it carries.
func WithPricing(pricing map[string]ModelPricing) Option {
	return func(o *Options) { o.Pricing = pricing }
}

// WithClock overrides the Clock Run reads for its deadline and
// Outcome.Elapsed. Tests inject a fake Clock to advance time without
// sleeping; production leaves the default, gate.RealClock.
func WithClock(c gate.Clock) Option { return func(o *Options) { o.Clock = c } }

// WithModels sets the model name sent in a request routed to each tier.
func WithModels(m router.Tiers[string]) Option { return func(o *Options) { o.Models = m } }

// WithThinking sets each tier's reasoning default for turns whose thread
// pinned none.
func WithThinking(t router.Tiers[*bool]) Option { return func(o *Options) { o.Thinking = t } }

// WithContextWindow sets the fast tier's served context in tokens.
func WithContextWindow(n int) Option { return func(o *Options) { o.ContextWindow = n } }

// ChangeGate receives every file change a tool makes and reports what the
// gates it triggered found. It is declared here because the loop is what
// consumes it: gates fire on change events rather than on the model
// deciding to test, and their findings reach the model on its next turn.
type ChangeGate interface {
	// Begin forgets writer's previous run, so what the harness reports
	// about this one describes this one. One gate serves every thread, so
	// the writer is what keeps a lane still working from being forgotten by
	// a lane starting beside it.
	Begin(writer string)
	Enqueue(c tool.Change)
	TakeFeedback() (string, bool)
	// FalseAlarms returns the gates that have passed over the same change
	// set they just failed over, and clears them. A retraction is the
	// harness reporting a defect that was never in the code, so it is
	// recorded rather than quietly dropped.
	FalseAlarms() []string
	// Stuck names a gate this run has failed identically several times over,
	// each after further edits, or reports false. It is the run's own
	// history read as a routing signal: the tier has been told what is
	// wrong and cannot move it.
	Stuck() (string, bool)
}

// WithChangeGate configures Run to feed every change into a debounced gate
// runner and to hand the model whatever those gates found before its next
// turn. Without one, gates run only at the end of a run.
func WithChangeGate(g ChangeGate) Option {
	return func(o *Options) { o.ChangeGate = g }
}

// WithVerifier configures Run to gate once the model reports it is done,
// feeding a failing verification back as a new turn instead of trusting
// the model's own claim of completion. With no verifier configured, Run's
// behavior on model completion is unchanged.
func WithVerifier(v Verifier) Option { return func(o *Options) { o.Verifier = v } }

// WithMaxVerifyRounds overrides DefaultMaxVerifyRounds.
func WithMaxVerifyRounds(n int) Option { return func(o *Options) { o.MaxVerifyRounds = n } }

// WithCheckpointer configures Run to capture a checkpoint at the start of
// every run and record it on Outcome and on any failure event so a caller
// can restore repoRoot to it. Run itself never restores: DESIGN.md defaults
// to reporting the restore point rather than performing it, since
// destroying uncommitted work without asking is worse than leaving it. Run
// fails outright if Capture errors, since a checkpoint that cannot be taken
// is not a checkpoint that succeeded.
func WithCheckpointer(c Checkpointer, repoRoot string) Option {
	return func(o *Options) { o.Checkpointer, o.RepoRoot = c, repoRoot }
}

// Loop drives one thread's tool-use turns against one llm.Provider per
// routing tier, chosen per turn by internal/router.
type Loop struct {
	providers router.Tiers[llm.Provider]
	tools     *tool.Registry
	gate      permission.Gate
	options   Options
}

// FastModel reports the model name the router serves a fast turn with.
func (l *Loop) FastModel() string { return l.options.Models.Fast }

// ContextWindow reports the fast tier's served context in tokens.
func (l *Loop) ContextWindow() int {
	if l.options.ContextWindow <= 0 {
		return router.FastContextBudget
	}

	return l.options.ContextWindow
}

// New builds a Loop. PermGate is consulted for any tool call whose Tool
// implements PermissionRequester and reports the call needs approval.
func New(
	providers router.Tiers[llm.Provider], tools *tool.Registry, permGate permission.Gate, opts ...Option,
) *Loop {
	options := Options{
		MaxTurns:            DefaultMaxTurns,
		MaxToolCallsPerTurn: DefaultMaxToolCallsPerTurn,
		MaxVerifyRounds:     DefaultMaxVerifyRounds,
		MaxReviewRounds:     DefaultMaxReviewRounds,
		MaxWallClock:        DefaultMaxWallClock,
		MaxHostedSpendUSD:   DefaultMaxHostedSpendUSD,
		MaxStagnantErrors:   DefaultMaxStagnantErrors,
		TurnsBeforeNudge:    DefaultTurnsBeforeNudge,
		CompactTrigger:      DefaultCompactTrigger,
		Clock:               gate.RealClock{},
		Pricing:             DefaultPricing,
	}
	for _, opt := range opts {
		opt(&options)
	}

	return &Loop{providers: providers, tools: tools, gate: permGate, options: options}
}

// Run appends prompt to th as a user turn, then drives model turns until the
// model ends its turn with no pending tool call or a bound trips. Hint seeds
// per-turn routing (Override and Thinking); Run fills in EstimatedTokens and
// moves the run one tier up per failure, since a failing tier is never
// retried on itself.
//
// Run returns a non-nil error only for a failure Outcome cannot describe:
// ctx cancellation, a thread I/O error, or a routed provider's stream
// failing with no further tier to escalate to. A tripped bound (malformed
// tool call, repeated call, turn or tool-call cap) is reported through
// Outcome and a KindError event on th.Log, not through the error return.
func (l *Loop) Run(
	ctx context.Context, th *thread.Thread, prefix Prefix, prompt string, hint router.Input,
) (Outcome, error) {
	system := prefix.System
	if prefix.Ledger != "" {
		system += "\n\n## Session ledger\n" + prefix.Ledger
	}

	if goal := standingGoal(th); goal != "" {
		system += "\n\n## Goal\n" + goal
	}

	if l.options.ChangeGate != nil {
		l.options.ChangeGate.Begin(string(th.ID()))
	}

	checkpoint, err := l.captureCheckpoint(ctx)
	if err != nil {
		return startupOutcome(ctx, ""), err
	}

	if err := th.AppendUser(ctx, prompt); err != nil {
		return startupOutcome(ctx, checkpoint), fmt.Errorf("appending prompt: %w", err)
	}
	if err := th.SetState(ctx, event.StateWorking); err != nil {
		return startupOutcome(ctx, checkpoint), fmt.Errorf("setting state: %w", err)
	}

	start := l.options.Clock.Now()

	var deadline time.Time
	if l.options.MaxWallClock > 0 {
		deadline = start.Add(l.options.MaxWallClock)
	}

	r := &run{
		loop: l, thread: th, system: system, tools: prefix.Tools, fastTools: prefix.FastTools,
		hint: hint, gk: newGateKeeper(l.gate, l.options.GateWrites),
		task: prompt, startTime: start, deadline: deadline,
		priorSpend: l.priorSpend(th),
	}
	r.outcome.Checkpoint = checkpoint
	r.outcome.ThreadSpendUSD = r.priorSpend

	return r.drive(ctx)
}

// startupOutcome carries what a failure before the first turn can still say.
// A cancellation that lands during startup is a cancellation, and a caller
// reading Stop to decide a thread's state must not read it as an ordinary
// failure.
func startupOutcome(ctx context.Context, checkpoint string) Outcome {
	out := Outcome{Checkpoint: checkpoint}
	if ctx.Err() != nil {
		out.Stop = StopCanceled
	}

	return out
}

// standingGoal is the thread's goal when the history about to be sent does
// not already carry it, and empty otherwise. A goal restated on every turn
// is the stronger form against drift and costs tokens on every turn of
// every thread; this covers the two points that lose it outright instead,
// which are a fork (it inherits no transcript by design) and a rewrite (the
// new wording was never in the history at all).
func standingGoal(th *thread.Thread) string {
	goal := th.Goal()
	if goal == "" {
		return ""
	}

	for _, msg := range th.History() {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, goal) {
			return ""
		}
	}

	return goal
}

// captureCheckpoint takes the checkpoint Run records on Outcome, when a
// Checkpointer is configured. It returns a wrapped error rather than an
// empty checkpoint on failure, so a run never proceeds believing it has a
// restore point it does not.
func (l *Loop) captureCheckpoint(ctx context.Context) (string, error) {
	if l.options.Checkpointer == nil {
		return "", nil
	}

	checkpoint, err := l.options.Checkpointer.Capture(ctx, l.options.RepoRoot)
	if err != nil {
		return "", fmt.Errorf("capturing checkpoint: %w", err)
	}

	return checkpoint, nil
}

// priorSpend prices the assistant turns already in th's log, so a resumed
// run reports what the thread has cost rather than what this run added. It
// returns zero rather than an error on an unreadable log: a report that
// cannot be built is not a reason to refuse the run.
func (l *Loop) priorSpend(th *thread.Thread) float64 {
	evs, err := th.Log().Since(0)
	if err != nil {
		return 0
	}

	total := 0.0

	for i := range evs {
		if evs[i].Kind != event.KindAgent || evs[i].Role == "" {
			continue
		}

		raw, ok := evs[i].Detail["usage"].(map[string]any)
		if !ok {
			continue
		}

		model, ok := evs[i].Detail["model"].(string)
		if !ok {
			continue
		}

		total += l.PriceTurn(model, llm.Usage{
			InputTokens:     loggedInt(raw, "input_tokens"),
			OutputTokens:    loggedInt(raw, "output_tokens"),
			CacheReadTokens: loggedInt(raw, "cache_read_tokens"),
		})
	}

	return total
}

// loggedInt reads a count back out of a log detail, where every number
// decoded as a float64.
func loggedInt(detail map[string]any, key string) int {
	v, ok := detail[key].(float64)
	if !ok {
		return 0
	}

	return int(v)
}

// PriceTurn prices one turn's usage against the configured per-model table.
// A model with no pricing entry contributes zero cost, so the cost ceiling
// never trips on a model wavez has no real price for. A caller pricing a turn
// off its own log event gets the same number the run accumulates.
//
// InputTokens counts the whole prompt including the part the provider served
// from its cache, so billing all of it at the input rate charges 3.5x what a
// cached token costs on the deep tier, where 90% of every turn's prompt is a
// cache hit.
func (l *Loop) PriceTurn(model string, usage llm.Usage) float64 {
	p, ok := l.options.Pricing[model]
	if !ok {
		return 0
	}

	cached := min(usage.CacheReadTokens, usage.InputTokens)
	if p.CacheReadPerMillion == 0 {
		cached = 0
	}

	fresh := usage.InputTokens - cached

	return float64(fresh)/million*p.InputPerMillion +
		float64(cached)/million*p.CacheReadPerMillion +
		float64(usage.OutputTokens)/million*p.OutputPerMillion
}

// RestoreCheckpoint reverts RepoRoot to checkpoint, the operation id
// Outcome.Checkpoint reports. Run never calls this itself; it is the entry
// point a coordinator calls once it decides to act on a reported restore
// point rather than merely surface it.
func (l *Loop) RestoreCheckpoint(ctx context.Context, checkpoint string) error {
	if l.options.Checkpointer == nil {
		return errNoCheckpointer
	}

	if err := l.options.Checkpointer.Restore(ctx, l.options.RepoRoot, checkpoint); err != nil {
		return fmt.Errorf("restoring checkpoint %s: %w", checkpoint, err)
	}

	return nil
}

// run holds the mutable state of one Loop.Run call.
// The log detail key every harness verdict writes.
const detailPass = "pass"

type run struct {
	startTime time.Time
	deadline  time.Time
	thread    *thread.Thread
	gk        *gateKeeper
	lastCall  *llm.ToolCall
	loop      *Loop
	system    string
	task      string
	// answer is the prose of the turn that ended the run with no pending
	// tool call, which is what the finish checks read.
	answer    string
	changes   []tool.Change
	tools     []llm.ToolSpec
	compacted []thread.TurnMessage
	// fastTools is the surface a fast turn is shown, empty to show it tools.
	fastTools []llm.ToolSpec
	hint      router.Input
	// route is the tier the turn in flight was routed to, which is what
	// says whether there is still a tier above to escalate into.
	route router.Decision
	// priorSpend is what the thread's earlier runs cost, so a resumed run
	// can report the thread's total without the ceiling bounding it.
	outcome Outcome
	// priorSpend is what the thread's earlier runs cost, so a resumed run
	// can report the thread's total without the ceiling bounding it.
	priorSpend        float64
	compactedThrough  int
	verifyRounds      int
	reviewRounds      int
	turnToolCalls     int
	consecutiveErrors int
	// escalations is how many times this run has moved up a tier, which the
	// router reads back as PriorFailures.
	escalations int
	nudges      int
	// stuckEscalated keeps a stuck gate from escalating twice. The signal
	// stays true once it fires, since the gate goes on failing until the
	// run fixes it.
	stuckEscalated bool
	editAttempted  bool
}

// nudgeIfNothingChanged tells a run that has read for many turns and
// changed nothing to make its first edit. Holding an editing tool is the
// whole test: a plan thread's registry has none and is never told to start,
// and every other thread was given one because a change was wanted. The
// edit-shaped task wording that gates the fatal no-change rule is too narrow
// to gate a nudge, since it reads only the first line's verb and let a task
// opening with "Count the tool calls that failed" through as a question.
func (r *run) nudgeIfNothingChanged(ctx context.Context) error {
	every := r.loop.options.TurnsBeforeNudge
	if every <= 0 || r.nudges >= maxNudges || len(r.changes) > 0 {
		return nil
	}

	if r.outcome.Turns < every*(r.nudges+1) {
		return nil
	}

	if !r.canEdit() {
		return nil
	}

	r.nudges++

	if err := r.thread.AppendUser(ctx, fmt.Sprintf(
		"You have changed no file in %d turns. More reading is not progress on this task. "+
			"Make the smallest edit that starts it now, and read again afterwards if you need to.",
		r.outcome.Turns)); err != nil {
		return fmt.Errorf("appending no-change nudge: %w", err)
	}

	return nil
}

// canEdit reports whether this run was handed a tool that changes a file.
func (r *run) canEdit() bool {
	for _, t := range r.tools {
		if _, ok := editToolNames[t.Name]; ok {
			return true
		}
	}

	return false
}

// routeInput describes this turn for the router at the given size.
func (r *run) routeInput(estimated int) router.Input {
	return router.Input{
		Override:        r.hint.Override,
		Window:          r.loop.ContextWindow(),
		EstimatedTokens: estimated,
		PriorFailures:   r.escalations,
	}
}

// streamFailed decides what a provider failure means: a canceled context and
// an expired deadline are the bound that fired rather than the tier, and
// anything else is retried one tier up rather than on itself, so a run
// pinned to a failing provider moves rather than retrying it until the turn
// bound. The top tier has nowhere to go and stops.
func (r *run) streamFailed(ctx context.Context, model string, cause error) (bool, Outcome, error) {
	if ctx.Err() != nil {
		out, cerr := r.stopCanceled(ctx)

		return true, out, cerr
	}

	// stream bounds itself by the deadline on its own context, so the
	// caller's ctx is still live when a hung stream is cut off. Naming the
	// bound that fired beats reporting it as a provider failure.
	if r.pastDeadline() {
		out, derr := r.stopBound(ctx, StopDeadline, r.deadlineReason())

		return true, out, derr
	}

	if !r.escalate() {
		out, herr := r.stopFailed(ctx, cause)

		return true, out, herr
	}

	if err := r.logTierFailure(model, string(r.route.Choice), cause); err != nil {
		return true, Outcome{}, err
	}

	return false, Outcome{}, nil
}

// logTierFailure records the provider failure that moved this run up a tier.
// Without it the move leaves no trace at all: three lanes ran their whole
// task on the tier above after one upstream 429, and the records read as a
// router decision about task shape.
func (r *run) logTierFailure(model, tier string, cause error) error {
	if _, err := r.thread.Log().Append(event.Event{
		Kind:   event.KindError,
		Text:   fmt.Sprintf("%s tier failed and the turn moved up: %v", tier, cause),
		Detail: map[string]any{"tier": tier, "model": model, "escalated": true},
	}); err != nil {
		return fmt.Errorf("logging tier failure: %w", err)
	}

	return nil
}

// thinkingFor is this turn's reasoning toggle: the thread's own pin when it
// has one, and otherwise the tier the turn routed to.
func (r *run) thinkingFor(route router.Decision) *bool {
	if r.hint.Thinking != nil {
		return r.hint.Thinking
	}

	return r.loop.options.Thinking.For(route)
}

func (r *run) drive(ctx context.Context) (Outcome, error) {
	for {
		if ctx.Err() != nil {
			return r.stopCanceled(ctx)
		}
		if r.pastDeadline() {
			return r.stopBound(ctx, StopDeadline, r.deadlineReason())
		}
		if r.outcome.Turns >= r.loop.options.MaxTurns {
			reason := fmt.Sprintf("max turns reached (dead-man's switch): %d, %s elapsed",
				r.loop.options.MaxTurns, r.elapsed().Round(time.Second))

			return r.stopBound(ctx, StopMaxTurns, reason)
		}

		done, out, err := r.turn(ctx)
		if err != nil {
			return out, err
		}
		if done {
			return out, nil
		}
	}
}

// elapsed reports wall time since Run began, read from the configured
// Clock so it advances under a fake clock in tests without sleeping.
func (r *run) elapsed() time.Duration {
	return r.loop.options.Clock.Now().Sub(r.startTime)
}

// changedFileCount counts the distinct file paths across changes.
func changedFileCount(changes []tool.Change) int {
	seen := make(map[string]struct{}, len(changes))
	for _, c := range changes {
		seen[c.Path] = struct{}{}
	}

	return len(seen)
}

// turn runs one model call and, if it asked for tools, executes them. It
// returns done=true once Run should stop, along with the Outcome and error to
// return from drive in that case.
func (r *run) turn(ctx context.Context) (bool, Outcome, error) {
	r.thread.BeginTurn()
	r.outcome.Turns++
	r.turnToolCalls = 0

	if err := r.collectGateFeedback(ctx); err != nil {
		return true, Outcome{}, err
	}

	if err := r.nudgeIfNothingChanged(ctx); err != nil {
		return true, Outcome{}, err
	}

	if err := r.maybeCompact(r.estimateTokens(r.messages())); err != nil {
		return true, Outcome{}, err
	}

	messages := r.messages()
	route := router.Route(r.routeInput(r.estimateTokens(messages)))
	r.route = route
	provider := r.loop.providers.For(route)
	req := llm.Request{
		Model:    r.loop.options.Models.For(route),
		System:   r.system,
		Tools:    r.toolsFor(r.route.Choice),
		Messages: messages,
		Thinking: r.thinkingFor(route),
	}
	bound := r.loop.options.samplingFor(route)
	req.PresencePenalty, req.RepeatPenalty = bound.presence, bound.repeat

	releaseSlot, err := r.admitSlot(ctx, route.Choice)
	if err != nil {
		return true, Outcome{}, err
	}

	text, calls, usage, stopReason, err := r.stream(ctx, provider, req)

	releaseSlot()

	if err != nil {
		return r.streamFailed(ctx, req.Model, err)
	}

	if len(calls) == 0 {
		if recovered := r.recoverTextCalls(text); len(recovered) > 0 {
			calls, stopReason = recovered, llm.StopToolUse
		}
	}

	msg := llm.Message{Content: text, ToolCalls: calls}
	meta := thread.TurnMeta{Model: req.Model, Tier: string(r.route.Choice)}
	if err := r.thread.AppendAssistant(ctx, msg, usage, meta); err != nil {
		return true, Outcome{}, fmt.Errorf("appending assistant turn: %w", err)
	}

	if usage != nil {
		r.outcome.InputTokens += usage.InputTokens
		r.outcome.OutputTokens += usage.OutputTokens
		// Priced per model rather than per tier: a model with no price
		// costs nothing, which is what an on-box tier is.
		r.outcome.HostedSpendUSD += r.loop.PriceTurn(req.Model, *usage)
		r.outcome.ThreadSpendUSD = r.priorSpend + r.outcome.HostedSpendUSD
	}
	if r.loop.options.MaxHostedSpendUSD > 0 && r.outcome.HostedSpendUSD >= r.loop.options.MaxHostedSpendUSD {
		reason := fmt.Sprintf(
			"hosted spend ceiling reached: $%.4f spent (ceiling $%.4f) after %d turn(s); "+
				"thread %s has cost $%.4f and keeps its transcript, so another prompt continues it",
			r.outcome.HostedSpendUSD, r.loop.options.MaxHostedSpendUSD, r.outcome.Turns,
			r.thread.ID(), r.outcome.ThreadSpendUSD)
		out, err := r.stopBound(ctx, StopCostCeiling, reason)

		return true, out, err
	}

	if stopReason == llm.StopEndTurn {
		if len(calls) == 0 {
			return r.endTurnWithNoCalls(ctx, text, usage)
		}

		return r.finishOrVerify(ctx)
	}

	return r.runTools(ctx, calls)
}

// finishOrVerify runs once the model ends its turn with no pending tool
// call. With no verifier configured it completes exactly as before. With
// one configured, it verifies the changes accumulated across the run: a
// pass hands off to the review step, a failure appends trimmed feedback as a
// new turn so the model must fix it, and MaxVerifyRounds bounds how many
// times that can happen before the run stops as StopVerifyFailed instead of
// looping forever. A failure the run cannot have caused skips all of that
// and stops as StopTreeState, since another round of the same question gets
// the same answer.
func (r *run) finishOrVerify(ctx context.Context) (bool, Outcome, error) {
	if r.loop.options.Verifier == nil {
		return r.reviewOrComplete(ctx)
	}

	feedback, verdict := r.loop.options.Verifier.Verify(ctx, r.changes)
	r.verifyRounds++

	if err := r.logVerify(verdict); err != nil {
		return true, Outcome{}, err
	}

	if verdict == VerdictPass {
		return r.reviewOrComplete(ctx)
	}

	if verdict == VerdictUnattributed {
		out, err := r.stopBound(ctx, StopTreeState, feedback)

		return true, out, err
	}

	if r.verifyRounds >= r.loop.options.MaxVerifyRounds {
		reason := fmt.Sprintf("verification failed after %d round(s)", r.verifyRounds)
		out, err := r.stopBound(ctx, StopVerifyFailed, reason)

		return true, out, err
	}

	if err := r.thread.AppendUser(ctx, feedback); err != nil {
		return true, Outcome{}, fmt.Errorf("appending verification feedback: %w", err)
	}

	return false, Outcome{}, nil
}

func (r *run) complete(ctx context.Context) (bool, Outcome, error) {
	if err := r.runFinishChecks(ctx); err != nil {
		return true, Outcome{}, err
	}

	r.outcome.Elapsed = r.elapsed()
	r.outcome.Stop = StopComplete
	r.outcome.Reason = fmt.Sprintf(
		"the model ended its turn after %d turn(s) and every configured check passed", r.outcome.Turns)
	if err := r.thread.SetState(ctx, event.StateDone); err != nil {
		return true, Outcome{}, fmt.Errorf("setting state: %w", err)
	}

	return true, r.outcome, nil
}

// admitSlot takes a slot on the on-box model for a turn routed there, and
// pushes the run's deadline out by however long it waited. Without that
// shift the bound measures queueing rather than work, which is what made
// three threads on one slot all fail at three minutes having taken one turn
// each.
func (r *run) admitSlot(ctx context.Context, choice router.Choice) (func(), error) {
	if r.loop.options.LocalSlots == nil || choice != router.ChoiceFast {
		return func() {}, nil
	}

	waitedFrom := r.loop.options.Clock.Now()

	release, err := r.loop.options.LocalSlots.AdmitSlot(ctx, string(r.thread.ID()))
	if err != nil {
		return nil, fmt.Errorf("admitting a local turn: %w", err)
	}

	if waited := r.loop.options.Clock.Now().Sub(waitedFrom); waited > 0 && !r.deadline.IsZero() {
		r.deadline = r.deadline.Add(waited)
	}

	return release, nil
}

func (r *run) logVerify(verdict GateVerdict) error {
	ev := event.Event{
		Kind: event.KindGate,
		Text: fmt.Sprintf("verification round %d", r.verifyRounds),
		Detail: map[string]any{
			"round": r.verifyRounds, detailPass: verdict == VerdictPass, "verdict": string(verdict),
		},
	}
	if _, err := r.thread.Log().Append(ev); err != nil {
		return fmt.Errorf("logging verification round: %w", err)
	}

	return nil
}

// pastDeadline reports whether this run's wall-clock bound has elapsed.
// Checked before every tool call as well as every turn, because one turn
// may issue MaxToolCallsPerTurn edits and a bound that only holds between
// turns does not bound those.
// How much of the run's wall-clock budget is left, zero when no bound is
// configured.
func (r *run) remaining() time.Duration {
	if r.deadline.IsZero() {
		return 0
	}

	return r.deadline.Sub(r.loop.options.Clock.Now())
}

func (r *run) pastDeadline() bool {
	return !r.deadline.IsZero() && !r.loop.options.Clock.Now().Before(r.deadline)
}

func (r *run) deadlineReason() string {
	return fmt.Sprintf("deadline reached: %s elapsed, %d file(s) changed",
		r.elapsed().Round(time.Second), changedFileCount(r.changes))
}

func (r *run) runTools(ctx context.Context, calls []llm.ToolCall) (bool, Outcome, error) {
	for i := range calls {
		call := calls[i]

		if r.pastDeadline() {
			out, err := r.stopBound(ctx, StopDeadline, r.deadlineReason())

			return true, out, err
		}
		if r.turnToolCalls >= r.loop.options.MaxToolCallsPerTurn {
			reason := fmt.Sprintf("per-turn tool-call flood guard tripped: %d calls in one turn",
				r.loop.options.MaxToolCallsPerTurn)
			out, err := r.stopBound(ctx, StopToolCallFlood, reason)

			return true, out, err
		}
		if !json.Valid(call.Input) {
			return r.handleMalformedCall(ctx, call)
		}
		if _, ok := editToolNames[call.Name]; ok {
			r.editAttempted = true
		}

		if r.lastCall != nil && r.lastCall.Name == call.Name && bytes.Equal(r.lastCall.Input, call.Input) {
			return r.handleRepeatedCall(ctx, call)
		}

		result, err := r.runTool(ctx, call)
		if err != nil {
			return true, Outcome{}, err
		}
		r.lastCall = &call
		r.outcome.ToolCalls++
		r.turnToolCalls++

		if done, out, err := r.checkStagnation(ctx, call.Name, result.IsError, result.Cause); done {
			return true, out, err
		}
	}

	return false, Outcome{}, nil
}

// toolsFor is the surface one turn advertises, which is narrower on the
// fast tier when the caller configured one.
func (r *run) toolsFor(choice router.Choice) []llm.ToolSpec {
	if choice == router.ChoiceFast && len(r.fastTools) > 0 {
		return r.fastTools
	}

	return r.tools
}

func (r *run) toolNames() []string {
	out := make([]string, len(r.tools))
	for i, t := range r.tools {
		out[i] = t.Name
	}

	return out
}

// handleToolCallAsText is the branch for a turn that ended with no tool
// calls while its text renders one as markup. Completing here would report
// a run that changed nothing as a success, which is the worst outcome
// available: the model believes it acted, the user believes it acted, and
// the gates have nothing to check.
//
// It follows the same escalate-then-stop rule as a repeated call, since
// both are evidence the tier cannot drive this tool surface: the first
// occurrence hands the model a critique and lets the next turn run hosted,
// and a second stops the run as StopMalformedTool. The critique never
// quotes the markup back, because repeating the malformed form is what the
// model would then imitate.
func (r *run) handleToolCallAsText(ctx context.Context) (bool, Outcome, error) {
	if !r.escalate() {
		out, err := r.stopBound(ctx, StopMalformedTool,
			"the model wrote a tool call as text again after escalating")

		return true, out, err
	}

	if err := r.thread.AppendUser(ctx,
		"That turn made no tool call. Text describing a call does nothing, whatever markup "+
			"it uses. Make the call itself, and write no prose before it."); err != nil {
		return true, Outcome{}, fmt.Errorf("appending tool-call-as-text critique: %w", err)
	}

	return false, Outcome{}, nil
}

// endTurnWithoutCalls handles a turn that ended with no tool call. Most
// such turns are the model reporting it is done; the three checks here are
// the ones that are not, each a turn that changed nothing it said it would.
// A turn that made no tool call is either a provider that returned nothing
// or a model that said something and stopped, and those want opposite
// answers.
func (r *run) endTurnWithNoCalls(ctx context.Context, text string, usage *llm.Usage) (bool, Outcome, error) {
	if out, done, err := r.emptyTurn(ctx, text, usage); done {
		return true, out, err
	}

	return r.endTurnWithoutCalls(ctx, text)
}

// An empty turn is one that produced nothing the loop can act on: no
// prose, no tool call. It is a provider failure rather than a model
// choosing to stay silent, and telling the two apart matters because the
// silent-model path escalates and then fails the run for talking without
// acting.
//
// Measured against `stealth/ox-alpha`, six hosted runs in a row ended this
// way in two turns with no tool calls and no spend: the model is a
// reasoning model whose whole output arrived in a field the wire did not
// read, so every hosted turn looked like a shrug.
func (r *run) emptyTurn(ctx context.Context, text string, usage *llm.Usage) (Outcome, bool, error) {
	if strings.TrimSpace(text) != "" {
		return Outcome{}, false, nil
	}

	cause := ErrEmptyCompletion
	if usage != nil && usage.ReasoningBytes > 0 {
		cause = fmt.Errorf("%w, spending %d bytes on reasoning it did not act on",
			cause, usage.ReasoningBytes)
	}

	if r.escalate() {
		return Outcome{}, false, nil
	}

	// Raised rather than absorbed as a Stop. An empty completion is the
	// provider rejecting the request shape, not an outcome the run reached:
	// `stealth/ox-alpha` returns one for every call carrying tools, which is
	// reproducible with a two-property schema and no harness at all. A run
	// that reports it as a bound hides a broken tier behind a plausible
	// story about the model.
	out, err := r.stopFailed(ctx, cause)

	return out, true, err
}

// recoverTextCalls reads the calls a model rendered into its message body
// instead of emitting, before the turn is written down. Recovering later
// worked and left the transcript holding the prose and no call, so the one
// record of what a run asked for undercounted the fast tier exactly where
// the dialect leaks; anything reading the sidecar back saw a turn that
// called nothing and a tool result with no call above it.
func (r *run) recoverTextCalls(text string) []llm.ToolCall {
	if !looksLikeToolCallText(text, r.toolNames()) {
		return nil
	}

	calls := parseToolCallText(text, r.toolNames())
	r.outcome.RecoveredCalls += len(calls)

	return calls
}

func (r *run) endTurnWithoutCalls(ctx context.Context, text string) (bool, Outcome, error) {
	r.answer = text

	switch {
	case looksLikeToolCallText(text, r.toolNames()):
		// Recovery already ran and found nothing readable, so the markup is
		// all there is and the run is told to make the call instead.
		return r.handleToolCallAsText(ctx)
	case looksLikeQuestionToUser(text):
		return r.handleTalkedNotActed(ctx, StopAskedInProse,
			"the model offered to act instead of acting again after escalating",
			"Do not offer to do work; do it. If you genuinely need a decision only the user "+
				"can make, call the question tool. Otherwise finish the task and report what "+
				"you changed.")
	case looksLikeAnnouncedAction(text):
		return r.handleTalkedNotActed(ctx, StopAnnouncedNotDone,
			"the model announced a next step and took none again after escalating",
			"That turn ended by announcing a step and taking none. Make the call you just "+
				"described, in this turn, with no prose before it.")
	default:
		return r.finishOrVerify(ctx)
	}
}

// handleTalkedNotActed is the branch for a turn that ends describing work
// instead of doing it, whether by offering it or by announcing it.
// Completing here labels the run complete on the model's own account of
// itself, which is the claim the harness exists to refuse: an offer to run
// the tests is not a test run, and the user reads `complete` as the work
// being finished.
//
// It follows the same escalate-then-stop rule as a tool call written as
// text, since all three are a turn that ended having changed nothing it
// said it would: the first occurrence hands the model a critique and lets
// the next turn run hosted, and a second stops the run.
func (r *run) handleTalkedNotActed(ctx context.Context, stop Stop, reason, critique string) (bool, Outcome, error) {
	if !r.escalate() {
		out, err := r.stopBound(ctx, stop, reason)

		return true, out, err
	}

	if err := r.thread.AppendUser(ctx, critique); err != nil {
		return true, Outcome{}, fmt.Errorf("appending %s critique: %w", stop, err)
	}

	return false, Outcome{}, nil
}

// handleMalformedCall is runTools' branch for arguments that are not valid
// JSON. The call never ran, so sending it again is all the model has to do,
// which makes this the most recoverable malformed shape and the one that
// least deserves to end a thread: measured on the fixed task set, qwen3:8b
// lost two of four tasks to a single bad emission, one of them on turn two.
// It follows the same escalate-then-stop rule as a tool call written as
// text, and answers the call rather than leaving the turn's tool_use
// unpaired, which the provider requires.
func (r *run) handleMalformedCall(ctx context.Context, call llm.ToolCall) (bool, Outcome, error) {
	if !r.escalate() {
		out, err := r.stopBound(ctx, StopMalformedTool,
			fmt.Sprintf("malformed tool call %q: input is not valid JSON after escalating", call.Name))

		return true, out, err
	}

	if err := r.appendToolResult(ctx, call, tool.Fail(tool.CauseMalformed,
		"the arguments for %s were not valid JSON, so the call never ran. Send it again with the "+
			"arguments as one JSON object, writing any multi-line text as a single string with "+
			`\n escapes rather than real line breaks`, call.Name), "", nil); err != nil {
		return true, Outcome{}, err
	}
	r.outcome.ToolCalls++
	r.turnToolCalls++

	if done, out, err := r.checkStagnation(ctx, call.Name, true, tool.CauseMalformed); done {
		return true, out, err
	}

	return false, Outcome{}, nil
}

// handleRepeatedCall is runTools' branch for a call that repeats the
// immediately preceding call's name and input. A repeat is evidence the
// tier is stuck, not that the thread should die: the router escalates a
// tier per failure, and repeated malformed calls compound rather than
// self-correct. A repeat hands the model a critique and moves the next turn
// up a tier; a repeat with no tier left stops the run.
// This also feeds the independent, generalized stagnation count, since the
// critique is itself an error result.
func (r *run) handleRepeatedCall(ctx context.Context, call llm.ToolCall) (bool, Outcome, error) {
	if !r.escalate() {
		out, err := r.stopBound(ctx, StopLoopDetected,
			fmt.Sprintf("identical repeated tool call %q after escalating", call.Name))

		return true, out, err
	}

	if err := r.appendToolResult(ctx, call, tool.Fail(tool.CauseRepeat,
		"you already made this exact %s call and it did not move the task forward; "+
			"read the previous result, then either change the arguments or stop", call.Name), "", nil); err != nil {
		return true, Outcome{}, err
	}
	r.outcome.ToolCalls++
	r.turnToolCalls++

	if done, out, err := r.checkStagnation(ctx, call.Name, true, tool.CauseBadInput); done {
		return true, out, err
	}

	return false, Outcome{}, nil
}

// checkStagnation records one tool-call result toward the generalized
// stagnation bound and, once MaxStagnantErrors consecutive results have
// erred regardless of whether their inputs matched, stops the run. Any
// non-error result resets the count. This is independent of the exact-repeat
// detection in runTools, which keeps its own escalate-then-stop behavior.
//
// A refusal neither counts nor resets. It is the safeguard declining by
// design and naming what to do instead, so spending a third of the bound on
// it stops a run for having been told something: two lanes reached for
// `find`, were pointed at `search` and `list`, and had one attempt left
// before the bound. An exact repeat of a refused call is still caught, by
// the repeat detection in runTools.
func (r *run) checkStagnation(
	ctx context.Context, toolName string, isError bool, cause tool.Cause,
) (bool, Outcome, error) {
	if !isError {
		r.consecutiveErrors = 0

		return false, Outcome{}, nil
	}

	if cause == tool.CauseRefused {
		return false, Outcome{}, nil
	}

	r.consecutiveErrors++

	if r.loop.options.MaxStagnantErrors <= 0 || r.consecutiveErrors < r.loop.options.MaxStagnantErrors {
		return false, Outcome{}, nil
	}

	r.outcome.StagnantTool = toolName
	r.outcome.StagnantCount = r.consecutiveErrors
	reason := fmt.Sprintf("tool %q failed %d times in a row", toolName, r.consecutiveErrors)
	out, err := r.stopBound(ctx, StopStagnant, reason)

	return true, out, err
}

func (r *run) runTool(ctx context.Context, call llm.ToolCall) (tool.Result, error) {
	t, err := r.loop.tools.Get(call.Name)
	if err != nil {
		unknown := tool.Fail(tool.CauseBadInput, "unknown tool %q", call.Name)

		return unknown, r.appendToolResult(ctx, call, unknown, "", nil)
	}

	allowed, err := r.gk.check(ctx, t, call.Input)
	if err != nil {
		return tool.Result{}, fmt.Errorf("checking permission for %q: %w", call.Name, err)
	}
	if !allowed {
		denied := tool.Fail(tool.CauseRefused, "permission denied for %q", call.Name)

		return denied, r.appendToolResult(ctx, call, denied, "", nil)
	}

	proceed, err := r.preToolUse(ctx, t, call)
	if err != nil {
		return tool.Result{}, err
	}
	if !proceed {
		refused := tool.Fail(tool.CauseRefused, "pre-tool-use hook refused %q", call.Name)

		return refused, r.appendToolResult(ctx, call, refused, "", nil)
	}

	result, err := t.Run(ctx, call.Input)
	if err != nil {
		result = tool.Fail(tool.CauseIO, "%s: %v", call.Name, err)
	}
	r.changes = append(r.changes, result.Changes...)
	r.gateChanges(result.Changes)
	op, repos := r.checkpointEdit(ctx, result.Changes)

	if err := r.postToolUse(ctx, t, call, result); err != nil {
		return tool.Result{}, err
	}

	return result, r.appendToolResult(ctx, call, result, op, repos)
}

// gateChanges hands each change to the gate runner. It is fire-and-forget
// on purpose: a gate run takes seconds and an edit must not wait on one, so
// what the gates find arrives at the next turn instead.
func (r *run) gateChanges(changes []tool.Change) {
	if r.loop.options.ChangeGate == nil {
		return
	}

	for _, c := range changes {
		c.Writer = string(r.thread.ID())
		r.loop.options.ChangeGate.Enqueue(c)
	}
}

// checkpointEdit records the tree as it stands just after one accepted
// change, one operation per repository the changes landed in, and reports
// the operation id a single-repository caller can restore to: the run's own
// repository's when it holds one of the changes, else the first captured.
// A change under no jj repository records nothing for itself. A failure to
// capture is not a failure of the edit: the run keeps its whole-run
// checkpoint either way, so this loses granularity rather than
// recoverability.
func (r *run) checkpointEdit(ctx context.Context, changes []tool.Change) (string, thread.Checkpoints) {
	if len(changes) == 0 || r.loop.options.Checkpointer == nil {
		return "", nil
	}

	// repos maps each repository root to the operation captured for it, and
	// is what a caller restores one repository at a time with.
	repos := thread.Checkpoints{}

	// repoRoots groups the changes by the repository holding each path,
	// first-seen order, so one capture covers everything a repository took.
	repoRoots := map[string][]string{}

	var order []string

	for _, c := range changes {
		repo, err := r.loop.options.Checkpointer.RepoRoot(ctx, r.repoFor(c.Path))
		if err != nil {
			continue
		}

		if _, seen := repoRoots[repo]; !seen {
			order = append(order, repo)
		}

		repoRoots[repo] = append(repoRoots[repo], c.Path)
	}

	primary := ""

	for _, repo := range order {
		op, err := r.loop.options.Checkpointer.Capture(ctx, repo)
		if err != nil {
			continue
		}

		r.outcome.Edits = append(r.outcome.Edits, EditPoint{
			Repo: repo, Op: op, Paths: repoRoots[repo],
		})

		repos[repo] = op

		if primary == "" || repo == r.loop.options.RepoRoot {
			primary = op
		}
	}

	if len(repos) == 0 {
		return "", nil
	}

	return primary, repos
}

// repoFor names the directory whose repository a change's path belongs to:
// the run's own repository when the path is relative (every tool reports
// one), the path's own directory otherwise.
func (r *run) repoFor(path string) string {
	if !filepath.IsAbs(path) {
		return r.loop.options.RepoRoot
	}

	return filepath.Dir(path)
}

// collectGateFeedback appends whatever the change-triggered gates found
// since the last turn, so a failure reaches the model as its own turn
// rather than being folded into a tool result it might not read.
func (r *run) collectGateFeedback(ctx context.Context) error {
	if r.loop.options.ChangeGate == nil {
		return nil
	}

	if err := r.logFalseAlarms(); err != nil {
		return err
	}

	if err := r.escalateIfStuck(); err != nil {
		return err
	}

	feedback, failed := r.loop.options.ChangeGate.TakeFeedback()
	if feedback == "" {
		return nil
	}

	// The delivery is logged rather than the run: what a gate found and
	// never told the model is not a gate failure the run had to answer, and
	// counting only escalations under that name made the corpus read as
	// though no run was ever handed one.
	if _, err := r.thread.Log().Append(event.Event{
		Kind:   event.KindGate,
		Text:   deliveredText(failed),
		Detail: map[string]any{detailPass: !failed, "delivered": true},
	}); err != nil {
		return fmt.Errorf("logging the gate feedback: %w", err)
	}

	if err := r.thread.AppendUser(ctx, feedback); err != nil {
		return fmt.Errorf("appending gate feedback: %w", err)
	}

	return nil
}

// escalateIfStuck moves the run up a tier when a gate has failed the same
// way several times over, each after further edits. Every other escalation
// this loop makes reads one turn (a malformed call, an empty completion, a
// repeat), which catches a tier that cannot emit and never a tier that
// emits fine and cannot solve the problem. On `e2` the fast tier passes 3
// of 3 checks about once in six runs and the other five spend every turn on
// one compile error the gate quotes back, escalating only when the deadline
// does it for them.
//
// The escalation is logged and never told to the model: what it needs is
// the gate failure it already has, and a run told it has been moved up
// treats the move as the progress.
func (r *run) escalateIfStuck() error {
	name, stuck := r.loop.options.ChangeGate.Stuck()
	if !stuck || r.stuckEscalated {
		return nil
	}

	r.stuckEscalated = true

	moved := r.escalate()

	text := name + " has failed the same way across edits; moving up a tier"
	if !moved {
		text = name + " has failed the same way across edits, with no tier above to move into"
	}

	// The signal is logged whether or not it could act, because a run on the
	// top tier reaching it is the same finding and a silent return made the
	// corpus read as though the condition had never held.
	if _, err := r.thread.Log().Append(event.Event{
		Kind: event.KindGate,
		Text: text,
		Detail: map[string]any{
			"gate": name, detailPass: false, "escalated": moved,
		},
	}); err != nil {
		return fmt.Errorf("logging the escalation: %w", err)
	}

	return nil
}

// logFalseAlarms records every gate that retracted a failure. It reaches
// the log and never the model: a gate that was wrong twice is a fact about
// the harness, and telling a run about it invites it to discount the next
// failure.
func (r *run) logFalseAlarms() error {
	for _, name := range r.loop.options.ChangeGate.FalseAlarms() {
		r.outcome.GateFalseAlarms++

		if _, err := r.thread.Log().Append(event.Event{
			Kind: event.KindGate,
			Text: name + " passed over the change set it just failed over",
			Detail: map[string]any{
				"gate": name, detailPass: true, "false_alarm": true,
			},
		}); err != nil {
			return fmt.Errorf("logging a gate false alarm: %w", err)
		}
	}

	return nil
}

func (r *run) appendToolResult(
	ctx context.Context, call llm.ToolCall, result tool.Result, checkpoint string, repos thread.Checkpoints,
) error {
	if err := r.thread.AppendToolResult(ctx, call.ID, call.Name, call.Input, result, checkpoint, repos); err != nil {
		return fmt.Errorf("appending tool result: %w", err)
	}

	return nil
}

// stream reads one model response, bounded by the run's wall-clock
// deadline. Without that bound a provider that accepts the request and then
// stops sending blocks here forever: the deadline is otherwise only checked
// between turns and before tool calls, so a hung stream is exactly the case
// it would miss.
func (r *run) stream(
	ctx context.Context, provider llm.Provider, req llm.Request,
) (string, []llm.ToolCall, *llm.Usage, llm.StopReason, error) {
	var (
		text    bytes.Buffer
		calls   []llm.ToolCall
		usage   *llm.Usage
		stopRsn llm.StopReason
	)

	// Bounded by the budget remaining rather than by the deadline instant,
	// because a stream blocks in real time while the run's clock may be
	// injected. A run already past its deadline never reaches here: drive
	// checks that before every turn.
	if remaining := r.remaining(); remaining > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, remaining)
		defer cancel()
	}

	for chunk, err := range provider.Stream(ctx, req) {
		if err != nil {
			return "", nil, nil, "", err
		}
		switch chunk.Kind {
		case llm.ChunkText:
			text.WriteString(chunk.Text)
			if _, err := r.thread.Log().Append(event.Event{Kind: event.KindAgent, Text: chunk.Text}); err != nil {
				return "", nil, nil, "", fmt.Errorf("logging text chunk: %w", err)
			}
		case llm.ChunkToolCall:
			if chunk.ToolCall != nil {
				calls = append(calls, *chunk.ToolCall)
			}
		case llm.ChunkDone:
			usage = chunk.Usage
			stopRsn = chunk.StopReason
		}
	}

	return text.String(), calls, usage, stopRsn, nil
}

func (r *run) stopCanceled(ctx context.Context) (Outcome, error) {
	r.outcome.Elapsed = r.elapsed()
	r.outcome.Stop = StopCanceled
	r.outcome.Reason = "the run's context was canceled"
	if err := r.thread.SetState(context.WithoutCancel(ctx), event.StateIdle); err != nil {
		return r.outcome, fmt.Errorf("agent run canceled: %w (also failed logging cancellation: %w)", ctx.Err(), err)
	}

	return r.outcome, fmt.Errorf("agent run canceled: %w", ctx.Err())
}

func (r *run) stopBound(ctx context.Context, stop Stop, reason string) (Outcome, error) {
	r.outcome.Elapsed = r.elapsed()
	r.outcome.Stop = stop
	r.outcome.Reason = reason
	detail := map[string]any{"bound": string(stop)}
	if r.outcome.Checkpoint != "" {
		detail["checkpoint"] = r.outcome.Checkpoint
	}
	ev := event.Event{Kind: event.KindError, Text: reason, Detail: detail}
	if _, err := r.thread.Log().Append(ev); err != nil {
		return Outcome{}, fmt.Errorf("logging bound: %w", err)
	}
	r.verifyAbandoned(ctx)
	r.checkAbandoned(ctx)

	if err := r.thread.SetState(ctx, event.StateFailed); err != nil {
		return Outcome{}, fmt.Errorf("setting state: %w", err)
	}

	return r.outcome, nil
}

// verifyAbandoned runs the gates over whatever a bounded run already changed.
// Ending on a bound with edited files and no verification is the worst case:
// changed code and no signal. The result is logged, never fed back, because
// the run is over.
func (r *run) verifyAbandoned(ctx context.Context) {
	if r.loop.options.Verifier == nil || len(r.changes) == 0 {
		return
	}

	feedback, verdict := r.loop.options.Verifier.Verify(ctx, r.changes)
	ok := verdict == VerdictPass
	r.outcome.GatesPassedAtEnd = ok
	detail := map[string]any{detailPass: ok, "abandoned": true, "verdict": string(verdict)}
	if !ok {
		detail["feedback"] = feedback
	}
	if r.outcome.Checkpoint != "" {
		detail["checkpoint"] = r.outcome.Checkpoint
	}
	if _, err := r.thread.Log().Append(event.Event{
		Kind: event.KindGate, Text: gateText(ok), Detail: detail,
	}); err != nil {
		return
	}
}

// checkAbandoned runs the finish checks over a bounded run's closing prose,
// for the reason verifyAbandoned exists one layer over: a run that ended on a
// bound still hands its answer to whoever reads the thread, and a run that
// struggled enough to hit one is the likelier place for a name it invented.
// Two plan runs on a foreign repository ended `stagnant` with confident
// answers that nothing checked, one of them wrong about a file it had never
// been able to search.
//
// Its error is dropped rather than returned, because the run is already over
// and turning a bounded outcome into a failed one would lose the outcome the
// caller is waiting for.
func (r *run) checkAbandoned(ctx context.Context) {
	if r.answer == "" {
		return
	}

	err := r.runFinishChecks(ctx)
	if err == nil {
		return
	}

	// The run has ended, so the log is the only reader left.
	_, _ = r.thread.Log().Append(event.Event{ //nolint:errcheck // a failed write here has no reader
		Kind: event.KindError, Text: "finish checks did not run: " + err.Error(),
	})
}

// deliveredText names what the run was handed, which is the event the
// corpus counts as a gate round.
func deliveredText(failed bool) string {
	if failed {
		return "gates reported a failure to the run"
	}

	return "gates reported a pass to the run"
}

func gateText(ok bool) string {
	if ok {
		return "gates passed on the abandoned change set"
	}

	return "gates failed on the abandoned change set"
}

func (r *run) stopFailed(ctx context.Context, cause error) (Outcome, error) {
	r.outcome.Elapsed = r.elapsed()
	r.outcome.Stop = StopFailed
	reason := fmt.Sprintf("provider stream failed: %v", cause)
	r.outcome.Reason = reason
	detail := map[string]any{}
	if r.outcome.Checkpoint != "" {
		detail["checkpoint"] = r.outcome.Checkpoint
	}
	if _, err := r.thread.Log().Append(event.Event{Kind: event.KindError, Text: reason, Detail: detail}); err != nil {
		return Outcome{}, fmt.Errorf("logging failure: %w", err)
	}
	if err := r.thread.SetState(ctx, event.StateFailed); err != nil {
		return Outcome{}, fmt.Errorf("setting state: %w", err)
	}

	return r.outcome, fmt.Errorf("%w: %w", ErrScriptedFailure, cause)
}

// escalate moves the run one tier up and reports whether it could. A run
// already on the top tier cannot, which is what turns a retry into a stop.
func (r *run) escalate() bool {
	if r.route.Choice == router.ChoiceDeep {
		return false
	}
	r.escalations++

	return true
}

// estimateTokens sizes the request the next turn would send: the system
// prompt, the tool specs, and the history. The specs count because they
// travel with every request and, on a small window, are the difference
// between a request that fits and one llama-server refuses.
// The fast tier's surface is what it sizes against, because the estimate
// exists to decide whether the request fits the fast window and that is the
// request the fast tier would be sent.
func (r *run) estimateTokens(history []llm.Message) int {
	return estimateRequestTokens(r.system, r.toolsFor(router.ChoiceFast), history)
}

func estimateRequestTokens(system string, tools []llm.ToolSpec, history []llm.Message) int {
	total := len(system)
	for _, t := range tools {
		total += len(t.Name) + len(t.Description) + len(t.Schema)
	}
	for _, msg := range history {
		total += len(msg.Content)
	}

	return total / 4 //nolint:mnd // char/4 is the project's documented token estimate heuristic
}
