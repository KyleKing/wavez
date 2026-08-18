package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/api"
)

// routinesState is the routines panel's cursor and whether the selected
// row's run history is expanded.
type routinesState struct {
	cursor  int
	history bool
}

// secondsPerMinute is the divisor the duration renderer uses for the
// seconds remainder of a run over a minute.
const secondsPerMinute = 60

// Column widths for a routine row, sized so an 80-column frame still shows
// a sparkline.
const (
	routineNameWidth    = 18
	routineTriggerWidth = 22
	sparklineWidth      = 12
	historyRows         = 8
)

func (m Model) updateRoutinesKey(_ tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	rows := m.routines
	if len(rows) == 0 {
		return m, nil
	}

	cursor := m.cappedRoutineCursor(len(rows))

	switch s {
	case keyJ, keyDown:
		m.routinesUI.cursor = cursor + 1
	case "k", keyUp:
		m.routinesUI.cursor = max(cursor-1, 0)
	case "g":
		m.routinesUI.cursor = 0
	case "G":
		m.routinesUI.cursor = len(rows) - 1
	case "h":
		m.routinesUI.history = !m.routinesUI.history
	case "r":
		return m.runRoutine(rows[cursor])
	}

	m.routinesUI.cursor = m.cappedRoutineCursor(len(rows))

	return m, nil
}

func (m Model) runRoutine(row api.RoutineInfo) (Model, tea.Cmd) {
	if !row.Enabled {
		m.status = row.Name + " is disabled in .wavez.pkl"

		return m, nil
	}

	if m.client == nil {
		return m, nil
	}

	m.status = "running " + row.Name

	return m, m.client.runRoutine(row.Name)
}

func (m Model) cappedRoutineCursor(n int) int {
	if n == 0 {
		return 0
	}

	return min(max(m.routinesUI.cursor, 0), n-1)
}

func routinesHints() []hint {
	return []hint{{"r", "run"}, {"h", "history"}, {keyEsc, labelBack}, {"?", labelHelp}}
}

func (m Model) renderRoutines() string {
	rows := m.routines
	cursor := m.cappedRoutineCursor(len(rows))

	body := make([]string, 0, len(rows)+historyRows)

	for i := range rows {
		line := m.routineRow(rows[i])

		if i != cursor {
			body = append(body, m.th.fgDefault.Render("  "+line))

			continue
		}

		body = append(body, m.th.accent.Render("> "+line))

		if m.routinesUI.history {
			body = append(body, m.routineHistory(rows[i])...)
		}
	}

	if len(rows) == 0 {
		body = append(body, m.th.fgMuted.Render("no routines · define one in .wavez.pkl"))
	}

	title := fmt.Sprintf("routines · %d", len(rows))
	footer := footerHints(routinesHints(), m.width-boxPad)

	return frame(m.width, title, body, footer, m.th)
}

func (m Model) routineRow(r api.RoutineInfo) string {
	last := "-"
	if n := len(r.Runs); n > 0 {
		last = age(r.Runs[n-1].Started, m.now())
	}

	return fmt.Sprintf("%s %-18s %-22s %-6s %s",
		routineMark(r, m.ascii),
		truncate(r.Name, routineNameWidth),
		truncate(triggerList(r), routineTriggerWidth),
		last,
		durationSparkline(durations(r.Runs), m.ascii))
}

// routineMark says at a glance whether the last run passed, using the same
// alphabet the thread rows use rather than a second set of symbols.
func routineMark(r api.RoutineInfo, ascii bool) string {
	switch {
	case !r.Enabled:
		return markDisabled(ascii)
	case len(r.Runs) == 0:
		return markIdle(ascii)
	case r.Runs[len(r.Runs)-1].Pass:
		return markPass(ascii)
	default:
		return markFail(ascii)
	}
}

func markDisabled(ascii bool) string {
	if ascii {
		return "-"
	}

	return "⊘"
}

func markIdle(ascii bool) string {
	if ascii {
		return "o"
	}

	return "○"
}

func markPass(ascii bool) string {
	if ascii {
		return "ok"
	}

	return "✔"
}

func markFail(ascii bool) string {
	if ascii {
		return "x"
	}

	return "✖"
}

func triggerList(r api.RoutineInfo) string {
	if len(r.Triggers) == 0 {
		return "-"
	}

	return strings.Join(r.Triggers, ",")
}

func (m Model) routineHistory(r api.RoutineInfo) []string {
	if len(r.Runs) == 0 {
		return []string{m.th.fgMuted.Render("    no runs yet")}
	}

	runs := r.Runs
	if len(runs) > historyRows {
		runs = runs[len(runs)-historyRows:]
	}

	out := make([]string, 0, len(runs))

	for i := len(runs) - 1; i >= 0; i-- {
		run := runs[i]

		line := fmt.Sprintf("    %-6s %-9s %-7s %s",
			age(run.Started, m.now()), run.Trigger, duration(run.Duration), runOutcome(run))
		out = append(out, m.th.fgMuted.Render(truncate(line, m.width-boxPad)))
	}

	return out
}

func runOutcome(run api.RoutineRun) string {
	if run.Pass {
		return "pass"
	}

	if len(run.Failed) == 0 {
		return "fail"
	}

	return strings.Join(run.Failed, ", ")
}

// duration renders a run's wall time in the compact form the panel's rows
// use, sub-second runs included: a routine that finishes in 40 ms must not
// render as "0s" beside one that took a minute.
func duration(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%secondsPerMinute)
	}
}

func durations(runs []api.RoutineRun) []time.Duration {
	out := make([]time.Duration, 0, len(runs))
	for _, r := range runs {
		out = append(out, r.Duration)
	}

	return out
}

// durationSparkline renders the last sparklineWidth durations relative to
// the longest of them, with zero as the floor so a fast routine reads low.
// The scale is per routine rather than global, so a fast routine's variation
// is still visible beside a slow one.
func durationSparkline(values []time.Duration, ascii bool) string {
	if len(values) == 0 {
		return ""
	}

	if len(values) > sparklineWidth {
		values = values[len(values)-sparklineWidth:]
	}

	alphabet := sparkGlyphs
	if ascii {
		alphabet = sparkASCII
	}

	longest := values[0]
	for _, v := range values {
		longest = max(longest, v)
	}

	var b strings.Builder

	for _, v := range values {
		b.WriteRune(alphabet[bucket(v, longest, len(alphabet))])
	}

	return b.String()
}

func bucket(v, longest time.Duration, buckets int) int {
	if longest <= 0 {
		return 0
	}

	idx := int(int64(v) * int64(buckets) / int64(longest))

	return min(max(idx, 0), buckets-1)
}
