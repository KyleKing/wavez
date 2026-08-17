// Package api defines the JSON protocol every client speaks to wavezd over a
// unix socket. The TUI, the headless runner, and the phone all use these
// commands and these events, which is what keeps a new client from becoming a
// rewrite.
package api

import (
	"time"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
)

// Protocol is the wire version. A client refuses a server that does not match.
const Protocol = 1

// CommandKind names a request from a client.
type CommandKind string

// Commands a client may send.
const (
	CmdHello     CommandKind = "hello"
	CmdList      CommandKind = "list"
	CmdNew       CommandKind = "new"
	CmdSend      CommandKind = "send"
	CmdSubscribe CommandKind = "subscribe"
	CmdAnswer    CommandKind = "answer"
	CmdCancel    CommandKind = "cancel"
	CmdDiag      CommandKind = "diag"
	CmdDiff      CommandKind = "diff"
	CmdRestore   CommandKind = "restore"
	CmdRoute     CommandKind = "route"
	// CmdThink turns a hybrid model's reasoning trace on or off for a
	// thread's next turn.
	CmdThink CommandKind = "think"
)

// Command is one request. ID correlates the Reply and is chosen by the client.
type Command struct {
	ID   string      `json:"id"`
	Kind CommandKind `json:"kind"`

	// ThreadID targets an existing thread for send, subscribe, answer, cancel.
	ThreadID string `json:"thread_id,omitempty"`
	// Prompt carries the text for new and send.
	Prompt string `json:"prompt,omitempty"`
	// Model overrides the router for this thread.
	Model string `json:"model,omitempty"`
	// Override pins ThreadID to one routing tier for every turn it runs
	// from now on. Empty clears the pin back to automatic routing, which is
	// why route carries no separate clear command.
	Override router.Choice `json:"override,omitempty"`
	// Thinking turns a hybrid model's reasoning trace on or off. Nil
	// restores the served model's own default, which the flag sets in both
	// directions, so absent and false are not the same thing.
	Thinking *bool `json:"thinking,omitempty"`
	// Parent makes this a sub-thread.
	Parent string `json:"parent,omitempty"`
	// Answer carries a permission decision or a question's text.
	Answer   string              `json:"answer,omitempty"`
	Decision permission.Decision `json:"decision,omitempty"`
	// PromptID names the pending prompt an answer resolves.
	PromptID string `json:"prompt_id,omitempty"`
	// Dirs is the directory set for new, defaulting to the daemon's scope.
	Dirs []string `json:"dirs,omitempty"`
	// From resumes a subscription after the client's last seen Seq.
	From uint64 `json:"from,omitempty"`
	// Confirm performs a restore instead of previewing what it would
	// discard, since destroying uncommitted work without asking is worse
	// than leaving it.
	Confirm bool `json:"confirm,omitempty"`
}

// ReplyKind names a message from the daemon.
type ReplyKind string

// Replies the daemon may send.
const (
	RepHello   ReplyKind = "hello"
	RepThreads ReplyKind = "threads"
	RepThread  ReplyKind = "thread"
	RepEvent   ReplyKind = "event"
	RepPending ReplyKind = "pending"
	RepDiag    ReplyKind = "diag"
	RepDiff    ReplyKind = "diff"
	RepRestore ReplyKind = "restore"
	RepError   ReplyKind = "error"
	// RepLagged reports that a subscription dropped events. The client must
	// resubscribe from its last seen Seq rather than assume continuity.
	RepLagged ReplyKind = "lagged"
)

// Reply is one message from the daemon. ID echoes the Command that caused it,
// and is empty for anything the daemon pushes on its own.
type Reply struct {
	ID   string    `json:"id,omitempty"`
	Kind ReplyKind `json:"kind"`

	Error    string        `json:"error,omitempty"`
	Thread   *ThreadInfo   `json:"thread,omitempty"`
	Event    *event.Event  `json:"event,omitempty"`
	Diag     *Diagnostics  `json:"diag,omitempty"`
	Diff     *Diff         `json:"diff,omitempty"`
	Restore  *Restore      `json:"restore,omitempty"`
	Threads  []ThreadInfo  `json:"threads,omitempty"`
	Pending  []PendingInfo `json:"pending,omitempty"`
	Protocol int           `json:"protocol,omitempty"`
	LastSeq  uint64        `json:"last_seq,omitempty"`
}

