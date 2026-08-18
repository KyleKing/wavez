// Package thread manages one work stream's directory set, append-only
// history, compaction state, and event log. The TUI, the headless runner, and
// the phone all read a thread through its eventlog.Log and nothing else.
package thread

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/eventlog"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/tool"
)

// maxLoggedInput bounds the tool input kept in the event log.
const maxLoggedInput = 2000

// ID names one thread. It is the eventlog file's base name, so it must be
// filesystem-safe.
type ID string

// TurnMessage pairs a history message with the loop turn that produced it, so
// compaction rules can reason about age without the thread mutating stored
// messages to attach that metadata later.
type TurnMessage struct {
	Message llm.Message
	Turn    int
}

// Thread is one work stream: a directory set, a model override, an optional
// parent, a lifecycle event.State, and an append-only history. It is safe for
// concurrent use.
//
// History is append-only: no exported method can replace or remove an earlier
// entry. Compaction (see compaction.go) reads a copy and returns a shorter
// replacement view; it never edits Thread's stored entries. That is what
// keeps a provider's prompt-cache prefix valid turn over turn, since editing
// the middle of a cached prefix measured 5-7x the cost of an append on the
// local runtime.
type Thread struct {
	log     *eventlog.Log
	id      ID
	model   string
	parent  ID
	state   event.State
	dirs    []string
	entries []TurnMessage
	turn    int
}

// Option configures a Thread at Open.
type Option func(*Thread)

// WithModel sets a model override for the thread.
func WithModel(model string) Option {
	return func(t *Thread) { t.model = model }
}

// WithParent records the thread this one forked from.
func WithParent(parent ID) Option {
	return func(t *Thread) { t.parent = parent }
}

// Open opens or resumes the thread's event log under logDir and returns a
// Thread positioned at event.StateIdle. Dirs is the thread's directory set.
func Open(logDir string, id ID, dirs []string, opts ...Option) (*Thread, error) {
	log, err := eventlog.Open(logDir, string(id), eventlog.Options{})
	if err != nil {
		return nil, fmt.Errorf("opening thread log: %w", err)
	}

	t := &Thread{id: id, dirs: dirs, log: log, state: event.StateIdle}
	for _, opt := range opts {
		opt(t)
	}

	return t, nil
}

// ID returns the thread's identity.
func (t *Thread) ID() ID { return t.id }

// Dirs returns the thread's directory set.
func (t *Thread) Dirs() []string {
	out := make([]string, len(t.dirs))
	copy(out, t.dirs)

	return out
}

// Model returns the thread's model override, empty when unset.
func (t *Thread) Model() string { return t.model }

// Parent returns the thread this one forked from, empty for a root thread.
func (t *Thread) Parent() ID { return t.parent }

// State returns the thread's current lifecycle position.
func (t *Thread) State() event.State { return t.state }

// Turn returns the most recently started loop turn number.
func (t *Thread) Turn() int { return t.turn }

// Log returns the thread's event log.
func (t *Thread) Log() *eventlog.Log { return t.log }

// BeginTurn starts a new loop turn and returns its number. Messages appended
// afterward are tagged with it until the next BeginTurn call.
func (t *Thread) BeginTurn() int {
	t.turn++

	return t.turn
}

// SetState records a lifecycle transition and appends a KindState event.
// It does nothing and returns ctx.Err() if ctx is already canceled.
func (t *Thread) SetState(ctx context.Context, state event.State) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	t.state = state
	if _, err := t.log.Append(event.Event{Kind: event.KindState, State: state}); err != nil {
		return fmt.Errorf("logging state: %w", err)
	}

	return nil
}

// AppendUser appends a user message to history and logs a KindUser event.
// It does nothing and returns ctx.Err() if ctx is already canceled.
func (t *Thread) AppendUser(ctx context.Context, text string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	t.entries = append(t.entries, TurnMessage{Message: llm.Message{Role: llm.RoleUser, Content: text}, Turn: t.turn})
	if _, err := t.log.Append(event.Event{Kind: event.KindUser, Text: text}); err != nil {
		return fmt.Errorf("logging user turn: %w", err)
	}

	return nil
}

// AppendAssistant appends the model's response to history and logs a
// KindAgent event summarizing it. Usage may be nil when the stream ended
// without one. It does nothing and returns ctx.Err() if ctx is already
// canceled, which keeps a mid-stream cancellation from committing a partial
// assistant turn.
func (t *Thread) AppendAssistant(ctx context.Context, msg llm.Message, usage *llm.Usage) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	msg.Role = llm.RoleAssistant
	t.entries = append(t.entries, TurnMessage{Message: msg, Turn: t.turn})

	detail := map[string]any{"tool_calls": len(msg.ToolCalls)}
	if usage != nil {
		detail["usage"] = usage
	}
	role := event.RoleNote
	if len(msg.ToolCalls) == 0 {
		role = event.RoleAnswer
	}
	if _, err := t.log.Append(event.Event{Kind: event.KindAgent, Role: role, Detail: detail}); err != nil {
		return fmt.Errorf("logging agent turn: %w", err)
	}

	return nil
}

// AppendToolResult appends a tool's result to history, tagged to toolCallID,
// and logs a KindTool event carrying the changes it produced and the input the
// model sent, truncated. The input is logged because a failed edit anchor
// cannot be diagnosed after the fact without it.
func (t *Thread) AppendToolResult(
	ctx context.Context, toolCallID, toolName string, input json.RawMessage, result tool.Result,
) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	msg := llm.Message{
		Role:       llm.RoleTool,
		Content:    result.Content,
		ToolCallID: toolCallID,
		IsError:    result.IsError,
	}
	t.entries = append(t.entries, TurnMessage{Message: msg, Turn: t.turn})

	ev := event.Event{Kind: event.KindTool, Tool: toolName, Text: result.Content, Changes: result.Changes}
	if len(input) > 0 {
		ev.Detail = map[string]any{"input": truncate(string(input), maxLoggedInput)}
	}
	if _, err := t.log.Append(ev); err != nil {
		return fmt.Errorf("logging tool turn: %w", err)
	}

	return nil
}

// History returns a copy of the thread's messages in append order, suitable
// for building an llm.Request. Mutating the returned slice does not affect
// the thread.
func (t *Thread) History() []llm.Message {
	out := make([]llm.Message, len(t.entries))
	for i, e := range t.entries {
		out[i] = e.Message
	}

	return out
}

// TurnHistory returns a copy of the thread's messages paired with the turn
// each was appended in, for compaction rules that reason about age.
func (t *Thread) TurnHistory() []TurnMessage {
	out := make([]TurnMessage, len(t.entries))
	copy(out, t.entries)

	return out
}

// Close flushes and releases the thread's event log.
func (t *Thread) Close() error {
	if err := t.log.Close(); err != nil {
		return fmt.Errorf("closing thread log: %w", err)
	}

	return nil
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}

	return s[:limit] + "…"
}

func contextErr(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context: %w", err)
	}

	return nil
}
