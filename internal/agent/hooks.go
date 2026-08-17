package agent

import (
	"context"
	"fmt"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/tool"
)

// Hooks is the pre- and post-tool-use pair, satisfied by *hook.Runner. It
// is declared here because this is where a hook is consumed: the loop is
// the only caller, and it needs exactly these four methods.
type Hooks interface {
	PreToolUseConfigured() bool
	PostToolUseConfigured() bool
	PreToolUse(ctx context.Context, call hook.Call) hook.Decision
	PostToolUse(ctx context.Context, call hook.Call, result tool.Result) hook.Observation
}

// WithHooks configures Run to consult external pre- and post-tool-use
// commands around every tool call. The hooks run after the permission gate
// and the guard, never instead of them: a hook may object to a call those
// allowed, and can never admit one they withheld.
func WithHooks(h Hooks) Option {
	return func(o *Options) { o.Hooks = h }
}

// hookCall describes one pending tool call to a hook. Paths come from the
// tool's own permission request, so they are the paths the tool declared
// rather than any this package guessed from the input.
func (r *run) hookCall(t tool.Tool, call llm.ToolCall) hook.Call {
	out := hook.Call{ThreadID: string(r.thread.ID()), Tool: call.Name, Input: call.Input}

	if requester, ok := t.(PermissionRequester); ok {
		if req, needs := requester.RequestPermission(call.Input); needs {
			out.Paths = req.Paths
		}
	}

	return out
}

// preToolUse reports whether the call may proceed. A refusal reaches the
// model as a fixed string with none of the hook's own text: a hook's output
// steering the model would be the policy-input channel DESIGN.md's Safety
// section forbids, so the reason goes to the thread log for the user
// instead.
func (r *run) preToolUse(ctx context.Context, t tool.Tool, call llm.ToolCall) (bool, error) {
	if r.loop.options.Hooks == nil || !r.loop.options.Hooks.PreToolUseConfigured() {
		return true, nil
	}

	decision := r.loop.options.Hooks.PreToolUse(ctx, r.hookCall(t, call))
	if decision.Verdict == hook.Allow {
		return true, nil
	}

	ev := event.Event{
		Kind: event.KindPermission,
		Tool: call.Name,
		Text: "pre-tool-use hook refused " + call.Name,
		Detail: map[string]any{
			"reason": decision.Reason, "output": decision.Output, "exit_code": decision.ExitCode,
		},
	}
	if _, err := r.thread.Log().Append(ev); err != nil {
		return false, fmt.Errorf("logging hook refusal: %w", err)
	}

	return false, nil
}

// postToolUse reports a hook that failed after the call ran. It never
// changes the result: the tool already ran, so there is nothing left to
// refuse.
func (r *run) postToolUse(ctx context.Context, t tool.Tool, call llm.ToolCall, result tool.Result) error {
	if r.loop.options.Hooks == nil || !r.loop.options.Hooks.PostToolUseConfigured() {
		return nil
	}

	obs := r.loop.options.Hooks.PostToolUse(ctx, r.hookCall(t, call), result)
	if obs.OK {
		return nil
	}

	ev := event.Event{
		Kind: event.KindError,
		Tool: call.Name,
		Text: "post-tool-use hook failed after " + call.Name,
		Detail: map[string]any{
			"reason": obs.Reason, "output": obs.Output, "exit_code": obs.ExitCode,
		},
	}
	if _, err := r.thread.Log().Append(ev); err != nil {
		return fmt.Errorf("logging hook failure: %w", err)
	}

	return nil
}
