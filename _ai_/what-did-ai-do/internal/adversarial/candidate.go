// Package adversarial flags AI decisions that look like low-quality "AI
// slop" — hasty, unjustified, or clearly suboptimal changes — cross-checked
// against whether the flagged code is even still present in the working
// tree (a decision superseded by later work isn't worth flagging).
package adversarial

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kyleking/what-did-ai-do/internal/decision"
	"github.com/kyleking/what-did-ai-do/internal/extract"
	"github.com/kyleking/what-did-ai-do/internal/session"
)

// maxDiffLen bounds how much of a ToolCall's diff content is retained per
// candidate, since it's headed into an LLM prompt alongside every other
// candidate in the same batched call.
const maxDiffLen = 2000

// Candidate pairs a Decision with the ToolCalls it was derived from, since
// decision.Decision itself carries no diff content — only extract's
// internal grouping does, and that's not part of its exported API.
type Candidate struct {
	Decision  decision.Decision
	ToolCalls []session.ToolCall
}

// CandidatesFrom re-derives extract's decision grouping over s, but keeps
// the originating ToolCalls attached. This duplicates extract's grouping
// rule (single-file exact-match merge) rather than exporting it from
// extract, which is an intentional, documented tradeoff: extract.Extract
// remains the single source of truth for Decision content and IDs, and
// groupToolCalls here MUST stay identical to extract's so the two stay
// index-aligned. If extract's grouping rule ever changes, this must change
// with it.
func CandidatesFrom(s *session.Session) []Candidate {
	decisions := extract.Extract(*s)
	groups := groupToolCalls(s.ToolCalls)

	candidates := make([]Candidate, 0, len(decisions))

	for i := range decisions {
		var calls []session.ToolCall
		if i < len(groups) {
			calls = groups[i]
		}

		candidates = append(candidates, Candidate{Decision: decisions[i], ToolCalls: calls})
	}

	return candidates
}

func groupToolCalls(calls []session.ToolCall) [][]session.ToolCall {
	groups := make([][]session.ToolCall, 0, len(calls))

	for i := range calls {
		tc := calls[i]

		key := fileSetKey(tc.Files)
		if key != "" && len(groups) > 0 {
			last := groups[len(groups)-1]
			if len(last) > 0 && fileSetKey(last[0].Files) == key {
				groups[len(groups)-1] = append(last, tc)
				continue
			}
		}

		groups = append(groups, []session.ToolCall{tc})
	}

	return groups
}

func fileSetKey(files []string) string {
	if len(files) != 1 {
		return ""
	}

	return files[0]
}

// toolCallInput is a ToolCall's raw Input decoded into its meaningful
// parts. Input is JSON for Claude Code tool_use calls (old_string/
// new_string for Edit, content for Write, command for Bash) but plain
// rendered diff text for other adapters (e.g. Aider's SEARCH/REPLACE
// block); Raw holds the original string unchanged for that case.
type toolCallInput struct {
	Raw       string
	OldString string
	NewString string
	Content   string
	Command   string
}

func parseToolCallInput(tc *session.ToolCall) toolCallInput {
	trimmed := strings.TrimSpace(tc.Input)

	in := toolCallInput{Raw: trimmed}
	if !strings.HasPrefix(trimmed, "{") {
		return in
	}

	var fields map[string]any
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
		return in
	}

	if v, ok := fields["old_string"].(string); ok {
		in.OldString = v
	}

	if v, ok := fields["new_string"].(string); ok {
		in.NewString = v
	}

	if v, ok := fields["content"].(string); ok {
		in.Content = v
	}

	if v, ok := fields["command"].(string); ok {
		in.Command = v
	}

	return in
}

// diffText renders a toolCallInput as an LLM-readable diff.
func diffText(tc *session.ToolCall) string {
	in := parseToolCallInput(tc)

	switch {
	case in.OldString != "" || in.NewString != "":
		return truncate(fmt.Sprintf("- %s\n+ %s", in.OldString, in.NewString))
	case in.Content != "":
		return truncate(in.Content)
	case in.Command != "":
		return truncate(in.Command)
	default:
		return truncate(in.Raw)
	}
}

// expectedText is the content a ToolCall claims it left behind at Files[0]
// — what gitstate.Resolve checks is still present in the working tree.
func expectedText(tc *session.ToolCall) string {
	in := parseToolCallInput(tc)

	switch {
	case in.NewString != "":
		return in.NewString
	case in.Content != "":
		return in.Content
	default:
		return in.Raw
	}
}

func truncate(s string) string {
	if len(s) <= maxDiffLen {
		return s
	}

	return s[:maxDiffLen] + "..."
}
