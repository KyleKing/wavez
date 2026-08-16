package tui

import (
	"fmt"
	"strings"
)

// hint is one footer entry. Hints are supplied in priority order (highest
// first); footerHints drops from the tail as width shrinks.
type hint struct {
	key   string
	label string
}

// Key strings, as tea.KeyPressMsg.String() reports them, and hint labels
// repeated across screens.
const (
	keyEnter = "enter"
	keyEsc   = "esc"
	keyDown  = "down"
	keyUp    = "up"
	keyJ     = "j"
	keyK     = "k"

	labelBack  = "back"
	labelHelp  = "help"
	labelInbox = "inbox"
	labelOpen  = "open"
	labelQuit  = "quit"
)

// boxPad is the horizontal space a frame's border and inner padding consume,
// so a body line's usable width is the frame width minus this.
const boxPad = 4

// footerHints renders as many hints as fit in width, highest priority first.
func footerHints(hints []hint, width int) string {
	var b strings.Builder

	used := 0
	for _, h := range hints {
		seg := fmt.Sprintf("[%s]%s", h.key, h.label)

		add := len(seg)
		if used > 0 {
			add++
		}
		if used > 0 && used+add > width {
			break
		}

		if used > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(seg)
		used += add
	}

	return b.String()
}
