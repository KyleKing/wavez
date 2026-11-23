package transcript

import (
	"cmp"
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// labelLimit is the longest label a step carries, in runes.
const labelLimit = 72

// detailLimit is the longest detail kept WithDetail, in bytes.
const detailLimit = 4000

// wireEntry is one transcript line, reduced to what a timeline reads.
//
//nolint:tagliatelle // the tags are the harness's own field names
type wireEntry struct {
	ToolUseID        string          `json:"toolUseID"`
	Cwd              string          `json:"cwd"`
	SessionID        string          `json:"sessionId"`
	Subtype          string          `json:"subtype"`
	Type             string          `json:"type"`
	Timestamp        string          `json:"timestamp"`
	Message          wireMessage     `json:"message"`
	ToolUseResult    json.RawMessage `json:"toolUseResult"`
	HookErrors       []string        `json:"hookErrors"`
	HookInfos        []wireHookInfo  `json:"hookInfos"`
	Attachment       wireAttachment  `json:"attachment"`
	TotalCostUSD     float64         `json:"totalCostUSD"`
	TotalAPIDuration int64           `json:"totalAPIDuration"`
	TotalToolMS      int64           `json:"totalToolDuration"`
	TotalDuration    int64           `json:"totalDuration"`
	IsMeta           bool            `json:"isMeta"`
	IsCompactSummary bool            `json:"isCompactSummary"`
	Prevented        bool            `json:"preventedContinuation"`
	UnknownModelCost bool            `json:"hasUnknownModelCost"`
}

type wireHookInfo struct {
	DurationMS int64 `json:"durationMs"` //nolint:tagliatelle // the harness's name
}

type wireMessage struct {
	Usage   *wireUsage      `json:"usage"`
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

type wireUsage struct {
	Input         int `json:"input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	Output        int `json:"output_tokens"`
}

// wireAttachment is what the harness notes beside the conversation. The
// ones read here are a hook's outputs.
//
//nolint:tagliatelle // the tags are the harness's own field names
type wireAttachment struct {
	Type          string `json:"type"`
	HookEvent     string `json:"hookEvent"`
	ToolUseID     string `json:"toolUseID"`
	Stderr        string `json:"stderr"`
	BlockingError struct {
		Text string `json:"blockingError"`
	} `json:"blockingError"`
	Content    json.RawMessage `json:"content"`
	DurationMS int64           `json:"durationMs"`
}

// wireBlock is one content block of a message.
type wireBlock struct {
	Input     wireInput       `json:"input"`
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Text      string          `json:"text"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type wireInput struct {
	Command      string `json:"command"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Pattern      string `json:"pattern"`
	Description  string `json:"description"`
	URL          string `json:"url"`
	Query        string `json:"query"`
	Skill        string `json:"skill"`
}

// wirePatch is the diff the harness keeps beside an edit's result.
//
//nolint:tagliatelle // the tags are the harness's own field names
type wirePatch struct {
	FilePath        string `json:"filePath"`
	StructuredPatch []struct {
		Lines []string `json:"lines"`
	} `json:"structuredPatch"`
}

// The attachment types and hook events a timeline reads.
const (
	attachBlocking   = "hook_blocking_error"
	attachSystem     = "hook_system_message"
	attachContext    = "hook_additional_context"
	attachNonBlock   = "hook_non_blocking_error"
	attachSuccess    = "hook_success"
	hookEventStop    = "Stop"
	hookEventPrompt  = "UserPromptSubmit"
	subtypeStopHooks = "stop_hook_summary"
	subtypeCompact   = "compact_boundary"
	typeToolUse      = "tool_use"
	typeToolResult   = "tool_result"
	typeText         = "text"
	releasedMarker   = "so the agent was allowed to stop"
	denialMarker     = "this renames a Go symbol by rewriting text"
	renameCommand    = "wavez rename "
	notificationOpen = "<task-notification>"
	changeSkipped    = "skipped"
	changeRemoved    = "removed"
)

// notPrompts are the prefixes of user messages the harness wrote itself.
var notPrompts = []string{"<command-name>", "<local-command", "<system-reminder>", "<command-message>"}

// kindOrder lists the work kinds weakest first, for picking a turn's
// strongest.
var kindOrder = []Kind{KindProse, KindOther, KindRetrieval, KindShell, KindRefactor, KindSweep, KindEdit}

var (
	summaryTag  = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
	testDecl    = regexp.MustCompile(`^func (Test\w+)\(|^\s*def (test_\w+)\(`)
	skipCall    = regexp.MustCompile(`\bt\.Skip(f|Now)?\(|pytest\.mark\.skip|\b(it|test|describe)\.skip\(`)
	gateLine    = regexp.MustCompile(`(?m)^(?:wavez: )?(FAIL|ADVISORY|NOT CHECKED|NOT COMPARED) ([^\s:]+).*$`)
	alreadyLine = regexp.MustCompile(`(?m)ALREADY FAILING before this session, so it does not block: (([^\s:]+).*)$`)
)

// timelineBuilder carries what a timeline needs across lines.
type timelineBuilder struct {
	lastTime time.Time
	turnOf   map[string]int
	callStep map[string]int
	commands map[int]string
	reads    map[string]int
	rounds   map[string]int
	tl       SessionTimeline
	prompt   int
	detail   bool
}

func newTimelineBuilder() *timelineBuilder {
	return &timelineBuilder{
		turnOf:   map[string]int{},
		callStep: map[string]int{},
		commands: map[int]string{},
		reads:    map[string]int{},
		rounds:   map[string]int{},
	}
}

func (b *timelineBuilder) line(raw []byte) {
	var e wireEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		b.tl.Skipped++

		return
	}

	b.seen(&e)

	switch e.Type {
	case "assistant":
		b.assistant(&e)
	case "user":
		b.user(&e)
	case "attachment":
		b.attachment(&e)
	case "system":
		b.system(&e)
	case "cost-state":
		b.tl.Cost = Cost{
			USD:      e.TotalCostUSD,
			Model:    time.Duration(e.TotalAPIDuration) * time.Millisecond,
			Tools:    time.Duration(e.TotalToolMS) * time.Millisecond,
			Wall:     time.Duration(e.TotalDuration) * time.Millisecond,
			Recorded: true,
			Partial:  e.UnknownModelCost,
		}
	default:
	}
}

// seen notes the facts any line may carry: when it was written, the
// session it belongs to, and where the session ran.
func (b *timelineBuilder) seen(e *wireEntry) {
	b.lastTime = time.Time{}

	if at, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
		b.lastTime = at

		if b.tl.Start.IsZero() || at.Before(b.tl.Start) {
			b.tl.Start = at
		}

		if at.After(b.tl.End) {
			b.tl.End = at
		}
	}

	b.tl.SessionID = cmp.Or(b.tl.SessionID, e.SessionID)
	b.tl.Cwd = cmp.Or(b.tl.Cwd, e.Cwd)
}

func (b *timelineBuilder) push(s Step) int {
	s.Index = len(b.tl.Steps)
	s.Time = b.lastTime
	s.Prompt = b.prompt
	s.Label = clipLabel(s.Label)

	if !b.detail {
		s.Detail = ""
	} else if len(s.Detail) > detailLimit {
		s.Detail = s.Detail[:detailLimit]
	}

	b.tl.Steps = append(b.tl.Steps, s)

	return s.Index
}

// assistant records one line of an assistant message. A message arrives as
// several lines sharing one id, so the first line opens the turn with a
// prose step and the first tool call, if one comes, takes that step over.
func (b *timelineBuilder) assistant(e *wireEntry) {
	id := e.Message.ID

	turn, ok := b.turnOf[id]
	if !ok {
		turn = len(b.tl.Turns)
		b.turnOf[id] = turn
		first := b.push(Step{Kind: KindProse, Label: "reply without a tool call", Turn: turn + 1})
		b.tl.Turns = append(b.tl.Turns, Turn{Kind: KindProse, First: first, Model: e.Message.Model})

		if m := e.Message.Model; m != "" && !strings.HasPrefix(m, "<") && !slices.Contains(b.tl.Models, m) {
			b.tl.Models = append(b.tl.Models, m)
		}
	}

	t := &b.tl.Turns[turn]

	if u := e.Message.Usage; u != nil {
		t.Usage = Usage{Input: u.Input, CacheRead: u.CacheRead, CacheCreation: u.CacheCreation, Output: u.Output}
		t.HasUsage = true
	}

	blocks := wireBlocks(e.Message.Content)
	for i := range blocks {
		if blocks[i].Type == typeToolUse {
			b.call(t, turn, &blocks[i])
		}
	}
}

func (b *timelineBuilder) call(t *Turn, turn int, blk *wireBlock) {
	step := b.toolStep(blk)
	step.Turn = turn + 1

	idx := t.First
	if b.tl.Steps[idx].Kind == KindProse {
		step.Index, step.Time, step.Prompt = idx, b.tl.Steps[idx].Time, b.tl.Steps[idx].Prompt
		step.Label = clipLabel(step.Label)

		if !b.detail {
			step.Detail = ""
		}

		b.tl.Steps[idx] = step
	} else {
		idx = b.push(step)
	}

	b.callStep[blk.ID] = idx

	if slices.Index(kindOrder, step.Kind) > slices.Index(kindOrder, t.Kind) {
		t.Kind = step.Kind
	}

	b.derive(idx, blk)
}

// toolStep names one tool call and says what kind of step it is.
func (b *timelineBuilder) toolStep(blk *wireBlock) Step {
	in := &blk.Input
	path := cmp.Or(in.FilePath, in.NotebookPath)
	step := Step{Tool: blk.Name, Kind: KindOther, Label: blk.Name}

	switch classify(blk.Name, in.Command) {
	case ClassRetrieval:
		step.Kind = KindRetrieval
	case ClassEdit:
		step.Kind = KindEdit
	case ClassShell:
		step.Kind = KindShell
	case ClassOther, ClassProse:
	}

	if blk.Name == shellTool {
		switch {
		case len(sweeps(in.Command)) > 0:
			step.Kind = KindSweep
		case len(refactorCalls(in.Command)) > 0:
			step.Kind = KindRefactor
		}

		step.Detail = in.Command
	}

	if path != "" {
		step.Path = b.relative(path)
	}

	arg := cmp.Or(step.Path, firstLine(in.Command), in.Pattern, in.Description, in.URL, in.Query, in.Skill)
	if arg != "" {
		step.Label = blk.Name + " " + arg
	}

	return step
}

// derive counts what a tool call adds to the session's totals.
func (b *timelineBuilder) derive(idx int, blk *wireBlock) {
	step := &b.tl.Steps[idx]

	switch {
	case step.Kind == KindEdit:
		b.tl.Edits++

		if step.Path != "" && !slices.Contains(b.tl.Edited, step.Path) {
			b.tl.Edited = append(b.tl.Edited, step.Path)
		}
	case step.Kind == KindRetrieval && step.Path != "":
		b.reads[step.Path]++
	case blk.Name == shellTool:
		b.commands[idx] = blk.Input.Command

		for _, label := range sweeps(blk.Input.Command) {
			b.tl.Sweeps = append(b.tl.Sweeps, Sweep{Command: label, Step: idx})
		}
	}
}

// relative shortens a path under the session's directory to the part
// below it.
func (b *timelineBuilder) relative(path string) string {
	if b.tl.Cwd == "" {
		return path
	}

	rel, err := filepath.Rel(b.tl.Cwd, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}

	return rel
}

func (b *timelineBuilder) user(e *wireEntry) {
	if text, ok := promptText(e); ok {
		b.promptStep(text)

		return
	}

	blocks := wireBlocks(e.Message.Content)
	for i := range blocks {
		if blocks[i].Type != typeToolResult {
			continue
		}

		if idx, ok := b.callStep[blocks[i].ToolUseID]; ok {
			b.result(idx, &blocks[i], e.ToolUseResult)
		}
	}
}

// promptText returns what a person, or the harness in a person's place,
// asked for, and false for a line that is a tool result or the harness's
// own bookkeeping.
func promptText(e *wireEntry) (string, bool) {
	if e.IsMeta || e.IsCompactSummary {
		return "", false
	}

	var text string
	if err := json.Unmarshal(e.Message.Content, &text); err != nil {
		blocks := wireBlocks(e.Message.Content)
		parts := make([]string, 0, len(blocks))

		for i := range blocks {
			if blocks[i].Type != typeText {
				return "", false
			}

			parts = append(parts, blocks[i].Text)
		}

		text = strings.Join(parts, "\n")
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}

	for _, prefix := range notPrompts {
		if strings.HasPrefix(text, prefix) {
			return "", false
		}
	}

	return text, true
}

func (b *timelineBuilder) promptStep(text string) {
	label := firstLine(text)

	if strings.HasPrefix(text, notificationOpen) {
		label = "notification"
		if m := summaryTag.FindStringSubmatch(text); m != nil {
			label += ": " + strings.TrimSpace(m[1])
		}
	}

	b.prompt++
	idx := b.push(Step{Kind: KindPrompt, Label: label, Detail: text})
	b.tl.Segments = append(b.tl.Segments, Segment{Prompt: b.prompt, First: idx, Label: b.tl.Steps[idx].Label})
}

// result closes a tool call: how long it took, whether it failed, and what
// its diff says about the tests.
func (b *timelineBuilder) result(idx int, blk *wireBlock, extra json.RawMessage) {
	step := &b.tl.Steps[idx]

	if !b.lastTime.IsZero() && !step.Time.IsZero() && b.lastTime.After(step.Time) {
		step.Duration = b.lastTime.Sub(step.Time)
	}

	step.Failed = blk.IsError

	if text := resultText(blk.Content); blk.IsError && strings.Contains(text, denialMarker) {
		step.Denied = true
		denial := Denial{Step: idx}

		for line := range strings.SplitSeq(text, "\n") {
			if line = strings.TrimSpace(line); strings.HasPrefix(line, renameCommand) {
				denial.Suggested = append(denial.Suggested, line)
			}
		}

		b.tl.Denials = append(b.tl.Denials, denial)
	}

	if step.Kind == KindEdit {
		b.testChanges(idx, extra)
	}
}

// testChanges reads an edit's diff for a test taken out of play: a line
// that skips one added, or a test's declaration removed and not put back.
func (b *timelineBuilder) testChanges(idx int, extra json.RawMessage) {
	var patch wirePatch
	if err := json.Unmarshal(extra, &patch); err != nil {
		return
	}

	var added, removed []string

	for _, hunk := range patch.StructuredPatch {
		for _, line := range hunk.Lines {
			switch {
			case strings.HasPrefix(line, "+"):
				added = append(added, line[1:])
			case strings.HasPrefix(line, "-"):
				removed = append(removed, line[1:])
			}
		}
	}

	path := b.tl.Steps[idx].Path

	for _, line := range added {
		if skipCall.MatchString(line) && !slices.Contains(removed, line) {
			change := TestChange{Path: path, Name: clipLabel(strings.TrimSpace(line)), What: changeSkipped, Step: idx}
			b.tl.TestChanges = append(b.tl.TestChanges, change)
		}
	}

	for _, line := range removed {
		name := declaredTest(line)
		if name == "" || slices.ContainsFunc(added, func(a string) bool { return declaredTest(a) == name }) {
			continue
		}

		b.tl.TestChanges = append(b.tl.TestChanges, TestChange{Path: path, Name: name, What: changeRemoved, Step: idx})
	}
}

func declaredTest(line string) string {
	m := testDecl.FindStringSubmatch(line)
	if m == nil {
		return ""
	}

	return cmp.Or(m[1], m[2])
}

func (b *timelineBuilder) attachment(e *wireEntry) {
	a := &e.Attachment
	if a.HookEvent == "" {
		return
	}

	agent := ""

	switch a.Type {
	case attachBlocking:
		agent = a.BlockingError.Text
	case attachContext:
		agent = strings.Join(contentStrings(a.Content), "\n")
	default:
	}

	if agent != "" {
		b.addHookBytes(a.HookEvent, len(agent))

		if a.HookEvent == hookEventPrompt {
			b.tl.RouteHints++
		}
	}

	if a.HookEvent != hookEventStop {
		return
	}

	round := b.round(a.ToolUseID)

	switch a.Type {
	case attachBlocking:
		round.Outcome = GateBlock
		b.told(round, agent, "")
	case attachContext:
		b.told(round, agent, "")
	case attachSystem:
		b.told(round, "", strings.Join(contentStrings(a.Content), "\n"))
	case attachNonBlock:
		round.Outcome = GateError
		b.told(round, "", a.Stderr)
	case attachSuccess:
		round.Duration = max(round.Duration, time.Duration(a.DurationMS)*time.Millisecond)
	default:
	}
}

// round returns the Stop hook run an id names, opening it and its step
// the first time the id is seen.
func (b *timelineBuilder) round(id string) *GateRound {
	n, ok := b.rounds[id]
	if !ok || id == "" {
		n = len(b.tl.Gates)
		b.rounds[id] = n
		idx := b.push(Step{Kind: KindHook})
		b.tl.Gates = append(b.tl.Gates, GateRound{Outcome: GatePass, Step: idx, Time: b.lastTime})
	}

	return &b.tl.Gates[n]
}

func (b *timelineBuilder) told(round *GateRound, agent, person string) {
	round.AgentBytes += len(agent)
	round.PersonBytes += len(person)
	round.Headline = cmp.Or(round.Headline, clipLabel(firstLine(cmp.Or(agent, person))))

	if b.detail {
		round.Agent += agent
		round.Person += person
	}

	for _, text := range []string{agent, person} {
		round.Checks = append(round.Checks, gateChecks(text)...)
	}

	if round.Outcome == GatePass && agent+person != "" {
		round.Outcome = GateNote
	}

	if strings.Contains(person, releasedMarker) {
		round.Outcome = GateReleased
	}
}

// gateChecks reads the checks a gate's text names from its headers.
func gateChecks(text string) []GateCheck {
	named := gateLine.FindAllStringSubmatch(text, -1)
	out := make([]GateCheck, 0, len(named))

	for _, m := range named {
		out = append(out, GateCheck{Name: m[2], Status: strings.ToLower(m[1]), Line: clipLabel(m[0])})
	}

	for _, m := range alreadyLine.FindAllStringSubmatch(text, -1) {
		out = append(out, GateCheck{Name: m[2], Status: "already failing", Line: clipLabel(m[1])})
	}

	return out
}

func (b *timelineBuilder) system(e *wireEntry) {
	switch e.Subtype {
	case subtypeCompact:
		b.tl.Compactions++
		b.push(Step{Kind: KindCompact, Label: "conversation compacted"})
	case subtypeStopHooks:
		round := b.round(e.ToolUseID)

		for _, info := range e.HookInfos {
			round.Duration = max(round.Duration, time.Duration(info.DurationMS)*time.Millisecond)
		}

		if round.AgentBytes == 0 && len(e.HookErrors) > 0 {
			b.told(round, strings.Join(e.HookErrors, "\n"), "")
			b.addHookBytes(hookEventStop, round.AgentBytes)
		}

		if (e.Prevented || len(e.HookErrors) > 0) && round.Outcome != GateReleased {
			round.Outcome = GateBlock
		}
	default:
	}
}

func (b *timelineBuilder) addHookBytes(event string, n int) {
	if b.tl.HookBytes == nil {
		b.tl.HookBytes = map[string]int{}
	}

	b.tl.HookBytes[event] += n
}

func wireBlocks(content json.RawMessage) []wireBlock {
	var out []wireBlock
	if err := json.Unmarshal(content, &out); err != nil {
		return nil
	}

	return out
}

// contentStrings decodes a field that is a string in some lines and a list
// of strings in others.
func contentStrings(raw json.RawMessage) []string {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}
	}

	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return nil
	}

	return many
}

// resultText flattens a tool result, which is a string or a list of text
// blocks.
func resultText(raw json.RawMessage) string {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return one
	}

	blocks := wireBlocks(raw)
	parts := make([]string, 0, len(blocks))

	for i := range blocks {
		parts = append(parts, blocks[i].Text)
	}

	return strings.Join(parts, "\n")
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")

	return strings.Join(strings.Fields(line), " ")
}

func clipLabel(text string) string {
	runes := []rune(text)
	if len(runes) <= labelLimit {
		return text
	}

	return string(runes[:labelLimit-1]) + "…"
}
