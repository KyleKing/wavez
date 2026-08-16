// Package extract derives quiz-worthy decision points from a session using
// only cheap, deterministic heuristics: no LLM calls, no network access. An
// LLM-based fallback for decisions where no rationale can be found belongs
// in a separate package.
package extract

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kyleking/what-did-ai-do/internal/decision"
	"github.com/kyleking/what-did-ai-do/internal/session"
)

// trivialRationaleThreshold is the minimum trimmed length an assistant
// message's text must have to count as real rationale rather than filler
// like "Let me check that." Chosen to be long enough to skip short
// acknowledgements while still catching a one-sentence explanation.
const trivialRationaleThreshold = 40

// summaryInputTruncateLen bounds how much of a ToolCall's Input is echoed
// into a no-file decision's Summary, so summaries stay skimmable.
const summaryInputTruncateLen = 60

// group is one decision point's worth of ToolCalls: either every
// consecutive ToolCall touching the exact same single file set, or a lone
// ToolCall with no discernible file target.
type group struct {
	toolCalls []session.ToolCall
	files     []string
}

// Extract derives quiz-worthy Decisions from a session using only
// structural/textual heuristics (no LLM calls). Rationale is filled in from
// nearby assistant prose when present; Source is set accordingly.
//
//nolint:gocritic // signature fixed by package contract: Session -> []Decision
func Extract(s session.Session) []decision.Decision {
	groups := groupToolCalls(s.ToolCalls)

	decisions := make([]decision.Decision, 0, len(groups))
	for i, g := range groups {
		decisions = append(decisions, buildDecision(&s, g, i))
	}

	return decisions
}

func groupToolCalls(calls []session.ToolCall) []group {
	groups := make([]group, 0, len(calls))
	for _, tc := range calls {
		key := fileSetKey(tc.Files)
		if key != "" && len(groups) > 0 {
			last := &groups[len(groups)-1]
			if fileSetKey(last.files) == key {
				last.toolCalls = append(last.toolCalls, tc)
				continue
			}
		}
		groups = append(groups, group{
			toolCalls: []session.ToolCall{tc},
			files:     tc.Files,
		})
	}

	return groups
}

// fileSetKey returns a canonical key for a ToolCall's file set so
// consecutive calls touching the exact same files can be merged. Only
// single-file sets are merged per the MVP grouping rule (exact match, no
// fuzzy similarity); a multi-file or empty set never merges with its
// neighbor.
func fileSetKey(files []string) string {
	if len(files) != 1 {
		return ""
	}

	return files[0]
}

func buildDecision(s *session.Session, g group, index int) decision.Decision {
	rationale, source := findRationale(s.Messages, g.toolCalls)

	return decision.Decision{
		ID:        fmt.Sprintf("%s-%03d", s.ID, index),
		SessionID: s.ID,
		Summary:   summarize(g),
		Rationale: rationale,
		Files:     g.files,
		ToolNames: toolNames(g.toolCalls),
		Source:    source,
	}
}

func toolNames(calls []session.ToolCall) []string {
	seen := make(map[string]bool, len(calls))
	names := make([]string, 0, len(calls))
	for _, tc := range calls {
		if seen[tc.Name] {
			continue
		}
		seen[tc.Name] = true
		names = append(names, tc.Name)
	}

	return names
}

func summarize(g group) string {
	if len(g.files) == 0 {
		tc := g.toolCalls[0]
		return fmt.Sprintf(
			"ran %s: %s",
			tc.Name,
			truncate(readableInput(tc.Input), summaryInputTruncateLen),
		)
	}

	verb := summaryVerb(g.toolCalls[len(g.toolCalls)-1].Name)
	if len(g.files) == 1 {
		return fmt.Sprintf("%s %s", verb, g.files[0])
	}

	sorted := append([]string(nil), g.files...)
	sort.Strings(sorted)

	return fmt.Sprintf("%s files: %s", verb, strings.Join(sorted, ", "))
}

func summaryVerb(toolName string) string {
	lower := strings.ToLower(toolName)
	switch {
	case strings.Contains(lower, "write"):
		return "wrote"
	case strings.Contains(lower, "edit"):
		return "edited"
	case strings.Contains(lower, "read"):
		return "read"
	default:
		return "touched"
	}
}

// commandInputKeys are the tool_use input fields, across common tools with
// no file target (Bash, WebSearch, Grep, ...), that hold the human-readable
// part of an otherwise JSON-encoded Input.
var commandInputKeys = []string{"command", "pattern", "query", "prompt", "description"}

// readableInput extracts a human-readable string from a ToolCall's raw
// Input for summaries. Input is JSON for Claude Code tool_use calls but
// plain text for other adapters (e.g. Aider's rendered diff); non-JSON
// input is returned unchanged.
func readableInput(input string) string {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "{") {
		return input
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
		return input
	}

	for _, key := range commandInputKeys {
		if v, ok := fields[key].(string); ok && v != "" {
			return v
		}
	}

	return input
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n] + "..."
}

// findRationale looks for adjacent assistant prose to explain a group of
// ToolCalls: the nearest assistant message before the first ToolCall, and
// the nearest one after the last, preferring "before" since an agent
// typically states its plan before acting.
func findRationale(messages []session.Message, calls []session.ToolCall) (string, decision.Source) {
	start := calls[0].At
	end := calls[len(calls)-1].At

	before := nearestAssistantBefore(messages, start)
	if isSubstantial(before) {
		return strings.TrimSpace(before), decision.SourceTranscript
	}

	after := nearestAssistantAfter(messages, end)
	if isSubstantial(after) {
		return strings.TrimSpace(after), decision.SourceTranscript
	}

	return "", decision.SourceStructural
}

func nearestAssistantBefore(messages []session.Message, at time.Time) string {
	var best string
	var bestAt time.Time
	found := false
	for _, m := range messages {
		if m.Role != "assistant" || m.At.After(at) {
			continue
		}
		if !found || m.At.After(bestAt) {
			best, bestAt, found = m.Text, m.At, true
		}
	}

	return best
}

func nearestAssistantAfter(messages []session.Message, at time.Time) string {
	var best string
	var bestAt time.Time
	found := false
	for _, m := range messages {
		if m.Role != "assistant" || m.At.Before(at) {
			continue
		}
		if !found || m.At.Before(bestAt) {
			best, bestAt, found = m.Text, m.At, true
		}
	}

	return best
}

func isSubstantial(text string) bool {
	return len(strings.TrimSpace(text)) > trivialRationaleThreshold
}
