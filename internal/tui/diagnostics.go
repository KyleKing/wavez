package tui

import (
	"fmt"

	"github.com/kyleking/wavez/internal/api"
)

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

func diagnosticsHints() []hint {
	return []hint{{keyEsc, labelBack}, {"?", labelHelp}}
}

// pct converts a 0..1 ratio to a whole percentage for display.
func pct(ratio float64) float64 {
	const percent = 100

	return ratio * percent
}

func (m Model) renderDiagnostics() string {
	d := m.diag

	memLine := fmt.Sprintf("mem   %s  model %s resident",
		gauge(d, api.GaugeMemory, memPair(d)), gauge(d, api.GaugeModelBytes, bytesGB(d.ModelBytes)))
	localLine := fmt.Sprintf("local %s  tok/s %s  prefix hit %s",
		orDash(d.LocalModel),
		gauge(d, api.GaugeTokensPerSec, fmt.Sprintf("%.1f", d.TokensPerSec)),
		gauge(d, api.GaugePrefixHit, fmt.Sprintf("%.0f%%", pct(d.PrefixHit))))
	hostedLine := fmt.Sprintf("hosted %s today  cache read %s",
		spend(d.SpendToday), gauge(d, api.GaugeCacheRead, fmt.Sprintf("%.0f%%", pct(d.CacheRead))))

	body := []string{
		memLine,
		localLine,
		hostedLine,
		fmt.Sprintf("gates queue %d  runs %d  fail %d", d.GateQueue, d.GateRuns, d.GateFailures),
		fmt.Sprintf("leases %d held  %d waiting", d.LeasesHeld, d.LeasesWaiting),
		fmt.Sprintf("threads %d  needs input %d", d.Threads, d.NeedsInput),
		fmt.Sprintf("tools %d calls  %d malformed", d.ToolCalls, d.Malformed),
	}

	footer := footerHints(diagnosticsHints(), m.width-boxPad)

	return frame(m.width, "diagnostics", body, footer, m.th)
}

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
