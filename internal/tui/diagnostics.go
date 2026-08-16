package tui

import (
	"fmt"

	"github.com/kyleking/wavez/internal/api"
)

// diagStrip renders the one-line summary embedded in every screen's header,
// fed entirely from api.Diagnostics.
func diagStrip(d api.Diagnostics) string {
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

	memLine := fmt.Sprintf("mem   %s/%s  model %s resident",
		bytesGB(d.MemUsedBytes), bytesGB(d.MemTotalBytes), bytesGB(d.ModelBytes))
	localLine := fmt.Sprintf("local %s  tok/s %.1f  prefix hit %.0f%%",
		orDash(d.LocalModel), d.TokensPerSec, pct(d.PrefixHit))
	hostedLine := fmt.Sprintf("hosted %s today  cache read %.0f%%", spend(d.SpendToday), pct(d.CacheRead))

	body := []string{
		memLine,
		localLine,
		hostedLine,
		fmt.Sprintf("gates queue %d  runs %d  fail %d", d.GateQueue, d.GateRuns, d.GateFailures),
		fmt.Sprintf("threads %d  needs input %d", d.Threads, d.NeedsInput),
		fmt.Sprintf("tools %d calls  %d malformed", d.ToolCalls, d.Malformed),
	}

	footer := footerHints(diagnosticsHints(), m.width-boxPad)

	return frame(m.width, "diagnostics", body, footer, m.th)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}
