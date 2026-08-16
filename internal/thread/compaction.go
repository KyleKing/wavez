package thread

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/llm"
)

// charsPerToken approximates one token as 4 characters of English or code, a
// heuristic used only to report an estimated saving, never to size a
// provider's real context budget.
const charsPerToken = 4

// headAndTail is TruncateToolOutput's keepLines multiplier: one copy for the
// head, one for the tail.
const headAndTail = 2

// ruleCount sizes Report.Rules so Compact never reallocates its map.
const ruleCount = 3

// Savings reports one compaction rule's effect.
type Savings struct {
	TokensSaved  int
	ItemsChanged int
}

// Report aggregates every rule Compact ran, keyed by rule name.
type Report struct {
	Rules       map[string]Savings
	TotalTokens int
}

// CompactOptions tunes which deterministic compaction rules run. A zero field
// disables the rule it controls.
type CompactOptions struct {
	// KeepLines is how many lines TruncateToolOutput keeps at the start and
	// end of an oversized tool result. Zero disables the rule.
	KeepLines int
	// MaxToolAge is how many turns a tool result may lag the current turn
	// before DropOldToolResults replaces it with a reference. Zero disables
	// the rule.
	MaxToolAge int
	// DedupeReads enables replacing a repeated identical tool result with a
	// reference to its first occurrence.
	DedupeReads bool
}

// Compact runs every rule CompactOptions enables over items, in the order
// truncate, drop-old, dedupe, and returns the resulting view alongside a
// Report of what each rule saved. Items and the Thread it was read from are
// never mutated; Compact returns a new slice.
func Compact(items []TurnMessage, currentTurn int, opts CompactOptions) ([]TurnMessage, Report) {
	report := Report{Rules: make(map[string]Savings, ruleCount)}
	out := items

	if opts.KeepLines > 0 {
		var s Savings
		out, s = TruncateToolOutput(out, opts.KeepLines)
		report.Rules["truncate_tool_output"] = s
		report.TotalTokens += s.TokensSaved
	}
	if opts.MaxToolAge > 0 {
		var s Savings
		out, s = DropOldToolResults(out, currentTurn, opts.MaxToolAge)
		report.Rules["drop_old_tool_results"] = s
		report.TotalTokens += s.TokensSaved
	}
	if opts.DedupeReads {
		var s Savings
		out, s = DedupeToolReads(out)
		report.Rules["dedupe_tool_reads"] = s
		report.TotalTokens += s.TokensSaved
	}

	return out, report
}

// TruncateToolOutput keeps the first and last keepLines lines of every tool
// result longer than that, replacing the middle with a count of dropped
// lines. It returns a new slice; items is not mutated.
func TruncateToolOutput(items []TurnMessage, keepLines int) ([]TurnMessage, Savings) {
	out := make([]TurnMessage, len(items))
	copy(out, items)

	var savings Savings

	for i, item := range out {
		if item.Message.Role != llm.RoleTool {
			continue
		}
		lines := strings.Split(item.Message.Content, "\n")
		threshold := keepLines * headAndTail
		if len(lines) <= threshold {
			continue
		}
		dropped := len(lines) - threshold
		head := lines[:keepLines]
		tail := lines[len(lines)-keepLines:]
		replacement := strings.Join(head, "\n") +
			fmt.Sprintf("\n... %d lines omitted ...\n", dropped) +
			strings.Join(tail, "\n")

		saved := estimateTokens(item.Message.Content) - estimateTokens(replacement)
		if saved <= 0 {
			continue
		}
		item.Message.Content = replacement
		out[i] = item
		savings.TokensSaved += saved
		savings.ItemsChanged++
	}

	return out, savings
}

// DropOldToolResults replaces a tool result's content with a one-line
// reference once it lags currentTurn by more than maxAge turns. It returns a
// new slice; items is not mutated.
func DropOldToolResults(items []TurnMessage, currentTurn, maxAge int) ([]TurnMessage, Savings) {
	out := make([]TurnMessage, len(items))
	copy(out, items)

	var savings Savings

	for i, item := range out {
		if item.Message.Role != llm.RoleTool {
			continue
		}
		if currentTurn-item.Turn <= maxAge {
			continue
		}
		ref := fmt.Sprintf("[tool result from turn %d omitted, see thread log]", item.Turn)
		saved := estimateTokens(item.Message.Content) - estimateTokens(ref)
		if saved <= 0 {
			continue
		}
		item.Message.Content = ref
		out[i] = item
		savings.TokensSaved += saved
		savings.ItemsChanged++
	}

	return out, savings
}

// DedupeToolReads replaces a tool result whose content byte-matches an
// earlier tool result with a reference to the turn that first produced it. It
// returns a new slice; items is not mutated.
func DedupeToolReads(items []TurnMessage) ([]TurnMessage, Savings) {
	out := make([]TurnMessage, len(items))
	copy(out, items)

	seen := make(map[string]int, len(out))
	var savings Savings

	for i, item := range out {
		if item.Message.Role != llm.RoleTool || item.Message.Content == "" {
			continue
		}
		hash := contentHash(item.Message.Content)
		firstTurn, dup := seen[hash]
		if !dup {
			seen[hash] = item.Turn

			continue
		}
		ref := fmt.Sprintf("[same content as turn %d, hash %s]", firstTurn, hash[:8])
		saved := estimateTokens(item.Message.Content) - estimateTokens(ref)
		if saved <= 0 {
			continue
		}
		item.Message.Content = ref
		out[i] = item
		savings.TokensSaved += saved
		savings.ItemsChanged++
	}

	return out, savings
}

// Flatten discards turn metadata, returning the plain llm.Message slice a
// request needs.
func Flatten(items []TurnMessage) []llm.Message {
	out := make([]llm.Message, len(items))
	for i, item := range items {
		out[i] = item.Message
	}

	return out
}

func contentHash(s string) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])
}

func estimateTokens(s string) int {
	return len(s) / charsPerToken
}
