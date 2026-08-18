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
	keyTab   = "tab"
	keyShTab = "shift+tab"
	keyHome  = "home"

	labelBack     = "back"
	labelHome     = "home"
	labelPanel    = "panel"
	labelSend     = "send"
	labelCancel   = "cancel"
	labelHelp     = "help"
	labelInbox    = "inbox"
	labelRoutines = "routines"
	labelOpen     = "open"
	labelQuit     = "quit"
	labelUndo     = "undo"
	labelModels   = "models"
	labelApply    = "apply"

	kindVerb    = "verb"
	msgNoThread = "no thread selected"
)

// boxPad is the horizontal space a frame's border and inner padding consume,
// so a body line's usable width is the frame width minus this.
const boxPad = 4

// promptWidth is the width of the "> " an input line is rendered behind.
const promptWidth = 2

// inputCursorCell is the extra column bubbles/v2 textinput renders beyond
// its configured width: SetWidth(n) measures n+1 columns, so sizing a field
// to the space available overflows its frame by one.
const inputCursorCell = 1

// fitInput sizes an input to the columns actually available to it: the
// frame's inner width less the prompt it sits behind and the cursor cell.
func fitInput(width int, behindPrompt bool) int {
	avail := width - boxPad - inputCursorCell
	if behindPrompt {
		avail -= promptWidth
	}

	return max(avail, 1)
}

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
