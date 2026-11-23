package transcript

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Kind is what one step of a session was.
type Kind string

// The kinds of step. The first six split the tool calls the way Class does,
// with the two shell calls the gates and refactors exist to replace pulled
// out of the shell class; the rest are the steps that are not tool calls.
const (
	KindRetrieval Kind = "retrieval"
	KindEdit      Kind = "edit"
	KindShell     Kind = "shell"
	KindSweep     Kind = "sweep"
	KindRefactor  Kind = "refactor"
	KindOther     Kind = "other"
	// KindProse is an assistant turn that called no tool.
	KindProse Kind = "prose"
	// KindPrompt is a message from the person, or a notification the
	// harness delivered in the person's place.
	KindPrompt Kind = "prompt"
	// KindHook is one run of the Stop hooks.
	KindHook Kind = "hook"
	// KindCompact is the harness compacting the conversation.
	KindCompact Kind = "compact"
)

// Kinds lists every kind in the order a report shows them.
func Kinds() []Kind {
	return []Kind{
		KindRetrieval, KindEdit, KindShell, KindSweep, KindRefactor, KindOther, KindProse,
		KindPrompt, KindHook, KindCompact,
	}
}

// Work reports whether the kind is something the agent did, as opposed to
// a prompt, a hook run, or a compaction.
func (k Kind) Work() bool {
	switch k {
	case KindRetrieval, KindEdit, KindShell, KindSweep, KindRefactor, KindOther, KindProse:
		return true
	case KindPrompt, KindHook, KindCompact:
		return false
	default:
		return false
	}
}

// Usage is the tokens of one request to the model, or a sum of them.
type Usage struct {
	Input         int `json:"input"`
	CacheRead     int `json:"cache_read"`
	CacheCreation int `json:"cache_creation"`
	Output        int `json:"output"`
}

// Context is every token the request carried in.
func (u Usage) Context() int { return u.Input + u.CacheRead + u.CacheCreation }

func (u *Usage) add(o Usage) {
	u.Input += o.Input
	u.CacheRead += o.CacheRead
	u.CacheCreation += o.CacheCreation
	u.Output += o.Output
}

// Step is one thing that happened in a session, in order.
type Step struct {
	// Time is when the step was written, zero when the line carried none.
	Time time.Time `json:"time"`
	Kind Kind      `json:"kind"`
	// Label is the tool and its file, or the first words of the command or
	// prompt, cut short.
	Label string `json:"label"`
	// Detail is the whole command or prompt, kept only WithDetail.
	Detail string `json:"detail,omitempty"`
	Tool   string `json:"tool,omitempty"`
	// Path is the file a read or an edit named.
	Path string `json:"path,omitempty"`
	// Duration is the time from a tool call to its result, or how long a
	// hook ran. Zero means the transcript did not say.
	Duration time.Duration `json:"duration,omitempty"`
	Index    int           `json:"index"`
	// Turn numbers the assistant message the step belongs to from 1, and
	// is 0 for a step the agent did not take.
	Turn int `json:"turn,omitempty"`
	// Prompt numbers the prompt the step followed from 1.
	Prompt int `json:"prompt,omitempty"`
	// Failed marks a tool call whose result was an error, Denied one a
	// hook refused, and Blocked a stop the hooks held.
	Failed  bool `json:"failed,omitempty"`
	Denied  bool `json:"denied,omitempty"`
	Blocked bool `json:"blocked,omitempty"`
}

// Turn is one request to the model: the steps it produced and what it cost.
type Turn struct {
	Model string `json:"model,omitempty"`
	// Kind is the strongest thing the turn did.
	Kind  Kind  `json:"kind"`
	Usage Usage `json:"usage"`
	// First is the index of the turn's first step.
	First int `json:"first"`
	// HasUsage is false when the transcript recorded no token counts.
	HasUsage bool `json:"has_usage"`
}

// Segment is the work that followed one prompt.
type Segment struct {
	Label string `json:"label"`
	// Prompt numbers the segment from 1; First is its prompt's step.
	Prompt int `json:"prompt"`
	First  int `json:"first"`
	// Steps counts the agent's steps and Turns its requests to the model.
	Steps int `json:"steps"`
	Turns int `json:"turns"`
}

