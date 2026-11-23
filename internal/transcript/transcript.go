package transcript

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// NotFoundError says no transcript under Root is named for the session.
type NotFoundError struct {
	Root    string
	Session string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("no transcript for session %s under %s", e.Session, e.Root)
}

// extension is the suffix of a transcript file.
const extension = ".jsonl"

// Find returns the transcript of a session under root, the directory that
// holds one folder per project (`~/.claude/projects`). It searches every
// project folder rather than rebuilding the folder name from a working
// directory, because that encoding is the harness's to change.
func Find(root, session string) (string, error) {
	if session == "" || strings.ContainsAny(session, `/\*?[`) {
		return "", &NotFoundError{Root: root, Session: session}
	}

	matches, err := filepath.Glob(filepath.Join(root, "*", session+extension))
	if err != nil {
		return "", fmt.Errorf("searching %s: %w", root, err)
	}

	if len(matches) == 0 {
		return "", &NotFoundError{Root: root, Session: session}
	}

	return matches[0], nil
}

// ReadSession attributes the transcript at path together with the
// transcripts of the subagents it launched, which the harness keeps in a
// `subagents` folder named for the session.
func ReadSession(path string) (Stats, error) {
	stats, err := readFile(path)
	if err != nil {
		return Stats{}, err
	}

	pattern := filepath.Join(strings.TrimSuffix(path, extension), "subagents", "*"+extension)

	subs, err := filepath.Glob(pattern)
	if err != nil {
		return Stats{}, fmt.Errorf("searching %s: %w", pattern, err)
	}

	for _, sub := range subs {
		s, err := readFile(sub)
		if err != nil {
			return Stats{}, err
		}

		stats.add(&s)
		stats.Subagents++
	}

	return stats, nil
}

func readFile(path string) (Stats, error) {
	f, err := os.Open(path) //nolint:gosec // the caller names the transcript to read
	if err != nil {
		return Stats{}, fmt.Errorf("opening transcript: %w", err)
	}

	stats, err := Read(f)

	return stats, errors.Join(err, f.Close())
}

// entry is one transcript line, reduced to the fields attribution reads.
type entry struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Message struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	HookErrors            []string `json:"hookErrors"`            //nolint:tagliatelle // the harness's name
	PreventedContinuation bool     `json:"preventedContinuation"` //nolint:tagliatelle // the harness's name
}

