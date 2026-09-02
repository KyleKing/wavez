package bench

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/event"
)

// barWidth is how many cells the longest turn's bar takes. The bars are
// relative to that turn rather than to wall time, because what a reader is
// looking for is the turn that cost several of the others put together.
const barWidth = 24

// subMinuteResolution is what a turn under a minute is reported to.
const subMinuteResolution = 100 * time.Millisecond

// ToolsPerRow bounds the tool calls one line names. A turn that called more
// than this says how many it made instead, since the point of the line is
// the shape of the run and not a second copy of the log.
const ToolsPerRow = 6

// Call is one tool call as the timeline reads it: what ran and why it
// failed, if it did.
type Call struct {
	Tool  string
	Cause string
	Error bool
}

// Turn is one turn of a run: how long it took, what it called, and what the
// harness did to it, which is where a run's answer to a failure begins.
type Turn struct {
	At    time.Time
	Tier  string
	Calls []Call
	// Notes are what the harness did during the turn: a gate delivery, a
	// gate escalation, a tier that failed and moved up.
	Notes []string
	// Duration is the turn's wall time with the time it spent parked for a
	// human taken out, since what a reader is comparing turns for is the
	// work, and a turn that waited five minutes for an approval was not
	// five minutes of work.
	Duration time.Duration
	// Waited is that parked time, kept rather than dropped: a run held up
	// on approvals is a fact about the session worth seeing.
	Waited time.Duration
	Number int
}

// Timeline reduces a thread's events to one row per turn.
//
// It is the counterpart of Summarize: the same log read as a sequence rather
// than as totals, because a run that spent 80 turns tells a reader something
// through where its tool calls and gate rounds fell that no count can.
func Timeline(evs []event.Event) []Turn {
	var (
		out      []Turn
		current  Turn
		started  time.Time
		parkedAt time.Time
	)

	for i := range evs {
		ev := &evs[i]

		if started.IsZero() {
			started = ev.At
		}

		parkedAt = trackParked(ev, parkedAt, &current)

		switch ev.Kind {
		case event.KindTool:
			current.Calls = append(current.Calls, Call{
				Tool:  ev.Tool,
				Error: boolField(ev.Detail, "is_error"),
				Cause: stringField(ev.Detail, "cause"),
			})
		case event.KindGate:
			if note := gateNote(ev); note != "" {
				current.Notes = append(current.Notes, note)
			}
		case event.KindError:
			if boolField(ev.Detail, "escalated") {
				current.Notes = append(current.Notes,
					stringField(ev.Detail, "tier")+" failed, moved up")
			}
		case event.KindAgent:
			if ev.Role == "" {
				continue
			}

			current.Number = len(out) + 1
			current.At = started
			current.Duration = ev.At.Sub(started) - current.Waited
			current.Tier = stringField(ev.Detail, "tier")
			out = append(out, current)

			current, started = Turn{}, ev.At
		default:
		}
	}

	return out
}

// trackParked follows the intervals a thread sits waiting for a human,
// folding each finished one into the turn it fell in and returning when the
// current one started, zero when none is open.
func trackParked(ev *event.Event, parkedAt time.Time, current *Turn) time.Time {
	if ev.Kind != event.KindState {
		return parkedAt
	}

	if ev.State == event.StateNeedsIn {
		if parkedAt.IsZero() {
			return ev.At
		}

		return parkedAt
	}

	if parkedAt.IsZero() {
		return parkedAt
	}

	current.Waited += ev.At.Sub(parkedAt)

	return time.Time{}
}

