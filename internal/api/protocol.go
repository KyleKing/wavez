// Package api defines the JSON protocol every client speaks to wavezd over a
// unix socket. The TUI, the headless runner, and the phone all use these
// commands and these events, which is what keeps a new client from becoming a
// rewrite.
package api

import (
	"time"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/permission"
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
	// Question is set when this is a question rather than a permission prompt,
	// so the client shows a text field instead of yes/no/always.
	Question bool `json:"question,omitempty"`
}

// Diagnostics is the strip in every header and the panel behind `D`. Every
// number here is one the daemon already keeps for its own decisions.
type Diagnostics struct {
	LocalModel    string  `json:"local_model,omitempty"`
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
