package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
)

// scheduleState is the schedule view's cursor and whether the lease list is
// showing instead of the lanes.
type scheduleState struct {
	cursor int
	// offset is the first lane drawn, so the cursor stays on screen in a
	// lane list longer than the terminal, the way Home's rows do.
	offset int
	leases bool
}

// Lane cells are one column each, so the lane alphabet is its own: Home's
// glyphs include a two-character ASCII fallback that would smear a lane by a
// column per done thread.
var laneGlyphs = map[event.State]string{
	event.StateIdle:    "░",
	event.StateWorking: "█",
	event.StateGating:  "◐",
	event.StateNeedsIn: "▲",
	event.StateBlocked: "○",
	event.StateFailed:  "✖",
	event.StateDone:    "▔",
}

var laneASCII = map[event.State]string{
	event.StateIdle:    ".",
	event.StateWorking: "#",
	event.StateGating:  "*",
	event.StateNeedsIn: "!",
	event.StateBlocked: "o",
	event.StateFailed:  "x",
	event.StateDone:    "=",
}

func laneGlyph(st event.State, ascii bool) string {
	if ascii {
		return laneASCII[st]
	}

	return laneGlyphs[st]
}

// asciiArrow rewrites the daemon's step text for a terminal with no Unicode
// to render, since the lock wait carries its holder behind an arrow.
func asciiArrow(s string, ascii bool) string {
	if !ascii {
		return s
	}

	return strings.ReplaceAll(s, "←", "<-")
}

const (
	laneCellCount    = 15
	laneMinNameWidth = 18
	laneMinCells     = 8
	leaseSubtreeCol  = 28
	leaseHolderCol   = 18
	laneStatusFloor  = 16
	laneStatusMargin = 4
)

// scheduleTickMsg re-fetches the schedule while its screen is showing. The
// reply costs a memory reading, so it is fetched on demand rather than
// folded into the poll every client pays for.
type scheduleTickMsg struct{}

func scheduleTick() tea.Cmd {
	return tea.Tick(pollInterval, func(_ time.Time) tea.Msg { return scheduleTickMsg{} })
}

// openSchedule pushes the schedule screen, on its lanes rather than
// whichever list was showing last, and starts its refresh.
func (m Model) openSchedule() (Model, tea.Cmd) {
	m.push(screenSchedule)
	m.sched.leases = false

	if m.client == nil {
		return m, scheduleTick()
	}

	return m, tea.Batch(m.client.schedule(), scheduleTick())
}

func (m Model) refreshSchedule() (Model, tea.Cmd) {
	if m.top() != screenSchedule {
		return m, nil
	}

	if m.client == nil {
		return m, scheduleTick()
	}

	return m, tea.Batch(m.client.schedule(), scheduleTick())
}

func (m Model) updateScheduleKey(_ tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	lanes := m.schedule.Lanes

	switch s {
	case keyJ, keyDown:
		m.sched.cursor = min(m.sched.cursor+1, max(len(lanes)-1, 0))
	case keyK, keyUp:
		m.sched.cursor = max(m.sched.cursor-1, 0)
	case "l":
		m.sched.leases = !m.sched.leases
	case "x":
		return m.killLane()
	case keyEnter:
		if len(lanes) > 0 {
			return m.openThread(lanes[m.cappedLane()].ThreadID)
		}
	}

	return m, nil
}

// killLane cancels the selected thread's turn. Pausing one is not offered:
// the daemon can stop a turn, and a paused turn would have to hold the
// model's memory while doing nothing, which is what admission exists to
// avoid.
func (m Model) killLane() (Model, tea.Cmd) {
	lanes := m.schedule.Lanes
	if len(lanes) == 0 {
		m.status = msgNoThread

		return m, nil
	}

	lane := lanes[m.cappedLane()]
	m.status = "killed " + lane.Thread

	if m.client == nil {
		return m, nil
	}

	return m, m.client.cancel(lane.ThreadID)
}