// FileCount is a file and how many times the Read tool opened it.
type FileCount struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// Sweep is one whole-project build, test, or lint run through the shell.
type Sweep struct {
	Command string `json:"command"`
	Step    int    `json:"step"`
}

// Run is a stretch of consecutive steps.
type Run struct {
	First  int `json:"first"`
	Length int `json:"length"`
}

// Denial is a shell command a PreToolUse hook refused as a rename by text.
type Denial struct {
	// Suggested holds the `wavez rename` lines the hook offered instead.
	Suggested []string `json:"suggested,omitempty"`
	Step      int      `json:"step"`
	// Retried is true when the same command was sent again.
	Retried bool `json:"retried"`
}

// TestChange is an edit that took a test out of play.
type TestChange struct {
	Path string `json:"path"`
	// Name is the test removed, or the line that skips one.
	Name string `json:"name"`
	// What is "skipped" or "removed".
	What string `json:"what"`
	Step int    `json:"step"`
}

// GateOutcome is how one run of the Stop hooks ended.
type GateOutcome string

// The outcomes of a Stop hook run.
const (
	// GatePass is a run that said nothing.
	GatePass GateOutcome = "pass"
	// GateNote is a run that let the agent stop and told someone something.
	GateNote GateOutcome = "note"
	// GateBlock is a run that held the agent.
	GateBlock GateOutcome = "block"
	// GateReleased is a run that gave up holding the agent on a failure.
	GateReleased GateOutcome = "released"
	// GateError is a hook that could not run.
	GateError GateOutcome = "error"
)

// GateCheck is one check a Stop hook's text named and what it said of it.
type GateCheck struct {
	Name string `json:"name"`
	// Status is "fail", "advisory", "not checked", "already failing", or
	// "not compared".
	Status string `json:"status"`
	// Line is the line that named the check, cut short.
	Line string `json:"line,omitempty"`
}

// GateRound is one run of the Stop hooks.
type GateRound struct {
	Time    time.Time   `json:"time"`
	Outcome GateOutcome `json:"outcome"`
	// Headline is the first line of what was said, cut short.
	Headline string `json:"headline,omitempty"`
	// Agent and Person are the whole texts, kept only WithDetail.
	Agent  string `json:"agent,omitempty"`
	Person string `json:"person,omitempty"`
	// Checks are the checks the text named. A run that said nothing names
	// none, since the transcript does not record what passed.
	Checks []GateCheck `json:"checks,omitempty"`
	// Duration is how long the hooks ran, zero when not recorded.
	Duration time.Duration `json:"duration,omitempty"`
	// AgentBytes and PersonBytes are how much text each was handed.
	AgentBytes  int `json:"agent_bytes"`
	PersonBytes int `json:"person_bytes"`
	Step        int `json:"step"`
}

// Cost is what the harness recorded about the whole session, subagents
// included. Recorded is false when the transcript holds no such line.
type Cost struct {
	USD float64 `json:"usd"`
	// Model is the time spent waiting on the model, Tools the time spent
	// running tools, and Wall the session's wall clock.
	Model    time.Duration `json:"model"`
	Tools    time.Duration `json:"tools"`
	Wall     time.Duration `json:"wall"`
	Recorded bool          `json:"recorded"`
	// Partial is true when a model's price was unknown to the harness.
	Partial bool `json:"partial,omitempty"`
}