// ThreadInfo is one row in Home: what the thread is doing and what it cost.
type ThreadInfo struct {
	LastEvent time.Time `json:"last_event"`
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Dir       string    `json:"dir"`
	Parent    string    `json:"parent,omitempty"`
	Model     string    `json:"model,omitempty"`
	// Override is the routing tier this thread is pinned to, empty when it
	// routes automatically. A client renders it because a pinned tier is not
	// recoverable from the model name alone.
	Override router.Choice `json:"override,omitempty"`
	// Thinking is the thread's reasoning-trace pin, nil when the thread
	// follows the served model's own default.
	Thinking *bool `json:"thinking,omitempty"`
	// Checkpoint is the operation id captured before the thread's first
	// turn, empty until it has run one. A client offers undo only for a
	// thread that has one.
	Checkpoint string `json:"checkpoint,omitempty"`
	// Step is the current activity in words, which is what Home renders.
	Step    string      `json:"step"`
	State   event.State `json:"state"`
	Dirs    []string    `json:"dirs,omitempty"`
	Spend   float64     `json:"spend"`
	Tokens  int         `json:"tokens"`
	Context int         `json:"context"`
	Window  int         `json:"window"`
	Seq     uint64      `json:"seq"`
}

// PendingInfo is one row in the inbox: a permission prompt or a question.
type PendingInfo struct {
	Asked    time.Time `json:"asked"`
	ID       string    `json:"id"`
	ThreadID string    `json:"thread_id"`
	Thread   string    `json:"thread"`
	Dir      string    `json:"dir"`
	Tool     string    `json:"tool,omitempty"`
	Action   string    `json:"action"`
	Detail   string    `json:"detail,omitempty"`
	// Reason is why approval is needed, carried from permission.Request.
	Reason string `json:"reason,omitempty"`
	// Question is set when this is a question rather than a permission prompt,
	// so the client shows a text field instead of yes/no/always.
	Question bool `json:"question,omitempty"`
}

// Diff is a thread's change set as text a client can render, rather than
// the counts ThreadInfo carries. It is fetched on demand because a diff is
// unbounded in a way an event stream should not be.
type Diff struct {
	ThreadID string `json:"thread_id"`
	// Unified is git-format unified diff text covering every file the thread
	// changed since its first turn started. Empty means the thread has
	// changed nothing, which a client renders differently from an error.
	Unified string `json:"unified"`
}

// Restore is a thread's checkpoint and the work restoring it costs. A
// preview reports Summary with Restored false; the confirming command
// reports what it discarded with Restored true.
type Restore struct {
	ThreadID   string `json:"thread_id"`
	Checkpoint string `json:"checkpoint"`
	// Summary is the per-file diff stat between the checkpoint and the
	// working copy, which is the uncommitted work a restore destroys.
	Summary  string `json:"summary"`
	Restored bool   `json:"restored"`
}

// Gauge names one Diagnostics number, so a daemon that cannot measure it can
// say so instead of sending a zero a client would render as a reading.
type Gauge string

// Gauges a daemon may report as unmeasured.
const (
	GaugeCacheRead Gauge = "cache_read"
	// GaugeMemory covers MemUsedBytes and MemTotalBytes together, since the
	// panel's memory row is unavailable or not as a whole.
	GaugeMemory     Gauge = "memory"
	GaugeModelBytes Gauge = "model_bytes"
	GaugePrefixHit  Gauge = "prefix_hit"
	//nolint:gosec // a gauge name, not a credential
	GaugeTokensPerSec Gauge = "tokens_per_sec"
)

// Diagnostics is the strip in every header and the panel behind `D`. Every
// number here is one the daemon already keeps for its own decisions.
type Diagnostics struct {
	LocalModel string `json:"local_model,omitempty"`
	// Unmeasured names the gauges whose value is absent rather than zero. A
	// client must render each of these as unavailable, since the field itself
	// carries the zero value either way.
	Unmeasured    []Gauge `json:"unmeasured,omitempty"`
	MemUsedBytes  uint64  `json:"mem_used_bytes"`
	MemTotalBytes uint64  `json:"mem_total_bytes"`
	ModelBytes    uint64  `json:"model_bytes"`
	SpendToday    float64 `json:"spend_today"`
	TokensPerSec  float64 `json:"tokens_per_sec"`
	PrefixHit     float64 `json:"prefix_hit"`
	CacheRead     float64 `json:"cache_read"`
	GateQueue     int     `json:"gate_queue"`
	GateFailures  int     `json:"gate_failures"`
	GateRuns      int     `json:"gate_runs"`
	Threads       int     `json:"threads"`
	NeedsInput    int     `json:"needs_input"`
	ToolCalls     int     `json:"tool_calls"`
	Malformed     int     `json:"malformed"`
}

// Measured reports whether g holds a real reading.
func (d Diagnostics) Measured(g Gauge) bool {
	for _, u := range d.Unmeasured {
		if u == g {
			return false
		}
	}

	return true
}
