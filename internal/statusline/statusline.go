package statusline

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/kyleking/wavez/internal/goal"
	"github.com/kyleking/wavez/internal/session"
)

// contextHigh is the share of the context window, in percent, from which
// the line asks the person to act: past it the harness is close to
// compacting, and a fresh session is cheaper than a compacted one.
const contextHigh = 80

const separator = " · "

// The ANSI sequences the line uses.
const (
	red    = "\x1b[31m"
	yellow = "\x1b[33m"
	dim    = "\x1b[2m"
	reset  = "\x1b[0m"
)

// Input is everything one rendering of the line reads.
type Input struct {
	// State is what the hooks kept about the session, and nil when they
	// kept nothing.
	State *session.State
	// Goal is the project's goal as its last check left it, and nil when
	// the project has none.
	Goal *goal.Summary
	// ContextPercent is how full the context window is, and nil when the
	// harness did not say.
	ContextPercent *float64
	// CostUSD is what the session has cost so far.
	CostUSD float64
	// MaxRounds is how many times the gates fail the same way before they
	// let the agent stop.
	MaxRounds int
	// Width is the terminal's width in columns, and 0 when unknown.
	Width int
	// Color turns the ANSI colors on.
	Color bool
}

// segment is one part of the line.
type segment struct {
	text  string
	color string
}

// Render returns the line. What needs the person comes first and in red;
// work still under way is yellow; the rest is dim. A line wider than the
// terminal loses its last parts first, so what needs the person is the last
// thing to go.
func Render(in Input) string {
	parts := slicesOf(needs(in), progress(in))
	if len(parts) == 0 {
		return ""
	}

	parts = append([]segment{{text: "wavez", color: dim}}, parts...)
	for in.Width > 0 && len(parts) > 2 && width(parts) > in.Width {
		parts = parts[:len(parts)-1]
	}

	texts := make([]string, 0, len(parts))

	for _, part := range parts {
		if in.Color && part.color != "" {
			texts = append(texts, part.color+part.text+reset)

			continue
		}

		texts = append(texts, part.text)
	}

	return strings.Join(texts, separator)
}

func slicesOf(groups ...[]segment) []segment {
	var all []segment

	for _, group := range groups {
		all = append(all, group...)
	}

	return all
}

func width(parts []segment) int {
	total := (len(parts) - 1) * utf8.RuneCountInString(separator)

	for _, part := range parts {
		total += utf8.RuneCountInString(part.text)
	}

	return total
}

// needs lists what the person has to look at or decide.
func needs(in Input) []segment {
	var out []segment

	if state := in.State; state != nil {
		switch {
		case in.MaxRounds > 0 && state.Failure.Rounds >= in.MaxRounds:
			out = append(out, segment{text: "! checks failing, agent let go", color: red})
		case state.Abstained != "":
			out = append(out, segment{text: "! checks could not run", color: red})
		}
	}

	if in.Goal != nil && in.Goal.CouldNotCheck > 0 {
		text := "! " + count(in.Goal.CouldNotCheck, "goal condition") + " could not be checked"
		out = append(out, segment{text: text, color: red})
	}

	if in.ContextPercent != nil && *in.ContextPercent >= contextHigh {
		out = append(out, segment{text: fmt.Sprintf("! context %.0f%% full", *in.ContextPercent), color: red})
	}

	return out
}

// progress lists how far the work has got, none of which asks for anything.
func progress(in Input) []segment {
	var out []segment

	if state := in.State; state != nil {
		switch {
		case state.Failure.Rounds > 0 && (in.MaxRounds == 0 || state.Failure.Rounds < in.MaxRounds):
			text := fmt.Sprintf("fixing checks, round %d", state.Failure.Rounds)
			out = append(out, segment{text: text, color: yellow})
		case state.Failure.Rounds == 0 && state.Abstained == "" && state.Passed != "":
			out = append(out, segment{text: "checks passed", color: dim})
		}
	}

	if in.Goal != nil {
		out = append(out, goalSegment(*in.Goal))
	}

	if in.ContextPercent != nil && *in.ContextPercent < contextHigh {
		out = append(out, segment{text: fmt.Sprintf("context %.0f%%", *in.ContextPercent), color: dim})
	}

	if in.CostUSD > 0 {
		out = append(out, segment{text: fmt.Sprintf("$%.2f", in.CostUSD), color: dim})
	}

	return out
}

func goalSegment(sum goal.Summary) segment {
	text := fmt.Sprintf("goal %d/%d", sum.Done, sum.Total)

	switch {
	case sum.Total == 0:
		return segment{text: "goal has no conditions", color: yellow}
	case sum.Done == sum.Total:
		return segment{text: text + " done", color: dim}
	case len(sum.Unmet) > 0:
		text += ", next: " + sum.Unmet[0]
	case sum.Stale:
		text += ", checked at the end of the turn"
	}

	return segment{text: text, color: yellow}
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return fmt.Sprintf("%d %ss", n, noun)
}
