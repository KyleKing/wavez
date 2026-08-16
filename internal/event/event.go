// Package event defines the structured record every client renders. The TUI,
// the headless runner, and later the phone all read these and nothing else.
package event

import (
	"time"

	"github.com/kyleking/wavez/internal/tool"
)

// Kind discriminates an Event. Transcript rows are typed by it.
type Kind string

const (
	KindUser       Kind = "user"
	KindAgent      Kind = "agent"
	KindTool       Kind = "tool"
	KindGate       Kind = "gate"
	KindPermission Kind = "permission"
	KindState      Kind = "state"
	KindError      Kind = "error"
	KindLedger     Kind = "ledger"
	KindUsage      Kind = "usage"
)

// State is a thread's lifecycle position, rendered as a glyph in every view.
type State string

const (
	StateIdle    State = "idle"
	StateWorking State = "working"
	StateGating  State = "gating"
	StateNeedsIn State = "needs_input"
	StateBlocked State = "blocked"
	StateFailed  State = "failed"
	StateDone    State = "done"
)

// Event is one append-only record in a thread's log. Seq is unique and
// monotonic per thread, so a client can resume a stream from its last Seq.
type Event struct {
	Seq      uint64    `json:"seq"`
	ThreadID string    `json:"thread_id"`
	At       time.Time `json:"at"`
	Kind     Kind      `json:"kind"`

	// Text is the human-facing line for the row.
	Text string `json:"text,omitempty"`
	// Tool names the tool for KindTool and KindPermission.
	Tool string `json:"tool,omitempty"`
	// Changes carries file changes a tool produced, which is what triggers gates.
	Changes []tool.Change `json:"changes,omitempty"`
	// State is set on KindState.
	State State `json:"state,omitempty"`
	// Detail holds kind-specific structured fields for clients that want more
	// than Text, and is omitted from compaction.
	Detail map[string]any `json:"detail,omitempty"`
}
