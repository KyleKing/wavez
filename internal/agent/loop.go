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

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
)

const (
	// DefaultMaxTurns is the default bound on model calls within one Run.
	DefaultMaxTurns = 20
	// DefaultMaxToolCalls is the default bound on tool executions within one Run.
	DefaultMaxToolCalls = 50
	// DefaultMaxVerifyRounds is the default bound on verification rounds
	// once the model reports it is done.
	DefaultMaxVerifyRounds = 2
)

// Stop names why Run returned.
type Stop string

// Reasons Run stops.
const (
	// StopComplete means the model ended its turn with no pending tool call.
	StopComplete Stop = "complete"
	// StopMaxTurns means Run reached its configured turn bound.
	StopMaxTurns Stop = "max_turns"
	// StopMaxToolCalls means Run reached its configured tool-call bound.
	StopMaxToolCalls Stop = "max_tool_calls"
	// StopMalformedTool means a tool call's input was not valid JSON.
	StopMalformedTool Stop = "malformed_tool_call"
	// StopLoopDetected means a tool call repeated the immediately preceding
	// call's name and input.
	StopLoopDetected Stop = "loop_detected"
	// StopCanceled means ctx was done before Run could reach another stop.
	StopCanceled Stop = "canceled"
	// StopFailed means the routed provider's stream failed for a reason
	// other than ctx cancellation, with no further tier to escalate to
	// (hosted itself failed, or local failed on a turn already routed
	// hosted).
	StopFailed Stop = "provider_failed"
	// StopVerifyFailed means the model's changes still failed verification
	// after MaxVerifyRounds rounds.
	StopVerifyFailed Stop = "verify_failed"
)

// ErrScriptedFailure is a sentinel a test provider may wrap to model a
// non-cancellation stream failure distinct from ctx.Err().
var ErrScriptedFailure = errors.New("agent: provider stream failed")

// Prefix holds the parts of an llm.Request that stay byte-identical across
// every turn of one Run: the system prompt (including the session ledger,
// which Run folds in once at the start) and the tool set. Only Messages grows
// turn over turn.
type Prefix struct {
	System string
	Ledger string
	Tools  []llm.ToolSpec
}

// Outcome reports how Run ended.
type Outcome struct {
	Stop      Stop
	Turns     int
	ToolCalls int
}

// Verifier gates a run once the model reports it is done, per DESIGN.md's
// decision to verify once on the final turn rather than on every turn.
// Verify reports ok=true when changes accumulated across the run pass, and
// on failure returns feedback already trimmed to what the model may see
// (the gate.Result.ForModel / gate.TrimFailure asymmetry).
type Verifier interface {
	Verify(ctx context.Context, changes []tool.Change) (feedback string, ok bool)
}

// Options bounds and configures a Loop.
type Options struct {
	Verifier        Verifier
	LocalModel      string
	HostedModel     string
	MaxTurns        int
	MaxToolCalls    int
	MaxVerifyRounds int
}

// Option configures a Loop.
type Option func(*Options)

// WithMaxTurns overrides DefaultMaxTurns.
func WithMaxTurns(n int) Option { return func(o *Options) { o.MaxTurns = n } }

// WithMaxToolCalls overrides DefaultMaxToolCalls.
func WithMaxToolCalls(n int) Option { return func(o *Options) { o.MaxToolCalls = n } }

// WithLocalModel sets the model name sent in a request routed local.
func WithLocalModel(name string) Option { return func(o *Options) { o.LocalModel = name } }

// WithHostedModel sets the model name sent in a request routed hosted.
func WithHostedModel(name string) Option { return func(o *Options) { o.HostedModel = name } }

// WithVerifier configures Run to gate once the model reports it is done,
// feeding a failing verification back as a new turn instead of trusting
// the model's own claim of completion. With no verifier configured, Run's
// behavior on model completion is unchanged.
func WithVerifier(v Verifier) Option { return func(o *Options) { o.Verifier = v } }

// WithMaxVerifyRounds overrides DefaultMaxVerifyRounds.
func WithMaxVerifyRounds(n int) Option { return func(o *Options) { o.MaxVerifyRounds = n } }

// Loop drives one thread's tool-use turns against a local and a hosted
// llm.Provider, chosen per turn by internal/router.
type Loop struct {
	local   llm.Provider
	hosted  llm.Provider
	tools   *tool.Registry
	gate    permission.Gate
	options Options
}

