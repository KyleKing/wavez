// Package event defines the structured record every client renders. The TUI,
// the headless runner, and later the phone all read these and nothing else.
package event

import (
	"time"

	"github.com/kyleking/wavez/internal/tool"
)

// Kind discriminates an Event. Transcript rows are typed by it.
type Kind string

// Kinds an Event may take.
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
	// KindCycle records a Cycle's phase transitions and the Condition
	// verdicts between them. It is not KindGate: a gate checks a change set,
	// where these say which phase the work is in and why it may or may not
	// advance.
	KindCycle Kind = "cycle"
	// KindHypothesis records one ledger row a phase produced: a candidate
	// cause, the experiment, the observation, and the verdict. It is the
	// content that crosses a phase boundary, and no Condition reads it.
	KindHypothesis Kind = "hypothesis"
	// KindReview records a model's judgment of a run's diff against its task.
	// It is deliberately not KindGate: a review objection never fails a run,
	// and counting it as one would report it as a run that failed a check.
	KindReview Kind = "review"
)

// State is a thread's lifecycle position, rendered as a glyph in every view.
type State string

// Positions a thread may hold.
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
	// Detail holds kind-specific structured fields for clients that want more
	// than Text, and is omitted from compaction.
	Detail   map[string]any `json:"detail,omitempty"`
	At       time.Time      `json:"at"`
	ThreadID string         `json:"thread_id"`
	Kind     Kind           `json:"kind"`
	// Text is the human-facing line for the row.
	Text string `json:"text,omitempty"`
	// Tool names the tool for KindTool and KindPermission.
	Tool string `json:"tool,omitempty"`
	// State is set on KindState.
	State State `json:"state,omitempty"`
	// Changes carries file changes a tool produced, which is what triggers gates.
	Changes []tool.Change `json:"changes,omitempty"`
	Seq     uint64        `json:"seq"`
}
