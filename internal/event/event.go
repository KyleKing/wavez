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
	// KindGoal records the thread's standing goal, which is the first user
	// prompt until someone rewrites it. A rewrite appends another one rather
	// than editing the first, so what the goal was at any turn stays
	// readable.
	KindGoal Kind = "goal"
	// KindReview records a model's judgment of a run's diff against its task.
	// It is deliberately not KindGate: a review objection never fails a run,
	// and counting it as one would report it as a run that failed a check.
	KindReview Kind = "review"
)

// Role types a KindAgent turn's prose so a client can tell what must be read
// from what is good to know. The harness derives it from turn shape, never
// from the model stating which it is: a turn that ends a run with no
// pending tool call is RoleAnswer, one that precedes tool calls is
// RoleNote. It is carried on a terminating KindAgent event that has no
// Text, a marker rather than a row, and applies to the agent prose that
// immediately preceded it in the log.
type Role string

// Roles a KindAgent turn's prose may take.
const (
	RoleAnswer Role = "answer"
	RoleNote   Role = "note"
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
	// Role is set on the terminating KindAgent marker; see Role's doc.
	Role Role `json:"role,omitempty"`
	// Changes carries file changes a tool produced, which is what triggers gates.
	Changes []tool.Change `json:"changes,omitempty"`
	Seq     uint64        `json:"seq"`
}
