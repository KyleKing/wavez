package tui

import "charm.land/lipgloss/v2"

// theme holds the semantic color slots the whole TUI renders through. It is
// never referenced directly by hex value outside this file, so a NO_COLOR
// theme (every slot empty) degrades every view uniformly.
type theme struct {
	fgDefault   lipgloss.Style
	fgMuted     lipgloss.Style
	fgEmphasis  lipgloss.Style
	accent      lipgloss.Style
	statusOK    lipgloss.Style
	statusWarn  lipgloss.Style
	statusErr   lipgloss.Style
	statusInfo  lipgloss.Style
	borderDim   lipgloss.Style
	borderFocus lipgloss.Style
}

func newTheme(noColor bool) theme {
	if noColor {
		return theme{
			fgDefault:   lipgloss.NewStyle(),
			fgMuted:     lipgloss.NewStyle().Faint(true),
			fgEmphasis:  lipgloss.NewStyle().Bold(true),
			accent:      lipgloss.NewStyle().Bold(true),
			statusOK:    lipgloss.NewStyle(),
			statusWarn:  lipgloss.NewStyle().Bold(true),
			statusErr:   lipgloss.NewStyle().Bold(true),
			statusInfo:  lipgloss.NewStyle(),
			borderDim:   lipgloss.NewStyle().Faint(true),
			borderFocus: lipgloss.NewStyle().Bold(true),
		}
	}

	return theme{
		fgDefault:   lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		fgMuted:     lipgloss.NewStyle().Foreground(lipgloss.Color("242")),
		fgEmphasis:  lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true),
		accent:      lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true),
		statusOK:    lipgloss.NewStyle().Foreground(lipgloss.Color("108")),
		statusWarn:  lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		statusErr:   lipgloss.NewStyle().Foreground(lipgloss.Color("203")),
		statusInfo:  lipgloss.NewStyle().Foreground(lipgloss.Color("111")),
		borderDim:   lipgloss.NewStyle().Foreground(lipgloss.Color("242")),
		borderFocus: lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true),
	}
}
