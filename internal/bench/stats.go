// Package bench turns a thread log into the counts an efficiency change is
// judged by. DESIGN.md's Standing objectives require a before and after from
// the same task for any change to a tool or a gate, and reconstructing those
// by hand from a transcript is what this replaces.
package bench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/event"
)

const (
	readTool   = "read"
	searchTool = "search"
	// A run whose searches keep missing falls back to shell grep, which is
	// what made an empty result worth counting. This is the prefix the search
	// tool reports one with.
	noMatchPrefix = "no matches"
)

// ToolStat is one tool's share of a run.
type ToolStat struct {
	Name        string `json:"name"`
	Calls       int    `json:"calls"`
	ResultBytes int    `json:"result_bytes"`
}

// Stats is what one run spent. Token counts come from the provider's own
// usage rather than an estimate, so they are absent for a provider that
// reports none rather than zero. The json tags are the field names a diff
// script reads on the wire.
type Stats struct {
	TierTurns        map[string]int `json:"tier_turns"`
	ThreadID         string         `json:"thread_id"`
	Tools            []ToolStat     `json:"tools"`
	Elapsed          time.Duration  `json:"elapsed"`
	Turns            int            `json:"turns"`
	ToolCalls        int            `json:"tool_calls"`
	InputTokens      int            `json:"input_tokens"`
	OutputTokens     int            `json:"output_tokens"`
	CacheReadTokens  int            `json:"cache_read_tokens"`
	RepeatReads      int            `json:"repeat_reads"`
	RepeatReadBytes  int            `json:"repeat_read_bytes"`
	EmptySearches    int            `json:"empty_searches"`
	GateRounds       int            `json:"gate_rounds"`
	GateFailures     int            `json:"gate_failures"`
	ReviewObjections int            `json:"review_objections"`
	CompactionSaved  int            `json:"compaction_saved"`
}

// Read decodes a thread log written by internal/eventlog.
func Read(path string) ([]event.Event, error) {
	f, err := os.Open(path) //nolint:gosec // path names a log the caller chose
	if err != nil {
		return nil, fmt.Errorf("opening thread log %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // read-only handle

	var out []event.Event

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxLogLine)

	for sc.Scan() {
		var ev event.Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return nil, fmt.Errorf("parsing thread log %s: %w", path, err)
		}

		out = append(out, ev)
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading thread log %s: %w", path, err)
	}

	return out, nil
}

// maxLogLine bounds one log line, which holds a whole tool result.
const maxLogLine = 8 << 20

// Summarize reduces a thread's events to what the run cost.
func Summarize(evs []event.Event) Stats {
	s := Stats{TierTurns: map[string]int{}}
	tools := map[string]*ToolStat{}
	tracker := newReadTracker()

	for i := range evs {
		ev := &evs[i]
		if s.ThreadID == "" {
			s.ThreadID = ev.ThreadID
		}

		switch ev.Kind {
		case event.KindAgent:
			s.countTurn(ev)
		case event.KindTool:
			s.countTool(ev, tools, tracker)
		case event.KindGate:
			s.countGate(ev)
		case event.KindReview:
			s.countReview(ev)
		case event.KindUsage:
			s.CompactionSaved += intField(ev.Detail, "tokens_saved")
		default:
		}
	}

	s.Tools = rankTools(tools)
	s.Elapsed = span(evs)

	return s
}

// countTurn reads the marker AppendAssistant writes at the end of each
// assistant turn, which is the only event carrying that turn's usage.
func (s *Stats) countTurn(ev *event.Event) {
	if ev.Role == "" {
		return
	}

	s.Turns++

	if tier := stringField(ev.Detail, "tier"); tier != "" {
		s.TierTurns[tier]++
	}

	usage, ok := ev.Detail["usage"].(map[string]any)
	if !ok {
		return
	}

	s.InputTokens += intField(usage, "input_tokens")
	s.OutputTokens += intField(usage, "output_tokens")
	s.CacheReadTokens += intField(usage, "cache_read_tokens")
}

func (s *Stats) countTool(ev *event.Event, tools map[string]*ToolStat, tracker *readTracker) {
	s.ToolCalls++

	stat, ok := tools[ev.Tool]
	if !ok {
		stat = &ToolStat{Name: ev.Tool}
		tools[ev.Tool] = stat
	}

	stat.Calls++
	stat.ResultBytes += len(ev.Text)

	if len(ev.Changes) > 0 {
		tracker.edited(ev.Changes)
	}

	if ev.Tool == searchTool && strings.HasPrefix(ev.Text, noMatchPrefix) {
		s.EmptySearches++
	}

	if ev.Tool == readTool && tracker.repeat(inputPath(ev.Detail)) {
		s.RepeatReads++
		s.RepeatReadBytes += len(ev.Text)
	}
}

func (s *Stats) countGate(ev *event.Event) {
	s.GateRounds++

	if pass, ok := ev.Detail["pass"].(bool); ok && !pass {
		s.GateFailures++
	}
}

func (s *Stats) countReview(ev *event.Event) {
	if stringField(ev.Detail, "result") == "objection" {
		s.ReviewObjections++
	}
}

func rankTools(tools map[string]*ToolStat) []ToolStat {
	out := make([]ToolStat, 0, len(tools))
	for _, t := range tools {
		out = append(out, *t)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}

		return out[i].Name < out[j].Name
	})

	return out
}

func span(evs []event.Event) time.Duration {
	if len(evs) < 2 { //nolint:mnd // one event spans no time
		return 0
	}

	return evs[len(evs)-1].At.Sub(evs[0].At)
}