// gateNote is what a gate event says about the turn it fell in: a delivery
// the run was handed, or the escalation one triggered. A gate round that was
// never delivered is a fact about the harness rather than something the turn
// was told, which is the same population countGate counts.
//
// A delivery carries no gate name, because it is the batch's verdict rather
// than one gate's, and an escalation carries the gate that caused it.
func gateNote(ev *event.Event) string {
	if boolField(ev.Detail, "escalated") {
		if name := stringField(ev.Detail, "gate"); name != "" {
			return "escalated on " + name
		}

		return "escalated"
	}

	if !boolField(ev.Detail, "delivered") || boolField(ev.Detail, "false_alarm") {
		return ""
	}

	if pass, ok := ev.Detail["pass"].(bool); ok && !pass {
		return "gates failed"
	}

	return "gates passed"
}

// RenderTimeline writes one line per turn: a bar for what the turn cost,
// the tools it called with a mark on each failure, and the gate rounds and
// tier changes beside them.
func RenderTimeline(w io.Writer, turns []Turn) error {
	if len(turns) == 0 {
		return writeLine(w, "no turns recorded")
	}

	longest := LongestTurn(turns)
	previousTier := ""

	for _, t := range turns {
		if err := writeLine(w, TurnLine(t, longest, previousTier)); err != nil {
			return err
		}

		if t.Tier != "" {
			previousTier = t.Tier
		}
	}

	return nil
}

// LongestTurn is what the bars are scaled against.
func LongestTurn(turns []Turn) time.Duration {
	longest := time.Duration(0)
	for i := range turns {
		longest = max(longest, turns[i].Duration)
	}

	return longest
}

// TurnLine renders one turn: its bar, its duration, the tools it called, and
// the notes beside them, where previousTier is the tier the turn before it
// ran on, empty for the first turn, which is what makes a tier change a note
// rather than a column repeated on every row.
func TurnLine(t Turn, longest time.Duration, previousTier string) string {
	var line strings.Builder

	fmt.Fprintf(&line, "%3d  %s %6s  %s",
		t.Number, bar(t.Duration, longest), shortDuration(t.Duration), callList(t.Calls))

	notes := t.Notes
	if t.Waited > 0 {
		notes = append(slices.Clone(notes), "waited "+shortDuration(t.Waited)+" for you")
	}

	if t.Tier != "" && previousTier != "" && t.Tier != previousTier {
		notes = append(slices.Clone(notes), "now on "+t.Tier)
	}

	for _, n := range notes {
		line.WriteString("  · " + n)
	}

	return strings.TrimRight(line.String(), " ")
}

func writeLine(w io.Writer, s string) error {
	if _, err := fmt.Fprintln(w, s); err != nil {
		return fmt.Errorf("writing the timeline: %w", err)
	}

	return nil
}

// bar is the turn's share of the longest turn, rounded up so a turn that
// cost anything at all is visible, padded to a fixed column. The padding is
// counted in cells rather than bytes, since the block character is three of
// them and a %-*s verb would step the column by turn length.
func bar(d, longest time.Duration) string {
	cells := 0
	if longest > 0 && d > 0 {
		cells = max(int(float64(d)/float64(longest)*barWidth), 1)
	}

	return strings.Repeat("█", cells) + strings.Repeat(" ", barWidth-cells)
}

// callList names the tools a turn called, each failure carrying its cause,
// since a call that failed is what the next turn is about.
func callList(calls []Call) string {
	if len(calls) == 0 {
		return "-"
	}

	if len(calls) > ToolsPerRow {
		return fmt.Sprintf("%d tool calls", len(calls))
	}

	names := make([]string, 0, len(calls))

	for _, c := range calls {
		switch {
		case c.Error && c.Cause != "":
			names = append(names, c.Tool+" ✗"+c.Cause)
		case c.Error:
			names = append(names, c.Tool+" ✗")
		default:
			names = append(names, c.Tool)
		}
	}

	return strings.Join(names, " ")
}

// shortDuration renders a turn's wall time in the width the column has.
func shortDuration(d time.Duration) string {
	switch {
	case d >= time.Minute:
		return d.Round(time.Second).String()
	case d >= time.Second:
		return d.Round(subMinuteResolution).String()
	default:
		return d.Round(time.Millisecond).String()
	}
}
