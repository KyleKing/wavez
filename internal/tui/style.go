package tui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

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
	// searchHit is reverse video rather than a color so a search stays
	// legible under NO_COLOR and on a monochrome terminal.
	searchHit lipgloss.Style
	// cursor is the composer's block cursor, drawn by wavez rather than by
	// the terminal because the composer renders inside a frame the real
	// cursor cannot be parked in. Reverse video for the same reason
	// searchHit uses it.
	cursor lipgloss.Style
	// input carries the bubbles textinput styles, which default to hardcoded
	// ANSI colors for the placeholder, blurred text, and cursor that no
	// surrounding lipgloss style can suppress.
	input textinput.Styles
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
			searchHit:   lipgloss.NewStyle().Reverse(true),
			cursor:      lipgloss.NewStyle().Reverse(true),
			input:       monoInputStyles(),
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
		searchHit:   lipgloss.NewStyle().Reverse(true),
		cursor:      lipgloss.NewStyle().Reverse(true),
		input:       textinput.DefaultDarkStyles(),
	}
}

func monoInputStyles() textinput.Styles {
	plain := textinput.StyleState{
		Text:        lipgloss.NewStyle(),
		Placeholder: lipgloss.NewStyle().Faint(true),
		Suggestion:  lipgloss.NewStyle().Faint(true),
		Prompt:      lipgloss.NewStyle(),
	}

	return textinput.Styles{
		Focused: plain,
		Blurred: plain,
		// A nil cursor color leaves the virtual cursor's reverse-video block,
		// which is an attribute rather than a color.
		Cursor: textinput.CursorStyle{Shape: tea.CursorBlock, Blink: true},
	}
}

// newInput builds a textinput whose styles come from the theme rather than
// from bubbles' color defaults.
func (t theme) newInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.Prompt = ""
	ti.SetStyles(t.input)

	return ti
}
