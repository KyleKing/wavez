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
	// CmdSchedule asks for the whole fleet's lanes, the current scheduler
	// phase, and the lease list, which is what the schedule view renders.
	CmdSchedule CommandKind = "schedule"
	CmdDiff     CommandKind = "diff"
	CmdRestore  CommandKind = "restore"
	CmdRoute    CommandKind = "route"
	// CmdThink turns a hybrid model's reasoning trace on or off for a
	// thread's next turn.
	CmdThink CommandKind = "think"
	// CmdDiagReset clears the diagnostics window: the sparkline samples and
	// every counter scoped to it. Lifetime totals survive.
	CmdDiagReset CommandKind = "diag_reset"
	// CmdModels lists the models Ollama has on disk. It never contacts the
	// registry, so it is cheap enough to call whenever the screen opens.
	CmdModels CommandKind = "models"
	// CmdModelCheck asks the registry whether a newer manifest exists for
	// Model, or for every installed model when Model is empty. It never
	// installs anything.
	CmdModelCheck CommandKind = "model_check"
	// CmdModelInstall pulls Model. Without Confirm it reports the disk the
	// pull would take and installs nothing.
	CmdModelInstall CommandKind = "model_install"
	// CmdModelRemove uninstalls Model. Without Confirm it reports the disk
	// the removal frees and removes nothing.
	CmdModelRemove CommandKind = "model_remove"
	// CmdModelSettings replaces Model's runtime settings with Settings.
	CmdModelSettings CommandKind = "model_settings"
	// CmdRoutines lists the project's routines with their triggers and
	// recent runs.
	CmdRoutines CommandKind = "routines"
	// CmdRunRoutine runs one routine by name and answers with that
	// routine's refreshed row, the completed run included.
	CmdRunRoutine CommandKind = "run_routine"
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
	// Cycle names the phased way of working a new thread runs its prompt
	// through, empty for an ordinary thread. A thread's cycle is fixed at
	// creation, since a cycle's phases are what its work is made of.
	Cycle string `json:"cycle,omitempty"`
	// Routine names the routine run_routine executes.
	Routine string `json:"routine,omitempty"`
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
	// Settings carries the runtime flags a model is served with, for
	// model_settings.
	Settings *ModelSettings `json:"settings,omitempty"`
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
	RepHello    ReplyKind = "hello"
	RepThreads  ReplyKind = "threads"
	RepThread   ReplyKind = "thread"
	RepEvent    ReplyKind = "event"
	RepPending  ReplyKind = "pending"
	RepDiag     ReplyKind = "diag"
	RepSchedule ReplyKind = "schedule"
	RepDiff     ReplyKind = "diff"
	RepRestore  ReplyKind = "restore"
	RepError    ReplyKind = "error"
	// RepModels answers every model command with the whole list, so a client
	// never merges a partial update into a list it already holds.
	RepModels ReplyKind = "models"
	// RepLagged reports that a subscription dropped events. The client must
	// resubscribe from its last seen Seq rather than assume continuity.
	RepLagged ReplyKind = "lagged"
	// RepRoutines carries the project's routines. A run_routine reply
	// carries the single routine it ran.
	RepRoutines ReplyKind = "routines"
)

// Reply is one message from the daemon. ID echoes the Command that caused it,
// and is empty for anything the daemon pushes on its own.
type Reply struct {
	ID   string    `json:"id,omitempty"`
	Kind ReplyKind `json:"kind"`

	Error string `json:"error,omitempty"`
	// Note carries a command's human-readable outcome where it has one: the
	// disk a model install or removal would cost, reported before anything
	// happens so a client can confirm against it.
	Note     string        `json:"note,omitempty"`
	Thread   *ThreadInfo   `json:"thread,omitempty"`
	Event    *event.Event  `json:"event,omitempty"`
	Diag     *Diagnostics  `json:"diag,omitempty"`
	Schedule *Schedule     `json:"schedule,omitempty"`
	Diff     *Diff         `json:"diff,omitempty"`
	Restore  *Restore      `json:"restore,omitempty"`
	Threads  []ThreadInfo  `json:"threads,omitempty"`
	Routines []RoutineInfo `json:"routines,omitempty"`
	Pending  []PendingInfo `json:"pending,omitempty"`
	Models   []ModelInfo   `json:"models,omitempty"`
	Protocol int           `json:"protocol,omitempty"`
	LastSeq  uint64        `json:"last_seq,omitempty"`
}

// ModelSettings is how one model is served: the llama-server flags DESIGN.md
// tunes per laptop. A zero field means "use the shipped default", which is
// what lets a client show the default beside every value and restore it.
type ModelSettings struct {
	SpecType    string `json:"spec_type,omitempty"`
	ContextSize int    `json:"context_size,omitempty"`
	CacheReuse  int    `json:"cache_reuse,omitempty"`
	Threads     int    `json:"threads,omitempty"`
	BatchSize   int    `json:"batch_size,omitempty"`
}