func (m Model) cappedLane() int {
	if len(m.schedule.Lanes) == 0 {
		return 0
	}

	return min(max(m.sched.cursor, 0), len(m.schedule.Lanes)-1)
}

func (m Model) renderSchedule() string {
	s := m.schedule

	title := fmt.Sprintf("schedule · phase: %s · %s · %s",
		orDash(s.Phase), scheduleMem(s), scheduleModel(s))

	body := m.laneRows()
	if m.sched.leases {
		body = m.leaseRows()
	}
	body = append(body, m.gateSection()...)

	footer := footerHints(scheduleHints(m.sched.leases), m.width-boxPad)

	return frame(m.width, title, body, footer, m.th)
}

func scheduleMem(s api.Schedule) string {
	if !s.MemMeasured {
		return "mem -"
	}

	return fmt.Sprintf("mem %s/%s free %.0f%%", bytesGB(s.MemUsedBytes), bytesGB(s.MemTotalBytes),
		pct(freeFraction(s)))
}

func freeFraction(s api.Schedule) float64 {
	if s.MemTotalBytes == 0 {
		return 0
	}

	return float64(s.MemTotalBytes-s.MemUsedBytes) / float64(s.MemTotalBytes)
}

func scheduleModel(s api.Schedule) string {
	if s.LocalModel == "" {
		return "no local model"
	}

	return s.LocalModel + " loaded"
}

func (m Model) laneRows() []string {
	lanes := m.schedule.Lanes
	if len(lanes) == 0 {
		return []string{m.th.fgMuted.Render("no threads yet · press n on home to start one")}
	}

	cursor := m.cappedLane()
	window := m.laneWindow(lanes, cursor)

	visible := make([]api.Lane, 0, len(window))
	for _, at := range window {
		visible = append(visible, lanes[at])
	}

	name := laneNameWidth(visible, m.width)

	out := make([]string, 0, len(window)+1)
	for _, at := range window {
		out = append(out, m.renderLane(lanes[at], name, at == cursor))
	}

	if len(lanes) > len(window) {
		out = append(out, m.th.fgMuted.Render(fmt.Sprintf("  … %d more", len(lanes)-len(window))))
	}

	return out
}

func (m Model) laneWindow(lanes []api.Lane, cursor int) []int {
	return scrolledWindow(len(lanes), cursor, m.sched.offset, m.laneListBudget())
}

// laneListBudget is how many lanes fit once the two border lines, the gate
// section below them, and the "… N more" overflow line have taken theirs.
func (m Model) laneListBudget() int {
	chrome := frameRows + len(m.gateSection())

	budget := max(m.height-chrome, 1)
	if len(m.schedule.Lanes) <= budget {
		return budget
	}

	return max(budget-1, 1)
}

// laneNameWidth sizes the name column from the lanes actually visible: the
// names take what the fixed cells and a readable status leave, bounded by the
// widest name on screen, so a fleet of near-identical names stops being cut to
// its prefix while half the row sits empty. Home's homeColumns found the same
// for its name column.
func laneNameWidth(visible []api.Lane, width int) int {
	widest := 0
	for i := range visible {
		widest = max(widest, lipgloss.Width(visible[i].Thread))
	}

	room := width - boxPad - laneCellCount - laneStatusFloor - laneStatusMargin

	return min(max(room, laneMinNameWidth), max(widest, laneMinNameWidth))
}

func (m Model) renderLane(lane api.Lane, name int, selected bool) string {
	line := fmt.Sprintf("%-*s %s %s", name, truncate(lane.Thread, name),
		m.laneCells(lane), truncate(m.laneStatus(lane), m.laneStatusWidth(name)))

	if selected {
		return m.th.accent.Render("> " + line)
	}

	return m.th.fgDefault.Render("  " + line)
}

