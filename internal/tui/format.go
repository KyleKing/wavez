package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// age renders the elapsed time since t in the compact form Home and the
// inbox use ("2m", "40s", "12m").
func age(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}

	d := now.Sub(t)
	if d < 0 {
		d = 0
	}

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// spend renders a dollar amount to two decimal places.
func spend(v float64) string {
	return fmt.Sprintf("$%.2f", v)
}

// truncate shortens s to at most n runes, marking the cut with an ellipsis.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}

	if lipgloss.Width(s) <= n {
		return s
	}

	return ansi.Truncate(s, n, "…")
}

// padRight pads or truncates s to exactly width terminal cells, measuring
// by display width rather than rune count: a state glyph like ▲ is one rune
// but two cells, and a rune-counted pad leaves box-drawing borders ragged.
func padRight(s string, width int) string {
	w := lipgloss.Width(s)
	if w > width {
		return truncate(s, width)
	}

	return s + strings.Repeat(" ", width-w)
}