// SessionTimeline is one session in order, with what can be derived from
// the order.
type SessionTimeline struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	// HookBytes is the text hooks handed the agent, by hook event.
	HookBytes map[string]int `json:"hook_bytes,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	// Cwd is the directory the session ran in.
	Cwd      string      `json:"cwd,omitempty"`
	Steps    []Step      `json:"steps"`
	Turns    []Turn      `json:"turns"`
	Segments []Segment   `json:"segments,omitempty"`
	Gates    []GateRound `json:"gates,omitempty"`
	// RepeatReads lists the files read more than once, most read first.
	RepeatReads []FileCount  `json:"repeat_reads,omitempty"`
	Sweeps      []Sweep      `json:"sweeps,omitempty"`
	Denials     []Denial     `json:"denials,omitempty"`
	TestChanges []TestChange `json:"test_changes,omitempty"`
	// Models lists the models that answered, in order of first use.
	Models []string `json:"models,omitempty"`
	// Edited lists the files the edit tools wrote, in order of first write.
	Edited []string `json:"edited,omitempty"`
	Cost   Cost     `json:"cost"`
	// Usage sums every turn that recorded its tokens.
	Usage Usage `json:"usage"`
	// LongestRetrieval is the longest stretch of retrieval steps that
	// nothing but prose interrupted.
	LongestRetrieval Run `json:"longest_retrieval"`
	// RetrievalBeforeEdit counts the retrieval steps before the first
	// edit, or all of them when nothing was edited.
	RetrievalBeforeEdit int `json:"retrieval_before_edit"`
	// TurnsAfterLastEdit counts the turns after the one that made the
	// last edit. It means nothing when Edits is zero.
	TurnsAfterLastEdit int `json:"turns_after_last_edit"`
	Edits              int `json:"edits"`
	// PeakContext is the most tokens one request carried in.
	PeakContext int `json:"peak_context"`
	Compactions int `json:"compactions,omitempty"`
	// RouteHints counts the prompts a hook added context to.
	RouteHints int `json:"route_hints,omitempty"`
	// Subagents counts the subagent transcripts ReadTimeline found, and
	// SubagentSteps and SubagentUsage what they held. Their steps are not
	// in Steps.
	Subagents     int   `json:"subagents,omitempty"`
	SubagentSteps int   `json:"subagent_steps,omitempty"`
	SubagentUsage Usage `json:"subagent_usage"`
	// Skipped counts lines that were not valid JSON.
	Skipped int `json:"skipped_lines,omitempty"`
}

// WorkSteps counts the steps the agent took.
func (t *SessionTimeline) WorkSteps() int {
	n := 0

	for i := range t.Steps {
		if t.Steps[i].Kind.Work() {
			n++
		}
	}

	return n
}

// TimelineOption changes what Timeline keeps.
type TimelineOption func(*timelineBuilder)

// WithDetail keeps whole commands, prompts, and hook texts beside their
// labels. Without it a timeline holds labels only.
func WithDetail() TimelineOption {
	return func(b *timelineBuilder) { b.detail = true }
}

// Timeline reads one transcript into its steps. Like Read, it counts and
// skips a line that is not JSON.
func Timeline(r io.Reader, opts ...TimelineOption) (SessionTimeline, error) {
	b := newTimelineBuilder()
	for _, opt := range opts {
		opt(b)
	}

	br := bufio.NewReader(r)

	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			b.line(line)
		}

		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return SessionTimeline{}, fmt.Errorf("reading transcript: %w", err)
		}
	}

	return b.finish(), nil
}

// ReadTimeline reads the transcript at path and counts what the subagents
// it launched did, without adding their steps to the timeline.
func ReadTimeline(path string, opts ...TimelineOption) (SessionTimeline, error) {
	tl, err := timelineFile(path, opts...)
	if err != nil {
		return SessionTimeline{}, err
	}

	pattern := filepath.Join(strings.TrimSuffix(path, extension), "subagents", "*"+extension)

	subs, err := filepath.Glob(pattern)
	if err != nil {
		return SessionTimeline{}, fmt.Errorf("searching %s: %w", pattern, err)
	}

	for _, sub := range subs {
		s, err := timelineFile(sub)
		if err != nil {
			return SessionTimeline{}, err
		}

		tl.Subagents++
		tl.SubagentSteps += s.WorkSteps()
		tl.SubagentUsage.add(s.Usage)
	}

	return tl, nil
}

func timelineFile(path string, opts ...TimelineOption) (SessionTimeline, error) {
	f, err := os.Open(path) //nolint:gosec // the caller names the transcript to read
	if err != nil {
		return SessionTimeline{}, fmt.Errorf("opening transcript: %w", err)
	}

	tl, err := Timeline(f, opts...)

	return tl, errors.Join(err, f.Close())
}
