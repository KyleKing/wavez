package thread

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/kyleking/wavez/internal/llm"
)

const (
	historyMaxLine     = 8 << 20
	historyInitialLine = 64 << 10
	historyFilePerm    = 0o600
)

// historyRecord is one model-visible message as it is stored on disk. A
// tool call's arguments are held as a string rather than as raw JSON,
// because the arguments a model emitted are not always JSON and the turn
// that stops a run for exactly that reason still has to be written down.
type historyRecord struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []historyCall `json:"tool_calls,omitempty"`
	Turn       int           `json:"turn"`
	IsError    bool          `json:"is_error,omitempty"`
}

type historyCall struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input string `json:"input"`
}

func newHistoryRecord(entry TurnMessage) historyRecord {
	rec := historyRecord{
		Role:       string(entry.Message.Role),
		Content:    entry.Message.Content,
		ToolCallID: entry.Message.ToolCallID,
		IsError:    entry.Message.IsError,
		Turn:       entry.Turn,
	}

	for _, c := range entry.Message.ToolCalls {
		rec.ToolCalls = append(rec.ToolCalls, historyCall{ID: c.ID, Name: c.Name, Input: string(c.Input)})
	}

	return rec
}

func (r historyRecord) entry() TurnMessage {
	msg := llm.Message{
		Role:       llm.Role(r.Role),
		Content:    r.Content,
		ToolCallID: r.ToolCallID,
		IsError:    r.IsError,
	}

	for _, c := range r.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{ID: c.ID, Name: c.Name, Input: json.RawMessage(c.Input)})
	}

	return TurnMessage{Message: msg, Turn: r.Turn}
}

// history is the thread's model-visible transcript, kept beside the event
// log because the log cannot reconstruct it: tool inputs are truncated
// there and assistant text arrives as streamed chunks. Without it a resumed
// thread hands the model an empty transcript, so a run stopped on a bound
// keeps the files it wrote and loses everything it knew about them.
//
// Every append is flushed, because the point of the file is the run that
// did not end the way it meant to.
type history struct {
	f    *os.File
	path string
	mu   sync.Mutex
}

// openHistory opens the sidecar for id under dir and returns the entries it
// already holds.
func openHistory(dir, id string) (*history, []TurnMessage, error) {
	path := filepath.Join(dir, id+".history.jsonl")

	entries, err := readHistory(path)
	if err != nil {
		return nil, nil, err
	}

	//nolint:gosec // caller-owned log dir
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, historyFilePerm)
	if err != nil {
		return nil, nil, fmt.Errorf("opening thread history: %w", err)
	}

	return &history{f: f, path: path}, entries, nil
}

func readHistory(path string) ([]TurnMessage, error) {
	f, err := os.Open(path) //nolint:gosec // caller-owned log dir
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("reading thread history: %w", err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // read-only handle

	var out []TurnMessage

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, historyInitialLine), historyMaxLine)

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}

		var rec historyRecord
		// A truncated tail is what a killed process leaves, and the
		// messages before it are still the transcript worth resuming.
		if err := json.Unmarshal(line, &rec); err != nil {
			break
		}

		out = append(out, rec.entry())
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading thread history %s: %w", path, err)
	}

	return out, nil
}

func (h *history) append(entry TurnMessage) error {
	if h == nil {
		return nil
	}

	line, err := json.Marshal(newHistoryRecord(entry))
	if err != nil {
		return fmt.Errorf("encoding history message: %w", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, err := h.f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("writing thread history: %w", err)
	}

	return nil
}

func (h *history) Close() error {
	if h == nil {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.f.Close(); err != nil {
		return fmt.Errorf("closing thread history: %w", err)
	}

	return nil
}

// lastTurn is the turn a resumed thread continues from, so a new turn never
// reuses a number an entry already carries.
func lastTurn(entries []TurnMessage) int {
	turn := 0
	for _, e := range entries {
		if e.Turn > turn {
			turn = e.Turn
		}
	}

	return turn
}
