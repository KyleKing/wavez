package tui

import (
	"strings"

	"github.com/kyleking/wavez/internal/event"
)

// goalIndent is the width the "earlier:" list is inset by.
const goalIndent = 2

// goalHeaderMin is the width a goal needs in the header before it is worth
// showing at all. Below it the header keeps the thread's name and the goal
// is one keypress away, which is the tradeoff a narrow terminal makes for
// every other optional field here.
const goalHeaderMin = 24

// headerGoal is the goal as the thread header shows it: appended when the
// title leaves room for it and dropped first when it does not, because what
// a thread is for is the field a reader can recover with a key and the
// model name is not.
func headerGoal(title, goal string, width int) string {
	room := width - boxPad - len(title) - len(" · ")
	if goal == "" || room < goalHeaderMin {
		return title
	}

	return title + " · " + truncate(goal, room)
}

// renderGoal shows the standing goal in full, with every earlier wording
// under it, so a thread whose goal was rewritten says what it used to be.
func (m Model) renderGoal() string {
	info, ok := m.activeThread()
	if !ok {
		return frame(m.width, "goal", []string{m.th.fgMuted.Render("no thread selected")}, keyEsc+" "+labelBack, m.th)
	}

	body := []string{m.th.fgMuted.Render("no goal recorded for this thread")}
	if info.Goal != "" {
		body = wrapLines(info.Goal, m.width-boxPad)
	}

	if past := m.pastGoals(info.ID); len(past) > 0 {
		body = append(body, "", m.th.fgMuted.Render("earlier:"))
		for _, g := range past {
			body = append(body, m.th.fgMuted.Render("  "+truncate(g, m.width-boxPad-goalIndent)))
		}
	}

	return frame(m.width, "goal · "+info.Name, body, "g/"+keyEsc+" "+labelBack, m.th)
}

// pastGoals is every wording this thread's goal had before the current one,
// most recent first. A rewritten goal is a decision, and the decision is
// only readable beside what it replaced.
func (m Model) pastGoals(id string) []string {
	tr, ok := m.transcripts[id]
	if !ok {
		return nil
	}

	var past []string

	for _, row := range tr.rows {
		if row.kind == event.KindGoal {
			past = append(past, row.text)
		}
	}

	if len(past) == 0 {
		return nil
	}

	// The last one is the goal the header and the body already show.
	earlier := past[:len(past)-1]
	out := make([]string, 0, len(earlier))

	for i := len(earlier) - 1; i >= 0; i-- {
		out = append(out, earlier[i])
	}

	return out
}

// wrapLines breaks text to width on spaces, which is enough for one goal.
func wrapLines(text string, width int) []string {
	var (
		out  []string
		line strings.Builder
	)

	for _, word := range strings.Fields(text) {
		if line.Len() > 0 && line.Len()+1+len(word) > width {
			out = append(out, line.String())
			line.Reset()
		}

		if line.Len() > 0 {
			line.WriteString(" ")
		}

		line.WriteString(word)
	}

	if line.Len() > 0 {
		out = append(out, line.String())
	}

	return out
}