// block is one content block of a message: a tool call from the assistant
// or a tool result from the user side.
type block struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
	Input     struct {
		Command      string `json:"command"`
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"input"`
	IsError bool `json:"is_error"`
}

// Read attributes one transcript. A line that is not JSON is counted and
// skipped, since a session cut off mid-write ends in half a line and the
// rest of it is still worth reading.
func Read(r io.Reader) (Stats, error) {
	acc := newAccumulator()
	br := bufio.NewReader(r)

	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			acc.line(line)
		}

		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return Stats{}, fmt.Errorf("reading transcript: %w", err)
		}
	}

	return acc.finish(), nil
}

// accumulator carries what attribution needs across lines: each turn's
// strongest class, which tool each call id belongs to, and when each file
// was last read and edited.
type accumulator struct {
	turns    map[string]Class
	callTool map[string]string
	lastRead map[string]int
	lastEdit map[string]int
	order    []string
	stats    Stats
	step     int
	// lastWrite is the position in order of the turn that last wrote a
	// file, and wrote says whether any turn did.
	lastWrite int
	wrote     bool
}

func newAccumulator() *accumulator {
	return &accumulator{
		turns:    map[string]Class{},
		callTool: map[string]string{},
		lastRead: map[string]int{},
		lastEdit: map[string]int{},
	}
}

func (a *accumulator) line(raw []byte) {
	var e entry
	if err := json.Unmarshal(raw, &e); err != nil {
		a.stats.Skipped++

		return
	}

	switch e.Type {
	case "assistant":
		a.assistant(&e)
	case "user":
		a.results(blocks(e.Message.Content))
	case "system":
		if e.Subtype == "stop_hook_summary" && (e.PreventedContinuation || len(e.HookErrors) > 0) {
			a.stats.StopBlocks++
		}
	default:
	}
}

// blocks decodes a message's content, which is a plain string for a typed
// prompt and a list of blocks otherwise.
func blocks(content json.RawMessage) []block {
	var out []block
	if err := json.Unmarshal(content, &out); err != nil {
		return nil
	}

	return out
}

// assistant records one line of an assistant message. The harness writes a
// message as several lines sharing one id, one content block each, so the
// turn is keyed by that id and takes the strongest class any line showed.
func (a *accumulator) assistant(e *entry) {
	id := e.Message.ID
	if _, seen := a.turns[id]; !seen {
		a.turns[id] = ClassProse
		a.order = append(a.order, id)
	}

	for _, b := range blocks(e.Message.Content) {
		if b.Type != "tool_use" {
			continue
		}

		class := a.call(&b)
		if stronger(class, a.turns[id]) {
			a.turns[id] = class
		}

		if writesFile(&b, class) {
			a.lastWrite, a.wrote = slices.Index(a.order, id), true
		}
	}
}

func (a *accumulator) call(b *block) Class {
	a.callTool[b.ID] = b.Name
	a.stats.ToolCalls = bump(a.stats.ToolCalls, b.Name, 1)
	a.step++

	class := classify(b.Name, b.Input.Command)
	path := cmp.Or(b.Input.FilePath, b.Input.NotebookPath)

	switch {
	case class == ClassEdit && path != "":
		a.lastEdit[path] = a.step
	case b.Name == "Read" && path != "":
		prev, seen := a.lastRead[path]
		if seen && a.lastEdit[path] < prev {
			a.stats.RepeatReads++
		}

		a.lastRead[path] = a.step
	case b.Name == shellTool:
		a.shell(b.Input.Command)
	}

	return class
}

func (a *accumulator) shell(command string) {
	for _, label := range sweeps(command) {
		a.stats.Sweeps++
		a.stats.SweepCommands = bump(a.stats.SweepCommands, label, 1)
	}

	for _, verb := range refactorCalls(command) {
		a.stats.Refactors = bump(a.stats.Refactors, verb, 1)
	}
}

func (a *accumulator) results(bs []block) {
	for _, b := range bs {
		if b.Type != "tool_result" || !b.IsError {
			continue
		}

		name, ok := a.callTool[b.ToolUseID]
		if !ok {
			name = "unknown"
		}

		a.stats.ToolErrors = bump(a.stats.ToolErrors, name, 1)
	}
}

// writesFile reports whether a tool call changed a file: an edit tool, or
// a shell command that redirects into one or runs a name-based refactor.
// Other shell commands may write too (`sed -i`, a generator), and are not
// counted, because the command alone does not say so.
func writesFile(b *block, class Class) bool {
	if class == ClassEdit {
		return true
	}

	if b.Name != shellTool {
		return false
	}

	return len(refactorCalls(b.Input.Command)) > 0 ||
		slices.ContainsFunc(splitCommands(b.Input.Command), func(c simpleCommand) bool { return c.writes })
}

func (a *accumulator) finish() Stats {
	a.stats.Turns = len(a.order)

	if a.wrote {
		a.stats.TurnsAfterLastEdit = len(a.order) - 1 - a.lastWrite
	}

	for _, id := range a.order {
		a.count(a.turns[id])
	}

	return a.stats
}

func (a *accumulator) count(class Class) {
	switch class {
	case ClassRetrieval:
		a.stats.Retrieval++
	case ClassEdit:
		a.stats.Edit++
	case ClassShell:
		a.stats.Shell++
	case ClassOther:
		a.stats.Other++
	case ClassProse:
		a.stats.Prose++
	}
}
