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
	// DefaultMaxWallClock bounds one Run's total wall-clock time. 180s is
	// dogfood.md's worst passing run (84s) plus roughly 2x headroom; it is a
	// starting number to replace once the benchmark harness has a real
	// distribution, not a tuned figure.
	DefaultMaxWallClock = 180 * time.Second
	// DefaultMaxHostedSpendUSD bounds accumulated hosted-tier spend for one
	// Run, at roughly fifty escalated turns of the hosted default's price in
	// DefaultPricing.
	DefaultMaxHostedSpendUSD = 0.10
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

// editToolNames are the tools that leave a tool.Change on success.
var editToolNames = map[string]struct{}{
	"str_replace": {},
	"write":       {},
}

// ModelPricing prices one model's hosted usage in dollars per million tokens.
type ModelPricing struct {
	InputPerMillion  float64
	OutputPerMillion float64
}

// DefaultPricing prices the models DESIGN.md's model-routing decision
// names, per their published OpenRouter rates. A model with no entry here
// prices at zero, so the cost ceiling never trips for a model wavez has no
// real price for.
//
//nolint:mnd // published per-million-token prices, not magic numbers
var DefaultPricing = map[string]ModelPricing{
	"openai/gpt-5-mini":                 {InputPerMillion: 0.25, OutputPerMillion: 2.00},
	"qwen/qwen3-coder":                  {InputPerMillion: 0.30, OutputPerMillion: 1.00},
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
)

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
	Turns        int
	ToolCalls    int
	InputTokens  int
	OutputTokens int
	// TokensCompacted is the estimated saving deterministic compaction made
	// across the run, zero when compaction never ran.
	TokensCompacted int
	Elapsed         time.Duration
	HostedSpendUSD  float64
	StagnantCount   int
}

// Condition reports the stop condition that held as the Verdict a Cycle
// phase's exit gate returns: a Loop's stop reasons and a phase's exit gate
// are the same idea at two granularities.
func (o Outcome) Condition() condition.Verdict {
	return condition.Met(string(o.Stop), o.Reason)
}

// Verifier gates a run once the model reports it is done, per DESIGN.md's
// decision to verify once on the final turn rather than on every turn.
// Verify reports ok=true when changes accumulated across the run pass, and
// on failure returns feedback already trimmed to what the model may see
// (the gate.Result.ForModel / gate.TrimFailure asymmetry).
type Verifier interface {
	Verify(ctx context.Context, changes []tool.Change) (feedback string, ok bool)
}

// Checkpointer captures and restores a jj checkpoint around a run, per
// DESIGN.md's VCS decision to take checkpointing from jj's operation log
// instead of writing own snapshots. Capture must be cheap enough to call
// before every run, since jj snapshots the working copy as a side effect
// of any command. Restore must be safe to call when nothing changed since
// the checkpoint it is given.
type Checkpointer interface {
	Capture(ctx context.Context, repoRoot string) (string, error)
	Restore(ctx context.Context, repoRoot, checkpoint string) error
}

// Options bounds and configures a Loop.
type Options struct {
	Verifier     Verifier
	Reviewer     Reviewer
	Checkpointer Checkpointer
	Clock        gate.Clock
	Hooks        Hooks
	ChangeGate   ChangeGate
	Pricing      map[string]ModelPricing
	// Models is the model name sent in a request routed to each tier.
	Models   router.Tiers[string]
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
	CompactEnabled      bool
}

// Option configures a Loop.
type Option func(*Options)

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

// WithContextWindow sets the fast tier's served context in tokens.
func WithContextWindow(n int) Option { return func(o *Options) { o.ContextWindow = n } }