// laneCells renders the run of glyphs, dropping the oldest cells first when
// the frame is too narrow to carry the whole window. A run of one state says
// no more than the word beside it, so it blanks and the column holds its
// place, keeping every status on one column; the history returns as soon as
// the states differ.
func (m Model) laneCells(lane api.Lane) string {
	cells := lane.Cells

	if room := m.laneCellBudget(); len(cells) > room {
		cells = cells[len(cells)-room:]
	}

	same := len(cells) > 0
	for _, c := range cells {
		if c != cells[0] {
			same = false

			break
		}
	}

	if same {
		return strings.Repeat(" ", len(cells))
	}

	var b strings.Builder
	for _, c := range cells {
		b.WriteString(laneGlyph(c, m.ascii))
	}

	return b.String()
}

// laneCellBudget is how many cells fit beside the name and a readable status.
func (m Model) laneCellBudget() int {
	return max(m.width-boxPad-laneMinNameWidth-laneStatusFloor-laneStatusMargin, laneMinCells)
}

func (m Model) laneStatusWidth(name int) int {
	shown := min(laneCellCount, m.laneCellBudget())

	return max(m.width-boxPad-name-shown-laneStatusMargin, laneMinCells)
}

// laneStatus says what the lane is waiting on before what it is doing, since
// a thread that waits is the one a reader has to act on.
func (m Model) laneStatus(lane api.Lane) string {
	if lane.Lock != "" {
		return asciiArrow(fmt.Sprintf("lock %s ← %s", lane.Lock, lane.LockHolder), m.ascii)
	}

	if lane.Gate != "" {
		return lane.Gate
	}

	return asciiArrow(lane.Step, m.ascii)
}

func (m Model) leaseRows() []string {
	leases := m.schedule.Leases
	if len(leases) == 0 {
		return []string{m.th.fgMuted.Render("no leases held")}
	}

	out := make([]string, 0, len(leases)+1)
	out = append(out, m.th.fgMuted.Render(fmt.Sprintf("  %-*s %-*s %-10s %s",
		leaseSubtreeCol, "subtree", leaseHolderCol, "holder", "state", "waiting")))

	budget := m.leaseListBudget()
	for _, l := range leases {
		if len(out) > budget {
			out = append(out, m.th.fgMuted.Render(fmt.Sprintf("  … %d more", len(leases)-budget+1)))

			break
		}

		out = append(out, m.th.fgDefault.Render(fmt.Sprintf("  %-*s %-*s %-10s %s",
			leaseSubtreeCol, truncate(l.Subtree, leaseSubtreeCol), leaseHolderCol, truncate(l.Holder, leaseHolderCol),
			l.State, strings.Join(l.Waiters, ", "))))
	}

	return out
}

// leaseListBudget is how many lease rows fit, header included, once the border
// lines and the gate section have taken theirs.
func (m Model) leaseListBudget() int {
	return max(m.height-frameRows-len(m.gateSection())-1, 1)
}

// gateSection renders the selected thread's gate run as the one line
// DESIGN.md's mock puts under the lanes.
func (m Model) gateSection() []string {
	lanes := m.schedule.Lanes
	if len(lanes) == 0 {
		return nil
	}

	lane := lanes[m.cappedLane()]

	label := "gate · " + lane.Thread
	body := m.th.fgMuted.Render("nothing running")

	if lane.Gate != "" {
		label = "gate · " + lane.Gate + " · " + lane.Thread
		body = m.th.fgDefault.Render(fmt.Sprintf("changed → select → run %s → trim",
			glyph(event.StateGating, m.ascii)))
	}

	return []string{sectionRule(label, m.width-boxPad), body}
}

// sectionRule is the mid-frame separator carrying a label, matching the
// frame's own top and bottom rules.
func sectionRule(label string, width int) string {
	fill := width - lipgloss.Width(label) - 1
	if fill < 0 {
		fill = 0
	}

	return label + " " + strings.Repeat("─", fill)
}

func scheduleHints(leases bool) []hint {
	locks := "locks"
	if leases {
		locks = "lanes"
	}

	return []hint{
		{keyEnter, labelOpen, ""},
		{"l", locks, ""},
		{"x", "kill", ""},
		{keyEsc, labelBack, ""},
		{"?", labelHelp, ""},
	}
}