// New builds a Loop. Gate is consulted for any tool call whose Tool
// implements PermissionRequester and reports the call needs approval.
func New(local, hosted llm.Provider, tools *tool.Registry, gate permission.Gate, opts ...Option) *Loop {
	options := Options{
		MaxTurns: DefaultMaxTurns, MaxToolCalls: DefaultMaxToolCalls, MaxVerifyRounds: DefaultMaxVerifyRounds,
	}
	for _, opt := range opts {
		opt(&options)
	}

	return &Loop{local: local, hosted: hosted, tools: tools, gate: gate, options: options}
}

// Run appends prompt to th as a user turn, then drives model turns until the
// model ends its turn with no pending tool call or a bound trips. Hint seeds
// per-turn routing (Override and FileCount); Run fills in EstimatedTokens and
// forces hosted after the first local stream failure, since local is never
// retried past one failure.
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

	if err := th.AppendUser(ctx, prompt); err != nil {
		return Outcome{}, fmt.Errorf("appending prompt: %w", err)
	}
	if err := th.SetState(ctx, event.StateWorking); err != nil {
		return Outcome{}, fmt.Errorf("setting state: %w", err)
	}

	r := &run{loop: l, thread: th, system: system, tools: prefix.Tools, hint: hint, gk: newGateKeeper(l.gate)}

	return r.drive(ctx)
}

// run holds the mutable state of one Loop.Run call.
type run struct {
	loop         *Loop
	thread       *thread.Thread
	gk           *gateKeeper
	lastCall     *llm.ToolCall
	system       string
	tools        []llm.ToolSpec
	changes      []tool.Change
	outcome      Outcome
	hint         router.Input
	verifyRounds int
	localFailed  bool
}

