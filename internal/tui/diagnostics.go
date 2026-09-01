package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/api"
)

// diagSection names one row of the panel. Tab walks them, and Enter drills
// into the selected one.
type diagSection int

// Sections in the order they render, matching DESIGN.md's mockup.
const (
	sectionMemory diagSection = iota
	sectionCPU
	sectionLocal
	sectionHosted
	sectionGates
	sectionLeases
	sectionTools
	sectionEvents
	sectionCount
)

// diagState is the panel's selected section and whether it is drilled in.
type diagState struct {
	section diagSection
	drilled bool
}

// diagStrip renders the one-line summary embedded in every screen's header,
// fed entirely from api.Diagnostics.
func diagStrip(d api.Diagnostics) string {
	if !d.Measured(api.GaugeMemory) {
		return "mem -"
	}

	return fmt.Sprintf("mem %s/%s", bytesGB(d.MemUsedBytes), bytesGB(d.MemTotalBytes))
}

func bytesGB(b uint64) string {
	const gb = 1 << 30

	return fmt.Sprintf("%.1fG", float64(b)/gb)
}

func diagnosticsHints(drilled bool) []hint {
	if drilled {
		return []hint{{keyEsc, "rows", ""}, {keyTab, "section", ""}, {"r", "reset", ""}, {"?", labelHelp, ""}}
	}

	return []hint{
		{keyTab, "section", ""},
		{keyEnter, "drill", ""},
		{"r", "reset window", ""},
		{keyEsc, labelBack, ""},
		{"?", labelHelp, ""},
	}
}

// pct converts a 0..1 ratio to a whole percentage for display.
func pct(ratio float64) float64 {
	const percent = 100

	return ratio * percent
}

func (m Model) updateDiagnosticsKey(s string) (Model, tea.Cmd) {
	switch s {
	case keyTab, keyJ, keyDown:
		m.diagUI.section = (m.diagUI.section + 1) % sectionCount
	case keyShTab, keyK, keyUp:
		m.diagUI.section = (m.diagUI.section + sectionCount - 1) % sectionCount
	case keyEnter:
		m.diagUI.drilled = !m.diagUI.drilled
	case "r":
		m.status = "diagnostics window reset"

		if m.client != nil {
			return m, m.client.resetDiag()
		}
	}

	return m, nil
}

// sparkGlyphs is the eighth-block ramp a sparkline is drawn from, with an
// ASCII ramp beside it so a monochrome or NO_COLOR terminal still reads a
// shape rather than a row of boxes.
var (
	sparkGlyphs = []rune("▁▂▃▄▅▆▇█")
	sparkASCII  = []rune(".:-=+*#@")
)

// sparkWidth is how many samples a row's sparkline shows, matching the
// mockup's width.
const sparkWidth = 8

// sparkline renders the last sparkWidth samples of series, scaled to its own
// range so a flat line reads as flat rather than as empty. A series with no
// samples renders as spaces, since a dash there would read as a measurement.
func sparkline(series []float64, ascii bool) string {
	if len(series) == 0 {
		return strings.Repeat(" ", sparkWidth)
	}

	if len(series) > sparkWidth {
		series = series[len(series)-sparkWidth:]
	}

	glyphs := sparkGlyphs
	if ascii {
		glyphs = sparkASCII
	}

	low, high := series[0], series[0]
	for _, v := range series {
		low, high = min(low, v), max(high, v)
	}

	var b strings.Builder
	for _, v := range series {
		idx := 0
		if high > low {
			idx = int(float64(len(glyphs)-1) * (v - low) / (high - low))
		}
		b.WriteRune(glyphs[idx])
	}

	return padRight(b.String(), sparkWidth)
}

func (m Model) spark(g api.Gauge) string {
	return sparkline(m.diag.Sparks[g], m.ascii)
}

func (m Model) renderDiagnostics() string {
	body := m.diagRows()

	if m.diagUI.drilled {
		body = append(body, "", m.th.fgEmphasis.Render("per thread"))
		body = append(body, m.diagDrill()...)
	}

	footer := footerHints(diagnosticsHints(m.diagUI.drilled), m.width-boxPad)

	return frame(m.width, "diagnostics", body, footer, m.th)
}

// diagRows renders one line per section, marking the selected one so Tab and
// Enter have something to point at.
func (m Model) diagRows() []string {
	lines := []string{
		m.memRow(), m.cpuRow(), m.localRow(), m.hostedRow(),
		m.gatesRow(), m.leasesRow(), m.toolsRow(), m.eventsRow(),
	}

	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if diagSection(i) == m.diagUI.section {
			out = append(out, m.th.accent.Render("> "+line))

			continue
		}

		out = append(out, m.th.fgDefault.Render("  "+line))
	}

	return out
}

func (m Model) memRow() string {
	d := m.diag

	return fmt.Sprintf("mem    %-11s %s  model %s resident  disk %s",
		gauge(d, api.GaugeMemory, memPair(d)),
		m.spark(api.GaugeMemory),
		gauge(d, api.GaugeModelBytes, bytesGB(d.ModelBytes)),
		gauge(d, api.GaugeModelDisk, bytesGB(d.ModelDiskBytes)))
}