// ModelInfo is one row on the model management screen: what is on disk, what
// loading it would leave free, and whether the registry has moved on.
type ModelInfo struct {
	Name      string `json:"name"`
	Tag       string `json:"tag"`
	Quant     string `json:"quant,omitempty"`
	ParamSize string `json:"param_size,omitempty"`
	// Settings is what wavez serves this model with, and Defaults is what it
	// ships with, so a client renders both and can restore one.
	Settings  ModelSettings `json:"settings"`
	Defaults  ModelSettings `json:"defaults"`
	SizeBytes uint64        `json:"size_bytes"`
	// FreeBytes is what stays free against the machine's ceiling once this
	// model is resident, so the cost of loading it is visible before the
	// scheduler has to refuse it.
	FreeBytes uint64 `json:"free_bytes"`
	// Checked reports whether an update check has run for this model. Until
	// it has, UpdateAvailable says nothing and a client renders a dash.
	Checked bool `json:"checked"`
	// UpdateAvailable reports that the registry holds a different manifest
	// for this tag. Wavez never acts on it.
	UpdateAvailable bool `json:"update_available"`
	// Loaded reports that this is the model the router serves local turns
	// with.
	Loaded bool `json:"loaded"`
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
	// Cycle is the phased way of working this thread runs, empty for an
	// ordinary thread. Phase is where that cycle has reached.
	Cycle string `json:"cycle,omitempty"`
	Phase string `json:"phase,omitempty"`
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

// RoutineInfo is one row in the routines panel: what fires the routine,
// what it does, and how its recent runs went.
type RoutineInfo struct {
	Name     string   `json:"name"`
	Triggers []string `json:"triggers,omitempty"`
	Steps    []string `json:"steps,omitempty"`
	// Runs are the routine's recent runs, oldest first, which is what the
	// panel's duration sparkline and its history view read.
	Runs    []RoutineRun `json:"runs,omitempty"`
	Enabled bool         `json:"enabled"`
}

// RoutineRun is one recorded run of a routine.
type RoutineRun struct {
	Started time.Time `json:"started"`
	Trigger string    `json:"trigger"`
	// Failed names the steps that did not pass, so a client can say what
	// broke without carrying every step's trimmed output.
	Failed   []string      `json:"failed,omitempty"`
	Duration time.Duration `json:"duration"`
	Pass     bool          `json:"pass"`
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

// Schedule is the fleet as the scheduler sees it: one lane per thread over
// the recent past, what the daemon is letting run, and who holds what.
type Schedule struct {
	// Phase is "edit" while threads write and gate runs queue, "execute"
	// while a gate run holds the machine.
	Phase string `json:"phase"`
	// LocalModel is the model resident for local turns, empty when none is.
	LocalModel    string  `json:"local_model,omitempty"`
	Lanes         []Lane  `json:"lanes,omitempty"`
	Leases        []Lease `json:"leases,omitempty"`
	MemUsedBytes  uint64  `json:"mem_used_bytes"`
	MemTotalBytes uint64  `json:"mem_total_bytes"`
	// Headroom is the free-memory fraction below which a local turn and a
	// gate run stop overlapping.
	Headroom float64 `json:"headroom"`
	// MemMeasured is false when memory could not be read, which a client
	// renders as unavailable rather than as an empty machine.
	MemMeasured bool `json:"mem_measured"`
}

// Lane is one thread's recent history, oldest cell first. Cells carry
// event.State values so a client picks its own glyphs.
type Lane struct {
	ThreadID string `json:"thread_id"`
	Thread   string `json:"thread"`
	// Step is what the thread is doing now, the same words Home shows.
	Step string `json:"step"`
	// Gate names the gate run this thread is waiting on or running, empty
	// when it is not gating.
	Gate string `json:"gate,omitempty"`
	// Lock is the subtree this thread waits on, empty when it waits on none.
	Lock string `json:"lock,omitempty"`
	// LockHolder names the thread holding Lock.
	LockHolder string        `json:"lock_holder,omitempty"`
	Cells      []event.State `json:"cells,omitempty"`
}

// Lease is one directory subtree's claim: who holds it, how strong the claim
// is, and who is waiting behind it.
type Lease struct {
	Subtree string `json:"subtree"`
	Holder  string `json:"holder"`
	// State is active while its holder writes, committed once the writes
	// land (a rebase risk rather than a concurrent-edit one), and expired
	// once a holder stops renewing it.
	State   string   `json:"state"`
	Waiters []string `json:"waiters,omitempty"`
}

// Gauge names one Diagnostics number, so a daemon that cannot measure it can
// say so instead of sending a zero a client would render as a reading.
type Gauge string

// Gauges a daemon may report as unmeasured. Each names one number on the
// diagnostics panel, so a client renders a dash for it rather than a zero and
// never has to infer which is which.
const (
	GaugeCacheRead Gauge = "cache_read"
	// GaugeMemory covers MemUsedBytes and MemTotalBytes together, since the
	// panel's memory row is unavailable or not as a whole.
	GaugeMemory     Gauge = "memory"
	GaugeModelBytes Gauge = "model_bytes"
	GaugeModelDisk  Gauge = "model_disk"
	GaugePrefixHit  Gauge = "prefix_hit"
	//nolint:gosec // a gauge name, not a credential
	GaugeTokensPerSec Gauge = "tokens_per_sec"
	GaugeContext      Gauge = "context"
	GaugeCPU          Gauge = "cpu"
	GaugeCPUDaemon    Gauge = "cpu_daemon"
	GaugeCPUModel     Gauge = "cpu_model"
	GaugeCPUGates     Gauge = "cpu_gates"
	GaugeCPUTUI       Gauge = "cpu_tui"
	GaugeHostedCalls  Gauge = "hosted_calls"
	// GaugeHostedLatency covers the hosted row's p50 and last together.
	GaugeHostedLatency Gauge = "hosted_latency"
	GaugeGateLatency   Gauge = "gate_latency"
	GaugeGateRunning   Gauge = "gate_running"
	// GaugeLeases covers LeasesHeld, LeasesWaiting, and LeaseWaitOn together,
	// since the scheduler either keeps leases or does not.
	GaugeLeases      Gauge = "leases"
	GaugeEscalations Gauge = "escalations"
	GaugeEvents      Gauge = "events"
	GaugeCompaction  Gauge = "compaction"
)

// Diagnostics is the strip in every header and the panel behind `D`. Every
// number here is one the daemon already keeps for its own decisions.
type Diagnostics struct {
	LocalModel string `json:"local_model,omitempty"`
	// GateRunning names the gate currently executing, empty when none is.
	GateRunning string `json:"gate_running,omitempty"`
	// LeaseWaitOn names the subtree the oldest waiting lease wants.
	LeaseWaitOn string `json:"lease_wait_on,omitempty"`
	// Unmeasured names the gauges whose value is absent rather than zero. A
	// client must render each of these as unavailable, since the field itself
	// carries the zero value either way.
	Unmeasured []Gauge `json:"unmeasured,omitempty"`
	// Sparks carries the window's samples per gauge, oldest first, for the
	// panel's sparklines. A gauge with no samples has no key.
	Sparks map[Gauge][]float64 `json:"sparks,omitempty"`
	// PerThread is what `Enter` on a panel row drills into.
	PerThread     []ThreadDiag `json:"per_thread,omitempty"`
	MemUsedBytes  uint64       `json:"mem_used_bytes"`
	MemTotalBytes uint64       `json:"mem_total_bytes"`
	ModelBytes    uint64       `json:"model_bytes"`
	// ModelDiskBytes is what every installed model takes on disk, which
	// bounds what the router may choose the same way memory does.
	ModelDiskBytes uint64  `json:"model_disk_bytes"`
	SpendToday     float64 `json:"spend_today"`
	TokensPerSec   float64 `json:"tokens_per_sec"`
	PrefixHit      float64 `json:"prefix_hit"`
	CacheRead      float64 `json:"cache_read"`
	CPUPercent     float64 `json:"cpu_percent"`
	CPUDaemon      float64 `json:"cpu_daemon"`
	CPUModel       float64 `json:"cpu_model"`
	CPUGates       float64 `json:"cpu_gates"`
	CPUTUI         float64 `json:"cpu_tui"`
	// HostedP50Ms and HostedLastMs are hosted-call latency over the window.
	HostedP50Ms  float64 `json:"hosted_p50_ms"`
	HostedLastMs float64 `json:"hosted_last_ms"`
	GateP50Ms    float64 `json:"gate_p50_ms"`
	// EventsPerSec is the window's event throughput across every thread.
	EventsPerSec float64 `json:"events_per_sec"`
	// ContextUsed and ContextWindow describe the most recently active
	// thread, since an occupied window belongs to one thread.
	ContextUsed    int `json:"context_used"`
	ContextWindow  int `json:"context_window"`
	HostedCalls    int `json:"hosted_calls"`
	GateQueue      int `json:"gate_queue"`
	GateFailures   int `json:"gate_failures"`
	GateRuns       int `json:"gate_runs"`
	LeasesHeld     int `json:"leases_held"`
	LeasesWaiting  int `json:"leases_waiting"`
	Threads        int `json:"threads"`
	NeedsInput     int `json:"needs_input"`
	ToolCalls      int `json:"tool_calls"`
	Malformed      int `json:"malformed"`
	Escalations    int `json:"escalations"`
	TranscriptRows int `json:"transcript_rows"`
	CompactionRuns int `json:"compaction_runs"`
	TokensSaved    int `json:"tokens_saved"`
}

// ThreadDiag is one thread's share of the panel's numbers, which is what a
// row drills into.
type ThreadDiag struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Dir     string  `json:"dir"`
	Spend   float64 `json:"spend"`
	Tokens  int     `json:"tokens"`
	Context int     `json:"context"`
	Window  int     `json:"window"`
	Rows    int     `json:"rows"`
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