func (r *run) drive(ctx context.Context) (Outcome, error) {
	for {
		if ctx.Err() != nil {
			return r.stopCanceled(ctx)
		}
		if r.outcome.Turns >= r.loop.options.MaxTurns {
			return r.stopBound(ctx, StopMaxTurns, fmt.Sprintf("max turns reached: %d", r.loop.options.MaxTurns))
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

// turn runs one model call and, if it asked for tools, executes them. It
// returns done=true once Run should stop, along with the Outcome and error to
// return from drive in that case.
func (r *run) turn(ctx context.Context) (bool, Outcome, error) {
	r.thread.BeginTurn()
	r.outcome.Turns++

	route := router.Route(router.Input{
		Override:        r.hint.Override,
		FileCount:       r.hint.FileCount,
		EstimatedTokens: estimateRequestTokens(r.system, r.thread.History()),
		PriorFailures:   priorFailures(r.localFailed),
	})
	provider := router.Select(route, r.loop.local, r.loop.hosted)
	req := llm.Request{
		Model:    router.Select(route, r.loop.options.LocalModel, r.loop.options.HostedModel),
		System:   r.system,
		Tools:    r.tools,
		Messages: r.thread.History(),
	}

	text, calls, usage, stopReason, err := r.stream(ctx, provider, req)
	if err != nil {
		if ctx.Err() != nil {
			out, cerr := r.stopCanceled(ctx)

			return true, out, cerr
		}
		if route.Choice == router.ChoiceLocal {
			r.localFailed = true

			return false, Outcome{}, nil
		}

		out, herr := r.stopFailed(ctx, err)

		return true, out, herr
	}

	msg := llm.Message{Content: text, ToolCalls: calls}
	if err := r.thread.AppendAssistant(ctx, msg, usage); err != nil {
		return true, Outcome{}, fmt.Errorf("appending assistant turn: %w", err)
	}

	if stopReason == llm.StopEndTurn {
		return r.finishOrVerify(ctx)
	}

	return r.runTools(ctx, calls)
}

// finishOrVerify runs once the model ends its turn with no pending tool
// call. With no verifier configured it completes exactly as before. With
// one configured, it verifies the changes accumulated across the run: a
// pass completes the run, a failure appends trimmed feedback as a new turn
// so the model must fix it, and MaxVerifyRounds bounds how many times that
// can happen before the run stops as StopVerifyFailed instead of looping
// forever.
func (r *run) finishOrVerify(ctx context.Context) (bool, Outcome, error) {
	if r.loop.options.Verifier == nil {
		return r.complete(ctx)
	}

	feedback, ok := r.loop.options.Verifier.Verify(ctx, r.changes)
	r.verifyRounds++

	if err := r.logVerify(ok); err != nil {
		return true, Outcome{}, err
	}

	if ok {
		return r.complete(ctx)
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
	r.outcome.Stop = StopComplete
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

func (r *run) runTools(ctx context.Context, calls []llm.ToolCall) (bool, Outcome, error) {
	for i := range calls {
		call := calls[i]

		if r.outcome.ToolCalls >= r.loop.options.MaxToolCalls {
			reason := fmt.Sprintf("max tool calls reached: %d", r.loop.options.MaxToolCalls)
			out, err := r.stopBound(ctx, StopMaxToolCalls, reason)

			return true, out, err
		}
		if !json.Valid(call.Input) {
			reason := fmt.Sprintf("malformed tool call %q: input is not valid JSON", call.Name)
			out, err := r.stopBound(ctx, StopMalformedTool, reason)

			return true, out, err
		}
		if r.lastCall != nil && r.lastCall.Name == call.Name && bytes.Equal(r.lastCall.Input, call.Input) {
			if r.localFailed {
				out, err := r.stopBound(ctx, StopLoopDetected,
					fmt.Sprintf("identical repeated tool call %q after escalating", call.Name))

				return true, out, err
			}

			// A repeat is evidence the tier is stuck, not that the thread should
			// die: the router already escalates after one local failure, and
			// repeated malformed calls compound rather than self-correct. Hand
			// the model back a critique and let the next turn run hosted.
			r.localFailed = true
			if err := r.appendToolResult(ctx, call, tool.Errorf(
				"you already made this exact %s call and it did not move the task forward; "+
					"read the previous result, then either change the arguments or stop", call.Name)); err != nil {
				return true, Outcome{}, err
			}
			r.outcome.ToolCalls++

			return false, Outcome{}, nil
		}

		if err := r.runTool(ctx, call); err != nil {
			return true, Outcome{}, err
		}
		r.lastCall = &call
		r.outcome.ToolCalls++
	}

	return false, Outcome{}, nil
}

func (r *run) runTool(ctx context.Context, call llm.ToolCall) error {
	t, err := r.loop.tools.Get(call.Name)
	if err != nil {
		unknown := tool.Errorf("unknown tool %q", call.Name)

		return r.appendToolResult(ctx, call, unknown)
	}

	allowed, err := r.gk.check(ctx, t, call.Input)
	if err != nil {
		return fmt.Errorf("checking permission for %q: %w", call.Name, err)
	}
	if !allowed {
		denied := tool.Errorf("permission denied for %q", call.Name)

		return r.appendToolResult(ctx, call, denied)
	}

	result, err := t.Run(ctx, call.Input)
	if err != nil {
		result = tool.Errorf("%s: %v", call.Name, err)
	}
	r.changes = append(r.changes, result.Changes...)

	return r.appendToolResult(ctx, call, result)
}

func (r *run) appendToolResult(ctx context.Context, call llm.ToolCall, result tool.Result) error {
	if err := r.thread.AppendToolResult(ctx, call.ID, call.Name, call.Input, result); err != nil {
		return fmt.Errorf("appending tool result: %w", err)
	}

	return nil
}

func (r *run) stream(
	ctx context.Context, provider llm.Provider, req llm.Request,
) (string, []llm.ToolCall, *llm.Usage, llm.StopReason, error) {
	var (
		text    bytes.Buffer
		calls   []llm.ToolCall
		usage   *llm.Usage
		stopRsn llm.StopReason
	)

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
	r.outcome.Stop = StopCanceled
	if err := r.thread.SetState(context.WithoutCancel(ctx), event.StateIdle); err != nil {
		return r.outcome, fmt.Errorf("agent run canceled: %w (also failed logging cancellation: %w)", ctx.Err(), err)
	}

	return r.outcome, fmt.Errorf("agent run canceled: %w", ctx.Err())
}

func (r *run) stopBound(ctx context.Context, stop Stop, reason string) (Outcome, error) {
	r.outcome.Stop = stop
	ev := event.Event{Kind: event.KindError, Text: reason, Detail: map[string]any{"bound": string(stop)}}
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
	r.outcome.Stop = StopFailed
	reason := fmt.Sprintf("provider stream failed: %v", cause)
	if _, err := r.thread.Log().Append(event.Event{Kind: event.KindError, Text: reason}); err != nil {
		return Outcome{}, fmt.Errorf("logging failure: %w", err)
	}
	if err := r.thread.SetState(ctx, event.StateFailed); err != nil {
		return Outcome{}, fmt.Errorf("setting state: %w", err)
	}

	return r.outcome, fmt.Errorf("%w: %w", ErrScriptedFailure, cause)
}

func priorFailures(localFailed bool) int {
	if localFailed {
		return 1
	}

	return 0
}

func estimateRequestTokens(system string, history []llm.Message) int {
	total := len(system)
	for _, msg := range history {
		total += len(msg.Content)
	}

	return total / 4 //nolint:mnd // char/4 is the project's documented token estimate heuristic
}