func (m Model) cpuRow() string {
	d := m.diag

	return fmt.Sprintf("cpu    %-11s %s  daemon %s  model %s  gates %s  tui %s",
		gauge(d, api.GaugeCPU, fmt.Sprintf("%.0f%%", d.CPUPercent)),
		m.spark(api.GaugeCPU),
		gauge(d, api.GaugeCPUDaemon, fmt.Sprintf("%.0f%%", d.CPUDaemon)),
		gauge(d, api.GaugeCPUModel, fmt.Sprintf("%.0f%%", d.CPUModel)),
		gauge(d, api.GaugeCPUGates, fmt.Sprintf("%.0f%%", d.CPUGates)),
		gauge(d, api.GaugeCPUTUI, fmt.Sprintf("%.0f%%", d.CPUTUI)))
}

func (m Model) localRow() string {
	d := m.diag

	return fmt.Sprintf("local  %s  ctx %s  %s tok/s %s  prefix hit %s",
		orDash(d.LocalModel),
		gauge(d, api.GaugeContext, fmt.Sprintf("%s/%s", tokens(d.ContextUsed), tokens(d.ContextWindow))),
		m.spark(api.GaugeTokensPerSec),
		gauge(d, api.GaugeTokensPerSec, fmt.Sprintf("%.1f", d.TokensPerSec)),
		gauge(d, api.GaugePrefixHit, fmt.Sprintf("%.0f%%", pct(d.PrefixHit))))
}

func (m Model) hostedRow() string {
	d := m.diag

	return fmt.Sprintf("hosted %s today  %s calls  cache read %s  p50 %s  last %s",
		spend(d.SpendToday),
		gauge(d, api.GaugeHostedCalls, strconv.Itoa(d.HostedCalls)),
		gauge(d, api.GaugeCacheRead, fmt.Sprintf("%.0f%%", pct(d.CacheRead))),
		gauge(d, api.GaugeHostedLatency, millis(d.HostedP50Ms)),
		gauge(d, api.GaugeHostedLatency, millis(d.HostedLastMs)))
}

func (m Model) gatesRow() string {
	d := m.diag

	return fmt.Sprintf("gates  queue %d  running %s  p50 %s  fail %d/%d",
		d.GateQueue,
		gauge(d, api.GaugeGateRunning, orDash(d.GateRunning)),
		gauge(d, api.GaugeGateLatency, millis(d.GateP50Ms)),
		d.GateFailures, d.GateRuns)
}

func (m Model) leasesRow() string {
	d := m.diag

	return fmt.Sprintf("leases %s held  %s waiting %s",
		gauge(d, api.GaugeLeases, strconv.Itoa(d.LeasesHeld)),
		gauge(d, api.GaugeLeases, strconv.Itoa(d.LeasesWaiting)),
		gauge(d, api.GaugeLeases, orDash(d.LeaseWaitOn)))
}

func (m Model) toolsRow() string {
	d := m.diag

	return fmt.Sprintf("tools  %d calls  %d malformed (%s)  %s escalations",
		d.ToolCalls, d.Malformed, ratio(d.Malformed, d.ToolCalls),
		gauge(d, api.GaugeEscalations, strconv.Itoa(d.Escalations)))
}

func (m Model) eventsRow() string {
	d := m.diag

	return fmt.Sprintf("events %.0f/s  transcript %s rows  compaction %s runs  saved %s tok",
		d.EventsPerSec, tokens(d.TranscriptRows),
		gauge(d, api.GaugeCompaction, strconv.Itoa(d.CompactionRuns)),
		gauge(d, api.GaugeCompaction, tokens(d.TokensSaved)))
}

// diagDrill is the per-thread breakdown behind Enter: the numbers the panel
// sums, attributed to the thread that produced them.
func (m Model) diagDrill() []string {
	rows := m.diag.PerThread
	if len(rows) == 0 {
		return []string{m.th.fgMuted.Render("  no threads")}
	}

	out := make([]string, 0, len(rows)+1)
	out = append(out, m.th.fgMuted.Render(
		fmt.Sprintf("  %-22s %-14s %-9s %-8s %s", "thread", "dir", "ctx", "tokens", "spend"),
	))

	for i := range rows {
		r := &rows[i]
		out = append(out, fmt.Sprintf("  %-22s %-14s %-9s %-8s %s",
			truncate(r.Name, drillNameWidth), truncate(r.Dir, drillDirWidth),
			tokens(r.Context)+"/"+tokens(r.Window), tokens(r.Tokens), spend(r.Spend)))
	}

	return out
}

const (
	drillNameWidth = 21
	drillDirWidth  = 13
)

// gauge renders a number the daemon measured, or a dash where it reported
// the gauge unmeasured. A zero that means "no source" must never read as a
// zero that was measured.
func gauge(d api.Diagnostics, g api.Gauge, value string) string {
	if !d.Measured(g) {
		return "-"
	}

	return value
}

func memPair(d api.Diagnostics) string {
	return bytesGB(d.MemUsedBytes) + "/" + bytesGB(d.MemTotalBytes)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// tokens renders a count the way the mockups do: thousands as "3.1k".
func tokens(n int) string {
	const k = 1000

	if n < k {
		return strconv.Itoa(n)
	}

	return fmt.Sprintf("%.1fk", float64(n)/k)
}

func millis(ms float64) string {
	const secondMS = 1000

	if ms < secondMS {
		return fmt.Sprintf("%.0fms", ms)
	}

	return fmt.Sprintf("%.1fs", ms/secondMS)
}

// ratio renders part of whole as a percentage, dashed where there is nothing
// to divide by.
func ratio(part, whole int) string {
	if whole == 0 {
		return "-"
	}

	return fmt.Sprintf("%.1f%%", pct(float64(part)/float64(whole)))
}