// ChangeGate receives every file change a tool makes and reports what the
// gates it triggered found. It is declared here because the loop is what
// consumes it: gates fire on change events rather than on the model
// deciding to test, and their findings reach the model on its next turn.
type ChangeGate interface {
	Enqueue(c tool.Change)
	TakeFeedback() string
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

	checkpoint, err := l.captureCheckpoint(ctx)
	if err != nil {
		return Outcome{}, err
	}

	if err := th.AppendUser(ctx, prompt); err != nil {
		return Outcome{}, fmt.Errorf("appending prompt: %w", err)
	}
	if err := th.SetState(ctx, event.StateWorking); err != nil {
		return Outcome{}, fmt.Errorf("setting state: %w", err)
	}

	start := l.options.Clock.Now()

	var deadline time.Time
	if l.options.MaxWallClock > 0 {
		deadline = start.Add(l.options.MaxWallClock)
	}

	r := &run{
		loop: l, thread: th, system: system, tools: prefix.Tools, hint: hint, gk: newGateKeeper(l.gate),
		task: prompt, startTime: start, deadline: deadline,
	}
	r.outcome.Checkpoint = checkpoint

	return r.drive(ctx)
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

// priceTurn prices one turn's usage against the configured per-model table.
// A model with no pricing entry contributes zero cost, so the cost ceiling
// never trips on a model wavez has no real price for. CacheReadTokens is not
// priced separately yet, pending real cache-rate data.
func (l *Loop) priceTurn(model string, usage *llm.Usage) float64 {
	p, ok := l.options.Pricing[model]
	if !ok {
		return 0
	}

	return float64(usage.InputTokens)/million*p.InputPerMillion + float64(usage.OutputTokens)/million*p.OutputPerMillion
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
type run struct {
	startTime time.Time
	deadline  time.Time
	thread    *thread.Thread
	gk        *gateKeeper
	lastCall  *llm.ToolCall
	loop      *Loop
	system    string
	task      string
	changes   []tool.Change
	tools     []llm.ToolSpec
	compacted []thread.TurnMessage
	hint      router.Input
	// route is the tier the turn in flight was routed to, which is what
	// says whether there is still a tier above to escalate into.
	route             router.Decision
	outcome           Outcome
	compactedThrough  int
	verifyRounds      int
	reviewRounds      int
	turnToolCalls     int
	consecutiveErrors int
	// escalations is how many times this run has moved up a tier, which the
	// router reads back as PriorFailures.
	escalations   int
	nudges        int
	editAttempted bool
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
		Tools:    r.tools,
		Messages: messages,
		Thinking: r.hint.Thinking,
	}

	text, calls, usage, stopReason, err := r.stream(ctx, provider, req)
	if err != nil {
		if ctx.Err() != nil {
			out, cerr := r.stopCanceled(ctx)

			return true, out, cerr
		}
		// stream bounds itself by the deadline on its own context, so the
		// caller's ctx is still live when a hung stream is cut off. Naming
		// the bound that fired beats reporting it as a provider failure.
		if r.pastDeadline() {
			out, derr := r.stopBound(ctx, StopDeadline, r.deadlineReason())

			return true, out, derr
		}
		// A failed tier is retried one tier up rather than on itself, so a
		// run pinned to a failing provider moves rather than retrying it
		// until the turn bound. The top tier has nowhere to go and stops.
		if r.escalate() {
			return false, Outcome{}, nil
		}

		out, herr := r.stopFailed(ctx, err)

		return true, out, herr
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
		r.outcome.HostedSpendUSD += r.loop.priceTurn(req.Model, usage)
	}
	if r.loop.options.MaxHostedSpendUSD > 0 && r.outcome.HostedSpendUSD >= r.loop.options.MaxHostedSpendUSD {
		reason := fmt.Sprintf("hosted spend ceiling reached: $%.4f spent (ceiling $%.4f) after %d turn(s)",
			r.outcome.HostedSpendUSD, r.loop.options.MaxHostedSpendUSD, r.outcome.Turns)
		out, err := r.stopBound(ctx, StopCostCeiling, reason)

		return true, out, err
	}

	if stopReason == llm.StopEndTurn {
		if len(calls) == 0 {
			return r.endTurnWithoutCalls(ctx, text)
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
// looping forever.
func (r *run) finishOrVerify(ctx context.Context) (bool, Outcome, error) {
	if r.loop.options.Verifier == nil {
		return r.reviewOrComplete(ctx)
	}

	feedback, ok := r.loop.options.Verifier.Verify(ctx, r.changes)
	r.verifyRounds++

	if err := r.logVerify(ok); err != nil {
		return true, Outcome{}, err
	}

	if ok {
		return r.reviewOrComplete(ctx)
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
	r.outcome.Elapsed = r.elapsed()
	r.outcome.Stop = StopComplete
	r.outcome.Reason = fmt.Sprintf(
		"the model ended its turn after %d turn(s) and every configured check passed", r.outcome.Turns)
	if err := r.thread.SetState(ctx, event.StateDone); err != nil {
		return true, Outcome{}, fmt.Errorf("setting state: %w", err)
	}

	return true, r.outcome, nil
}

func (r *run) logVerify(ok bool) error {
	ev := event.Event{
		Kind:   event.KindGate,
		Text:   fmt.Sprintf("verification round %d", r.verifyRounds),
		Detail: map[string]any{"round": r.verifyRounds, "pass": ok},
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

		if done, out, err := r.checkStagnation(ctx, call.Name, result.IsError); done {
			return true, out, err
		}
	}

	return false, Outcome{}, nil
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
func (r *run) endTurnWithoutCalls(ctx context.Context, text string) (bool, Outcome, error) {
	switch {
	case looksLikeToolCallText(text, r.toolNames()):
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

	if err := r.appendToolResult(ctx, call, tool.Errorf(
		"the arguments for %s were not valid JSON, so the call never ran. Send it again with the "+
			"arguments as one JSON object, writing any multi-line text as a single string with "+
			`\n escapes rather than real line breaks`, call.Name)); err != nil {
		return true, Outcome{}, err
	}
	r.outcome.ToolCalls++
	r.turnToolCalls++

	if done, out, err := r.checkStagnation(ctx, call.Name, true); done {
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

	if err := r.appendToolResult(ctx, call, tool.Errorf(
		"you already made this exact %s call and it did not move the task forward; "+
			"read the previous result, then either change the arguments or stop", call.Name)); err != nil {
		return true, Outcome{}, err
	}
	r.outcome.ToolCalls++
	r.turnToolCalls++

	if done, out, err := r.checkStagnation(ctx, call.Name, true); done {
		return true, out, err
	}

	return false, Outcome{}, nil
}

// checkStagnation records one tool-call result toward the generalized
// stagnation bound and, once MaxStagnantErrors consecutive results have
// erred regardless of whether their inputs matched, stops the run. Any
// non-error result resets the count. This is independent of the exact-repeat
// detection in runTools, which keeps its own escalate-then-stop behavior.
func (r *run) checkStagnation(ctx context.Context, toolName string, isError bool) (bool, Outcome, error) {
	if !isError {
		r.consecutiveErrors = 0

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
		unknown := tool.Errorf("unknown tool %q", call.Name)

		return unknown, r.appendToolResult(ctx, call, unknown)
	}

	allowed, err := r.gk.check(ctx, t, call.Input)
	if err != nil {
		return tool.Result{}, fmt.Errorf("checking permission for %q: %w", call.Name, err)
	}
	if !allowed {
		denied := tool.Errorf("permission denied for %q", call.Name)

		return denied, r.appendToolResult(ctx, call, denied)
	}

	proceed, err := r.preToolUse(ctx, t, call)
	if err != nil {
		return tool.Result{}, err
	}
	if !proceed {
		refused := tool.Errorf("pre-tool-use hook refused %q", call.Name)

		return refused, r.appendToolResult(ctx, call, refused)
	}

	result, err := t.Run(ctx, call.Input)
	if err != nil {
		result = tool.Errorf("%s: %v", call.Name, err)
	}
	r.changes = append(r.changes, result.Changes...)
	r.gateChanges(result.Changes)

	if err := r.postToolUse(ctx, t, call, result); err != nil {
		return tool.Result{}, err
	}

	return result, r.appendToolResult(ctx, call, result)
}

// gateChanges hands each change to the gate runner. It is fire-and-forget
// on purpose: a gate run takes seconds and an edit must not wait on one, so
// what the gates find arrives at the next turn instead.
func (r *run) gateChanges(changes []tool.Change) {
	if r.loop.options.ChangeGate == nil {
		return
	}

	for _, c := range changes {
		r.loop.options.ChangeGate.Enqueue(c)
	}
}

// collectGateFeedback appends whatever the change-triggered gates found
// since the last turn, so a failure reaches the model as its own turn
// rather than being folded into a tool result it might not read.
func (r *run) collectGateFeedback(ctx context.Context) error {
	if r.loop.options.ChangeGate == nil {
		return nil
	}

	feedback := r.loop.options.ChangeGate.TakeFeedback()
	if feedback == "" {
		return nil
	}

	if err := r.thread.AppendUser(ctx, feedback); err != nil {
		return fmt.Errorf("appending gate feedback: %w", err)
	}

	return nil
}

func (r *run) appendToolResult(ctx context.Context, call llm.ToolCall, result tool.Result) error {
	if err := r.thread.AppendToolResult(ctx, call.ID, call.Name, call.Input, result); err != nil {
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

	feedback, ok := r.loop.options.Verifier.Verify(ctx, r.changes)
	detail := map[string]any{"pass": ok, "abandoned": true}
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
func (r *run) estimateTokens(history []llm.Message) int {
	return estimateRequestTokens(r.system, r.tools, history)
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
