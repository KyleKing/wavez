package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// frame draws the single-box-with-embedded-title shape every DESIGN.md
// mockup uses: a title baked into the top rule, content lines padded to
// width, and a footer baked into the bottom rule. The border always uses
// the focused style since exactly one screen frame renders at a time;
// Tab-cycling between sub-panels within a screen is styled inline by that
// screen's own render function instead.
func frame(width int, title string, body []string, footer string, th theme) string {
	border := th.borderFocus

	var b strings.Builder
	b.WriteString(border.Render(rule('┌', '┐', title, width)) + "\n")

	inner := width - boxPad
	if inner < 0 {
		inner = 0
	}

	for _, line := range body {
		left := border.Render("│ ")
		right := border.Render(" │")
		b.WriteString(left + padRight(line, inner) + right + "\n")
	}

	b.WriteString(border.Render(rule('└', '┘', footer, width)))

	return b.String()
}

// ruleFixed is the width a rule's corners and padding spaces consume:
// "┌ " + label + " ┐".
const ruleFixed = 4

// rule builds one border line: a corner, the label, and fill dashes up to
// width. A label too wide for width is truncated rather than overflowing.
func rule(left, right rune, label string, width int) string {
	avail := width - ruleFixed
	if avail < 0 {
		avail = 0
	}
	if lipgloss.Width(label) > avail {
		label = truncate(label, avail)
	}

	fillLen := avail - lipgloss.Width(label)
	if fillLen < 0 {
		fillLen = 0
	}

	var b strings.Builder
	b.WriteRune(left)
	b.WriteByte(' ')
	b.WriteString(label)
	b.WriteByte(' ')
	b.WriteString(strings.Repeat("─", fillLen))
	b.WriteRune(right)

	return b.String()
}
